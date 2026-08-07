// internal/domain/user/two_factor_handler.go
package user

import (
	"net/http"
	"strings"
	"time"

	"gengine-0/internal/pkg/i18n"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
)

// TwoFactorHandler РѕР±СЂР°Р±Р°С‚С‹РІР°РµС‚ HTTP-Р·Р°РїСЂРѕСЃС‹ РґР»СЏ 2FA.
type TwoFactorHandler struct {
	twoFactorSvc    *TwoFactorService
	authService     *AuthService
	userRepo        UserRepository
	jwtAccessExpiry time.Duration
}

// NewTwoFactorHandler СЃРѕР·РґР°С‘С‚ РЅРѕРІС‹Р№ handler 2FA.
func NewTwoFactorHandler(twoFactorSvc *TwoFactorService, authService *AuthService, userRepo UserRepository, jwtAccessExpiry time.Duration) *TwoFactorHandler {
	if jwtAccessExpiry <= 0 {
		jwtAccessExpiry = 15 * time.Minute
	}
	return &TwoFactorHandler{
		twoFactorSvc:    twoFactorSvc,
		authService:     authService,
		userRepo:        userRepo,
		jwtAccessExpiry: jwtAccessExpiry,
	}
}

// VerifyForm РѕС‚РѕР±СЂР°Р¶Р°РµС‚ С„РѕСЂРјСѓ РІРІРѕРґР° TOTP-РєРѕРґР°.
// @Summary Р¤РѕСЂРјР° 2FA РІРµСЂРёС„РёРєР°С†РёРё
// @Tags 2fa
// @Produce html
// @Success 200 {string} html "Р¤РѕСЂРјР° 2FA"
// @Router /auth/2fa/verify [get]
func (h *TwoFactorHandler) VerifyForm(c *gin.Context) {
	userID := c.GetUint("userID")
	render.Page(c, http.StatusOK, "admin-2fa-verify.html", gin.H{
		"Title":         "Р”РІСѓС…С„Р°РєС‚РѕСЂРЅР°СЏ Р°СѓС‚РµРЅС‚РёС„РёРєР°С†РёСЏ",
		"Message":       render.Tr(c, "handler.enter_totp_code"),
		"ReturnURL":     safeReturnURL(c.Query("return_url"), "/"),
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"csrf":          csrf.GetToken(c),
	})
}

