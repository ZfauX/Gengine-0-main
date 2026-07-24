package user

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/audit"
	csrf2 "gengine-0/internal/pkg/csrf"
	"gengine-0/internal/pkg/render"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	gowebauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

const webauthnSessionKey = "webauthn_registration"
const webauthnLoginSessionKey = "webauthn_login"

type WebAuthnHandler struct {
	cfg          *config.Config
	webAuthn     *gowebauthn.WebAuthn
	authSvc      *AuthService
	userRepo     UserRepository
	webauthnRepo WebAuthnRepository
	auditSvc     *audit.Service
}

func NewWebAuthnHandler(
	cfg *config.Config,
	authSvc *AuthService,
	userRepo UserRepository,
	webauthnRepo WebAuthnRepository,
	auditSvc *audit.Service,
) (*WebAuthnHandler, error) {
	baseURL, err := url.Parse(cfg.Server.BaseURL)
	if err != nil {
		return nil, err
	}
	rpID := baseURL.Hostname()
	rpOrigins := []string{cfg.Server.BaseURL}

	wcfg := &gowebauthn.Config{
		RPDisplayName: "Encounter Engine",
		RPID:          rpID,
		RPOrigins:     rpOrigins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			UserVerification: protocol.VerificationPreferred,
		},
		AttestationPreference: protocol.PreferDirectAttestation,
	}

	webAuthn, err := gowebauthn.New(wcfg)
	if err != nil {
		return nil, err
	}

	return &WebAuthnHandler{
		cfg:          cfg,
		webAuthn:     webAuthn,
		authSvc:      authSvc,
		userRepo:     userRepo,
		webauthnRepo: webauthnRepo,
		auditSvc:     auditSvc,
	}, nil
}

type BeginRegistrationInput struct {
	Name string `json:"name" form:"name"`
}

func (h *WebAuthnHandler) BeginRegistration(c *gin.Context) {
	userID := c.GetUint("userID")
	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Пользователь не найден"})
		return
	}

	creds, err := h.webauthnRepo.ListByUserID(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("BeginRegistration: failed to list credentials")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Внутренняя ошибка"})
		return
	}

	libCreds := make([]gowebauthn.Credential, len(creds))
	for i, wc := range creds {
		libCreds[i] = toLibraryCredential(&wc)
	}

	waUser := NewWebAuthnUser(user, libCreds)

	var input BeginRegistrationInput
	if bindErr := c.ShouldBindJSON(&input); bindErr != nil {
		input.Name = ""
	}

	opts := []gowebauthn.RegistrationOption{}
	if len(libCreds) > 0 {
		descs := make([]protocol.CredentialDescriptor, len(libCreds))
		for i, cred := range libCreds {
			descs[i] = protocol.CredentialDescriptor{
				Type:         protocol.PublicKeyCredentialType,
				CredentialID: cred.ID,
			}
		}
		opts = append(opts, gowebauthn.WithExclusions(descs))
	}

	creation, sessionData, err := h.webAuthn.BeginRegistration(waUser, opts...)
	if err != nil {
		log.Error().Err(err).Msg("BeginRegistration: failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка начала регистрации"})
		return
	}

	sess := sessions.Default(c)
	sessionDataJSON, _ := json.Marshal(sessionData)
	sess.Set(webauthnSessionKey, string(sessionDataJSON))
	if input.Name != "" {
		sess.Set(webauthnSessionKey+"_name", input.Name)
	}
	if err := sess.Save(); err != nil {
		log.Error().Err(err).Msg("BeginRegistration: failed to save session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Внутренняя ошибка"})
		return
	}

	c.JSON(http.StatusOK, creation)
}