// Verify РѕР±СЂР°Р±Р°С‚С‹РІР°РµС‚ РІРІРѕРґ TOTP-РєРѕРґР° Рё СѓСЃС‚Р°РЅР°РІР»РёРІР°РµС‚ С„Р»Р°Рі РІ СЃРµСЃСЃРёРё.
// @Summary РџСЂРѕРІРµСЂРєР° TOTP-РєРѕРґР°
// @Tags 2fa
// @Accept x-www-form-urlencoded
// @Produce html
// @Param code formData string true "TOTP-РєРѕРґ"
// @Success 302 {string} string "РџРµСЂРµРЅР°РїСЂР°РІР»РµРЅРёРµ"
// @Router /auth/2fa/verify [post]
func (h *TwoFactorHandler) Verify(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	code := c.PostForm("code")
	returnURL := safeReturnURL(c.DefaultPostForm("return_url", ""), "/")

	baseData := func() gin.H {
		return gin.H{
			"Title":         "Р”РІСѓС…С„Р°РєС‚РѕСЂРЅР°СЏ Р°СѓС‚РµРЅС‚РёС„РёРєР°С†РёСЏ",
			"Message":       render.Tr(c, "handler.enter_totp_code"),
			"ReturnURL":     returnURL,
			"CurrentUserID": userID,
			"IsAdmin":       middleware.IsAdmin(c),
			"csrf":          csrf.GetToken(c),
		}
	}

	if err := h.twoFactorSvc.Validate2FAInput(code); err != nil {
		data := baseData()
		data["Error"] = err.Error()
		render.Page(c, http.StatusOK, "admin-2fa-verify.html", data)
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	valid, err := h.twoFactorSvc.VerifyCode(user.TwoFactorSecret, code)
	if err != nil || !valid {
		data := baseData()
		data["Error"] = render.Tr(c, "handler.wrong_code_try_again")
		render.Page(c, http.StatusOK, "admin-2fa-verify.html", data)
		return
	}

	// РЎРѕС…СЂР°РЅСЏРµРј С„Р»Р°Рі РІРµСЂРёС„РёРєР°С†РёРё РІ СЃРµСЃСЃРёРё (РїСЂРёРІСЏР·Р°РЅ Рє userID)
	session := sessions.Default(c)
	set2FAVerified(session, userID)
	if err := session.Save(); err != nil {
		log.Error().Err(err).Msg("Verify: failed to save session")
	}

	// Session fixation protection: РїРµСЂРµРІС‹РїСѓСЃРєР°РµРј JWT СЃ РЅРѕРІС‹Рј jti.
	// Cookie store РЅРµ РїРѕРґРґРµСЂР¶РёРІР°РµС‚ session.Regenerate(), РїРѕСЌС‚РѕРјСѓ РІС‹РґР°С‘Рј РЅРѕРІС‹Р№ access-С‚РѕРєРµРЅ
	// вЂ” СЃС‚Р°СЂС‹Рµ (РїРµСЂРµС…РІР°С‡РµРЅРЅС‹Рµ РґРѕ 2FA) СЃС‚Р°РЅРѕРІСЏС‚СЃСЏ РЅРµРІР°Р»РёРґРЅС‹ С‡РµСЂРµР· jti blacklist.
	if userObj, err := h.userRepo.GetByID(c.Request.Context(), userID); err == nil {
		if newToken, jwtErr := h.authService.GenerateJWT(*userObj); jwtErr == nil {
			setSecureCookie(c, "jwt", newToken, int(h.jwtAccessExpiry.Seconds()), "/")
		} else {
			log.Error().Err(jwtErr).Uint("user_id", userID).Msg("Verify: failed to regenerate JWT")
		}
	}

	c.Redirect(http.StatusFound, returnURL)
}

// BackupForm РѕС‚РѕР±СЂР°Р¶Р°РµС‚ С„РѕСЂРјСѓ РІРІРѕРґР° СЂРµР·РµСЂРІРЅРѕРіРѕ РєРѕРґР°.
// @Summary Р¤РѕСЂРјР° СЂРµР·РµСЂРІРЅРѕРіРѕ РєРѕРґР° 2FA
// @Tags 2fa
// @Produce html
// @Success 200 {string} html "Р¤РѕСЂРјР° СЂРµР·РµСЂРІРЅРѕРіРѕ РєРѕРґР°"
// @Router /auth/2fa/backup [get]
func (h *TwoFactorHandler) BackupForm(c *gin.Context) {
	userID := c.GetUint("userID")
	render.Page(c, http.StatusOK, "admin-2fa-backup.html", gin.H{
		"Title":         "Р РµР·РµСЂРІРЅС‹Р№ РєРѕРґ 2FA",
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"csrf":          csrf.GetToken(c),
	})
}

// BackupVerify РѕР±СЂР°Р±Р°С‚С‹РІР°РµС‚ РІРІРѕРґ СЂРµР·РµСЂРІРЅРѕРіРѕ РєРѕРґР°.
// @Summary РџСЂРѕРІРµСЂРєР° СЂРµР·РµСЂРІРЅРѕРіРѕ РєРѕРґР°
// @Tags 2fa
// @Accept x-www-form-urlencoded
// @Produce html
// @Param backup_code formData string true "Р РµР·РµСЂРІРЅС‹Р№ РєРѕРґ"
// @Success 302 {string} string "РџРµСЂРµРЅР°РїСЂР°РІР»РµРЅРёРµ"
// @Router /auth/2fa/backup [post]
func (h *TwoFactorHandler) BackupVerify(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	baseData := func() gin.H {
		return gin.H{
			"Title":         "Р РµР·РµСЂРІРЅС‹Р№ РєРѕРґ 2FA",
			"CurrentUserID": userID,
			"IsAdmin":       middleware.IsAdmin(c),
			"csrf":          csrf.GetToken(c),
		}
	}

	backupCode := c.PostForm("backup_code")
	if backupCode == "" {
		data := baseData()
		data["Error"] = render.Tr(c, "handler.enter_backup_code")
		render.Page(c, http.StatusOK, "admin-2fa-backup.html", data)
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	// Verify and remove the used backup code
	remainingCodes, verr := h.twoFactorSvc.VerifyAndRemoveBackupCode(user.TwoFactorBackupCodes, backupCode)
	if verr != nil {
		data := baseData()
		data["Error"] = "РќРµРІРµСЂРЅС‹Р№ СЂРµР·РµСЂРІРЅС‹Р№ РєРѕРґ"
		render.Page(c, http.StatusOK, "admin-2fa-backup.html", data)
		return
	}

	// Persist remaining backup codes
	if err := h.userRepo.Update(c.Request.Context(), user.ID, map[string]any{
		"two_factor_backup_codes": remainingCodes,
	}); err != nil {
		log.Error().Err(err).Uint("user_id", user.ID).Msg("BackupVerify: failed to update backup codes")
	}

	// РЎРѕС…СЂР°РЅСЏРµРј С„Р»Р°Рі РІРµСЂРёС„РёРєР°С†РёРё РІ СЃРµСЃСЃРёРё (РїСЂРёРІСЏР·Р°РЅ Рє userID)
	session := sessions.Default(c)
	set2FAVerified(session, userID)
	if err := session.Save(); err != nil {
		log.Error().Err(err).Msg("BackupVerify: failed to save session")
	}

	c.Redirect(http.StatusFound, "/")
}

// EnableForm РѕС‚РѕР±СЂР°Р¶Р°РµС‚ С„РѕСЂРјСѓ РІРєР»СЋС‡РµРЅРёСЏ 2FA.
// @Summary Р¤РѕСЂРјР° РІРєР»СЋС‡РµРЅРёСЏ 2FA
// @Tags 2fa
// @Produce html
// @Success 200 {string} html "Р¤РѕСЂРјР° РІРєР»СЋС‡РµРЅРёСЏ 2FA"
// @Failure 401 {object} map[string]interface{} "РўСЂРµР±СѓРµС‚СЃСЏ Р°СѓС‚РµРЅС‚РёС„РёРєР°С†РёСЏ"
// @Router /user/2fa/enable [get]
// @Security JWT
func (h *TwoFactorHandler) EnableForm(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	if user.TwoFactorEnabled {
		render.Page(c, http.StatusOK, "user-2fa-enabled.html", gin.H{
			"Title": render.Tr(c, "handler.2fa_already_enabled"),
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Р“РµРЅРµСЂРёСЂСѓРµРј СЃРµРєСЂРµС‚ Рё QR-РєРѕРґ
	secret, err := h.twoFactorSvc.GenerateSecret()
	if err != nil {
		log.Error().Err(err).Msg("EnableForm: failed to generate 2FA secret")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	qrURL, qrErr := h.twoFactorSvc.GenerateQRCodeURL(secret, user.Email, "Gengine-0")
	if qrErr != nil {
		log.Error().Err(qrErr).Msg("EnableForm: failed to generate QR code URL")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	// РЎРѕС…СЂР°РЅСЏРµРј СЃРµРєСЂРµС‚ РІ СЃРµСЃСЃРёРё РґР»СЏ РїРѕРґС‚РІРµСЂР¶РґРµРЅРёСЏ РЅР° СЃР»РµРґСѓСЋС‰РµРј С€Р°РіРµ
	sess := sessions.Default(c)
	sess.Set("2fa_pending_secret", secret)
	sess.Set("2fa_pending_secret_url", qrURL)
	if err := sess.Save(); err != nil {
		log.Error().Err(err).Msg("EnableForm: failed to save session")
	}

	render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
		"Title":         "Р’РєР»СЋС‡РёС‚СЊ 2FA",
		"User":          user.ToPublic(),
		"Secret":        secret,
		"QRURL":         qrURL,
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"csrf":          csrf.GetToken(c),
	})
}

// Enable РїРѕРґС‚РІРµСЂР¶РґР°РµС‚ РІРєР»СЋС‡РµРЅРёРµ 2FA.
// @Summary РџРѕРґС‚РІРµСЂР¶РґРµРЅРёРµ РІРєР»СЋС‡РµРЅРёСЏ 2FA
// @Tags 2fa
// @Accept x-www-form-urlencoded
// @Produce html
// @Param code formData string true "РљРѕРґ РїРѕРґС‚РІРµСЂР¶РґРµРЅРёСЏ"
// @Success 302 {string} string "РџРµСЂРµРЅР°РїСЂР°РІР»РµРЅРёРµ РІ РїСЂРѕС„РёР»СЊ"
// @Failure 400 {object} map[string]interface{} render.Tr(c, "handler.invalid_code")
// @Failure 401 {object} map[string]interface{} "РўСЂРµР±СѓРµС‚СЃСЏ Р°СѓС‚РµРЅС‚РёС„РёРєР°С†РёСЏ"
// @Router /user/2fa/enable [post]
// @Security JWT
func (h *TwoFactorHandler) Enable(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	var input struct {
		Code     string `form:"code" binding:"required"`
		Password string `form:"password" binding:"required"`
	}
	if err := c.ShouldBind(&input); err != nil {
		c.Redirect(http.StatusFound, "/user/2fa/enable")
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	// S2: РІРєР»СЋС‡РµРЅРёРµ 2FA С‚СЂРµР±СѓРµС‚ РїРѕРґС‚РІРµСЂР¶РґРµРЅРёСЏ РїР°СЂРѕР»СЏ. РРЅР°С‡Рµ Р°С‚Р°РєСѓСЋС‰РёР№ СЃ
	// СѓРєСЂР°РґРµРЅРЅРѕР№ СЃРµСЃСЃРёРµР№ РїСЂРёРІСЏР·Р°Р» Р±С‹ СЃРІРѕР№ TOTP-СЃРµРєСЂРµС‚ Рє С‡СѓР¶РѕРјСѓ Р°РєРєР°СѓРЅС‚Сѓ.
	renderEnableError := func(msg string) {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title": render.Tr(c, "twofa.page_title"),
			"Error": msg,
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
	}
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.Password)) != nil {
		renderEnableError(render.Tr(c, "handler.wrong_password"))
		return
	}

	// РџРѕР»СѓС‡Р°РµРј СЃРµРєСЂРµС‚ РёР· СЃРµСЃСЃРёРё (СЃРіРµРЅРµСЂРёСЂРѕРІР°РЅ РЅР° С€Р°РіРµ EnableForm)
	sess := sessions.Default(c)
	pendingSecret, ok := sess.Get("2fa_pending_secret").(string)
	if !ok || pendingSecret == "" {
		render.Page(c, http.StatusBadRequest, "user-2fa-enable.html", gin.H{
			"Title": render.Tr(c, "twofa.page_title"),
			"Error": "РЎРµСЃСЃРёСЏ РёСЃС‚РµРєР»Р°. РќР°С‡РЅРёС‚Рµ РЅР°СЃС‚СЂРѕР№РєСѓ 2FA Р·Р°РЅРѕРІРѕ.",
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// РџСЂРѕРІРµСЂСЏРµРј РєРѕРґ РїСЂРѕС‚РёРІ СЃРѕС…СЂР°РЅС‘РЅРЅРѕРіРѕ СЃРµРєСЂРµС‚Р°
	valid, err := h.twoFactorSvc.VerifyCode(pendingSecret, input.Code)
	if err != nil || !valid {
		renderEnableError(render.Tr(c, "handler.wrong_code_try_again"))
		return
	}

	// Р“РµРЅРµСЂРёСЂСѓРµРј СЂРµР·РµСЂРІРЅС‹Рµ РєРѕРґС‹
	backupCodes, err := h.twoFactorSvc.GenerateBackupCodes()
	if err != nil {
		renderEnableError(render.LocalizeError(c, err.Error()))
		return
	}

	hashedCodes, err := h.twoFactorSvc.HashBackupCodes(backupCodes)
	if err != nil {
		renderEnableError(render.LocalizeError(c, err.Error()))
		return
	}

	// Р’РєР»СЋС‡Р°РµРј 2FA СЃ РїРѕРґС‚РІРµСЂР¶РґС‘РЅРЅС‹Рј СЃРµРєСЂРµС‚РѕРј
	user.TwoFactorEnabled = true
	user.TwoFactorSecret = pendingSecret
	user.TwoFactorBackupCodes = hashedCodes

	// РЎРѕС…СЂР°РЅСЏРµРј РїРѕР»СЊР·РѕРІР°С‚РµР»СЏ
	if err := h.userRepo.Update(c.Request.Context(), user.ID, map[string]any{
		"two_factor_enabled":      true,
		"two_factor_secret":       pendingSecret,
		"two_factor_backup_codes": hashedCodes,
	}); err != nil {
		render.Page(c, http.StatusOK, "user-2fa-enable.html", gin.H{
			"Title": render.Tr(c, "twofa.page_title"),
			"Error": render.LocalizeError(c, err.Error()),
			"User":  user.ToPublic(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// РћС‡РёС‰Р°РµРј СЃРµСЃСЃРёСЋ
	sess.Delete("2fa_pending_secret")
	sess.Delete("2fa_pending_secret_url")
	sess.Delete(session2FAKey(userID))
	if err := sess.Save(); err != nil {
		log.Error().Err(err).Msg("Enable: failed to clear session")
	}

	// РџРѕРєР°Р·С‹РІР°РµРј СЃС‚СЂР°РЅРёС†Сѓ СѓСЃРїРµС…Р° СЃ СЂРµР·РµСЂРІРЅС‹РјРё РєРѕРґР°РјРё
	render.Page(c, http.StatusOK, "user-2fa-enabled.html", gin.H{
		"Title":       "2FA РІРєР»СЋС‡РµРЅР°",
		"User":        user.ToPublic(),
		"BackupCodes": backupCodes,
		"csrf":        csrf.GetToken(c),
	})
}

// DisableForm РѕС‚РѕР±СЂР°Р¶Р°РµС‚ С„РѕСЂРјСѓ РѕС‚РєР»СЋС‡РµРЅРёСЏ 2FA.
// @Summary Р¤РѕСЂРјР° РѕС‚РєР»СЋС‡РµРЅРёСЏ 2FA
// @Tags 2fa
// @Produce html
// @Success 200 {string} html "Р¤РѕСЂРјР° РѕС‚РєР»СЋС‡РµРЅРёСЏ 2FA"
// @Failure 401 {object} map[string]interface{} "РўСЂРµР±СѓРµС‚СЃСЏ Р°СѓС‚РµРЅС‚РёС„РёРєР°С†РёСЏ"
// @Router /user/2fa/disable [get]
// @Security JWT
func (h *TwoFactorHandler) DisableForm(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	if !user.TwoFactorEnabled {
		c.Redirect(http.StatusFound, "/user/profile")
		return
	}

	render.Page(c, http.StatusOK, "user-2fa-disable.html", gin.H{
		"Title":         "РћС‚РєР»СЋС‡РёС‚СЊ 2FA",
		"User":          user.ToPublic(),
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"csrf":          csrf.GetToken(c),
	})
}

// Disable РѕС‚РєР»СЋС‡Р°РµС‚ 2FA.
// @Summary РћС‚РєР»СЋС‡РµРЅРёРµ 2FA
// @Tags 2fa
// @Accept x-www-form-urlencoded
// @Produce html
// @Param code formData string true "РљРѕРґ РїРѕРґС‚РІРµСЂР¶РґРµРЅРёСЏ"
// @Success 302 {string} string "РџРµСЂРµРЅР°РїСЂР°РІР»РµРЅРёРµ РІ РїСЂРѕС„РёР»СЊ"
// @Failure 400 {object} map[string]interface{} render.Tr(c, "handler.invalid_code")
// @Failure 401 {object} map[string]interface{} "РўСЂРµР±СѓРµС‚СЃСЏ Р°СѓС‚РµРЅС‚РёС„РёРєР°С†РёСЏ"
// @Router /user/2fa/disable [post]
// @Security JWT
func (h *TwoFactorHandler) Disable(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	var input struct {
		Password string `form:"password" binding:"required"`
		Code     string `form:"code"`
	}
	if err := c.ShouldBind(&input); err != nil {
		render.SetFlash(c, "error", i18n.T("twofa.enter_password"))
		c.Redirect(http.StatusFound, "/user/2fa/disable")
		return
	}

	user, err := h.userRepo.GetByID(c.Request.Context(), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	// РџСЂРѕРІРµСЂСЏРµРј РїР°СЂРѕР»СЊ РЅР°РїСЂСЏРјСѓСЋ (РљСЂРёС‚#2): РїРѕР»РЅС‹Р№ authService.Login РёРЅРєСЂРµРјРµРЅС‚РёС‚
	// СЃС‡С‘С‚С‡РёРє РЅРµСѓРґР°С‡ Рё Р±Р»РѕРєРёСЂСѓРµС‚ Р°РєРєР°СѓРЅС‚ РЅР° 30 РјРёРЅ РїСЂРё РЅРµРІРµСЂРЅРѕРј РїР°СЂРѕР»Рµ.
	baseData := gin.H{
		"Title":         "РћС‚РєР»СЋС‡РёС‚СЊ 2FA",
		"User":          user.ToPublic(),
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"csrf":          csrf.GetToken(c),
	}

	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.Password)) != nil {
		data := baseData
		data["Error"] = render.Tr(c, "handler.wrong_password")
		render.Page(c, http.StatusOK, "user-2fa-disable.html", data)
		return
	}

	// РўСЂРµР±СѓРµРј С‚РµРєСѓС‰РёР№ TOTP-РєРѕРґ (S-L1): РѕС‚РєР»СЋС‡РµРЅРёРµ 2FA РїРѕ РѕРґРЅРѕРјСѓ РїР°СЂРѕР»СЋ
	// РїРѕР·РІРѕР»СЏР»Рѕ Р±С‹ Р°С‚Р°РєСѓСЋС‰РµРјСѓ СЃ СѓРєСЂР°РґРµРЅРЅРѕР№ СЃРµСЃСЃРёРµР№+РїР°СЂРѕР»РµРј СЃРЅСЏС‚СЊ Р·Р°С‰РёС‚Сѓ.
	if user.TwoFactorEnabled {
		valid, codeErr := h.twoFactorSvc.VerifyCode(user.TwoFactorSecret, strings.TrimSpace(input.Code))
		if codeErr != nil || !valid {
			data := baseData
			data["Error"] = render.Tr(c, "handler.invalid_code")
			render.Page(c, http.StatusOK, "user-2fa-disable.html", data)
			return
		}
	}

	// РћС‚РєР»СЋС‡Р°РµРј 2FA
	h.twoFactorSvc.Disable2FA(user)

	// РЎРѕС…СЂР°РЅСЏРµРј
	if err := h.userRepo.Update(c.Request.Context(), user.ID, map[string]any{
		"two_factor_enabled":      false,
		"two_factor_secret":       "",
		"two_factor_backup_codes": "",
	}); err != nil {
		data := baseData
		data["Error"] = render.LocalizeError(c, err.Error())
		render.Page(c, http.StatusOK, "user-2fa-disable.html", data)
		return
	}

	render.SetFlash(c, "success", i18n.T("twofa.success_disabled"))
	clear2FASessionFlag(c)
	c.Redirect(http.StatusFound, "/profile")
}