func (h *WebAuthnHandler) FinishRegistration(c *gin.Context) {
	userID := c.GetUint("userID")
	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Пользователь не найден"})
		return
	}

	sess := sessions.Default(c)
	sessionDataStr := sess.Get(webauthnSessionKey)
	if sessionDataStr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Сессия не найдена. Начните регистрацию заново."})
		return
	}

	sessionDataRaw, sessionOK := sessionDataStr.(string)
	if !sessionOK {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные сессии"})
		return
	}

	var sessionData gowebauthn.SessionData
	if parseErr := json.Unmarshal([]byte(sessionDataRaw), &sessionData); parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные сессии"})
		return
	}

	credName, _ := sess.Get(webauthnSessionKey + "_name").(string)

	sess.Delete(webauthnSessionKey)
	sess.Delete(webauthnSessionKey + "_name")
	if saveErr := sess.Save(); saveErr != nil {
		log.Error().Err(saveErr).Msg("FinishRegistration: failed to clear session")
	}

	creds, err := h.webauthnRepo.ListByUserID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Внутренняя ошибка"})
		return
	}

	libCreds := make([]gowebauthn.Credential, len(creds))
	for i, wc := range creds {
		libCreds[i] = toLibraryCredential(&wc)
	}

	waUser := NewWebAuthnUser(user, libCreds)

	body, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные"})
		return
	}

	parsedResponse, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		log.Error().Err(err).Msg("FinishRegistration: failed to parse response")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные аутентификатора"})
		return
	}

	credential, err := h.webAuthn.CreateCredential(waUser, sessionData, parsedResponse)
	if err != nil {
		log.Error().Err(err).Msg("FinishRegistration: CreateCredential failed")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ошибка проверки аутентификатора"})
		return
	}

	wc := &WebAuthnCredential{
		UserID:          userID,
		CredentialID:    credential.ID,
		PublicKey:       credential.PublicKey,
		AttestationType: credential.AttestationType,
		AAGUID:          credential.Authenticator.AAGUID,
		SignCount:       credential.Authenticator.SignCount,
		BackupEligible:  credential.Flags.BackupEligible,
		BackupState:     credential.Flags.BackupState,
		Name:            credName,
	}

	transports := make([]string, len(credential.Transport))
	for i, t := range credential.Transport {
		transports[i] = string(t)
	}
	wc.Transport = transports

	if err := h.webauthnRepo.Create(c.Request.Context(), wc); err != nil {
		log.Error().Err(err).Msg("FinishRegistration: failed to save credential")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка сохранения ключа"})
		return
	}

	h.auditSvc.Log(userID, "webauthn_register", "user", userID, "Passkey зарегистрирован")
	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": wc.ID})
}

func (h *WebAuthnHandler) BeginLogin(c *gin.Context) {
	assertion, sessionData, err := h.webAuthn.BeginDiscoverableLogin()
	if err != nil {
		log.Error().Err(err).Msg("BeginLogin: failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка начала входа"})
		return
	}

	sess := sessions.Default(c)
	sessionDataJSON, _ := json.Marshal(sessionData)
	sess.Set(webauthnLoginSessionKey, string(sessionDataJSON))
	if err := sess.Save(); err != nil {
		log.Error().Err(err).Msg("BeginLogin: failed to save session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Внутренняя ошибка"})
		return
	}

	c.JSON(http.StatusOK, assertion)
}

func (h *WebAuthnHandler) FinishLogin(c *gin.Context) {
	sess := sessions.Default(c)
	sessionDataStr := sess.Get(webauthnLoginSessionKey)
	if sessionDataStr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Сессия не найдена. Начните вход заново."})
		return
	}

	sessionDataRaw, sessionOK := sessionDataStr.(string)
	if !sessionOK {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные сессии"})
		return
	}

	var sessionData gowebauthn.SessionData
	if err := json.Unmarshal([]byte(sessionDataRaw), &sessionData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные сессии"})
		return
	}

	sess.Delete(webauthnLoginSessionKey)
	if saveErr := sess.Save(); saveErr != nil {
		log.Error().Err(saveErr).Msg("FinishLogin: failed to clear session")
	}

	body, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные"})
		return
	}

	parsedResponse, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		log.Error().Err(err).Msg("FinishLogin: failed to parse response")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверные данные аутентификатора"})
		return
	}

	discoverableHandler := func(rawID, userHandle []byte) (gowebauthn.User, error) {
		wc, wcErr := h.webauthnRepo.GetByCredentialID(c.Request.Context(), rawID)
		if wcErr != nil {
			return nil, errors.New("ключ не найден")
		}
		foundUser, userErr := h.userRepo.GetByID(c.Request.Context(), wc.UserID)
		if userErr != nil {
			return nil, errors.New("пользователь не найден")
		}
		allCreds, listErr := h.webauthnRepo.ListByUserID(c.Request.Context(), wc.UserID)
		if listErr != nil {
			return nil, errors.New("ошибка загрузки ключей")
		}
		libCreds := make([]gowebauthn.Credential, len(allCreds))
		for i, c := range allCreds {
			libCreds[i] = toLibraryCredential(&c)
		}
		return NewWebAuthnUser(foundUser, libCreds), nil
	}

	waUser, credential, err := h.webAuthn.ValidatePasskeyLogin(discoverableHandler, sessionData, parsedResponse)
	if err != nil {
		log.Error().Err(err).Msg("FinishLogin: ValidatePasskeyLogin failed")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Ошибка аутентификации"})
		return
	}

	if credential.Authenticator.CloneWarning {
		log.Warn().Bytes("credential_id", credential.ID).Msg("FinishLogin: clone warning detected")
	}

	waUserTyped, waOK := waUser.(*WebAuthnUser)
	if !waOK {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Внутренняя ошибка"})
		return
	}

	token, err := h.authSvc.GenerateJWT(*waUserTyped.user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Внутренняя ошибка"})
		return
	}

	setSecureCookie(c, "jwt", token, int(h.cfg.JWT.AccessExpiry.Seconds()), "/")

	deviceID := c.GetHeader("X-Device-ID")
	refreshToken, err := h.authSvc.GenerateRefreshToken(c.Request.Context(), *waUserTyped.user, deviceID)
	if err == nil {
		setSecureCookie(c, "refresh_token", refreshToken, int(h.cfg.JWT.RefreshExpiry.Seconds()), "/auth/refresh")
	} else {
		log.Error().Err(err).Msg("FinishLogin: failed to generate refresh token")
	}

	wc, err := h.webauthnRepo.GetByCredentialID(c.Request.Context(), credential.ID)
	if err == nil {
		_ = h.webauthnRepo.UpdateSignCount(c.Request.Context(), wc.ID, credential.Authenticator.SignCount, credential.Flags.BackupState)
	}

	h.auditSvc.Log(waUserTyped.user.ID, "webauthn_login", "user", waUserTyped.user.ID, "Вход по passkey")

	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"redirect":  "/dashboard",
		"user_id":   waUserTyped.user.ID,
		"user_name": waUserTyped.user.Name,
	})
}

func (h *WebAuthnHandler) ListKeys(c *gin.Context) {
	userID := c.GetUint("userID")
	creds, err := h.webauthnRepo.ListByUserID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	render.Page(c, http.StatusOK, "webauthn-manage.html", gin.H{
		"Credentials": creds,
		"csrf":        csrf2.GetToken(c),
	})
}

func (h *WebAuthnHandler) DeleteKey(c *gin.Context) {
	userID := c.GetUint("userID")
	id, ok := render.ParseID(c, "id")
	if !ok {
		return
	}

	if err := h.webauthnRepo.Delete(c.Request.Context(), id, userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderError(c, http.StatusNotFound, "Ключ не найден")
			return
		}
		log.Error().Err(err).Uint("id", id).Msg("DeleteKey: failed")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	h.auditSvc.Log(userID, "webauthn_delete", "webauthn_credential", id, "Passkey удалён")
	c.Redirect(http.StatusFound, "/profile/webauthn-keys")
}
