// internal/domain/user/oauth_service.go
package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"gengine-0/internal/config"

	"github.com/lib/pq"
	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/yandex"
	"gorm.io/gorm"
)

// ---------- OAuthService ----------

func httpClientWithTimeout(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
	}
}

// extraString безопасно приводит oauth2.Extra-значение к строке (H4, pass 30).
// VK отдаёт user_id числом (float64), а Yandex — строкой; тихая потеря
// приводила к пустому externalID и невозможности связать аккаунт.
func extraString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case json.Number:
		return val.String()
	default:
		return ""
	}
}

type OAuthService struct {
	userRepo     UserRepository
	extLoginRepo ExternalLoginRepository
	cfg          *config.Config
	configs      map[string]*oauth2.Config
	httpClient   *http.Client
}

func NewOAuthService(
	userRepo UserRepository,
	extLoginRepo ExternalLoginRepository,
	cfg *config.Config,
) *OAuthService {
	httpClient := httpClientWithTimeout(oauthHTTPTimeout)

	configs := map[string]*oauth2.Config{
		"yandex": {
			ClientID:     cfg.OAuth.Yandex.ClientID,
			ClientSecret: cfg.OAuth.Yandex.ClientSecret,
			RedirectURL:  cfg.Server.BaseURL + "/auth/oauth/yandex/callback",
			Scopes:       []string{"login:email", "login:info"},
			Endpoint:     yandex.Endpoint,
		},
		"vk": {
			ClientID:     cfg.OAuth.VK.ClientID,
			ClientSecret: cfg.OAuth.VK.ClientSecret,
			RedirectURL:  cfg.Server.BaseURL + "/auth/oauth/vk/callback",
			Scopes:       []string{"email"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://oauth.vk.com/authorize",
				TokenURL: "https://oauth.vk.com/access_token",
			},
		},
	}

	return &OAuthService{
		userRepo:     userRepo,
		extLoginRepo: extLoginRepo,
		cfg:          cfg,
		configs:      configs,
		httpClient:   httpClient,
	}
}

func (s *OAuthService) GetAuthURL(provider string) (authURL string, state string, err error) {
	cfg, ok := s.configs[provider]
	if !ok {
		return "", "", stderrors.New("неподдерживаемый провайдер")
	}
	stateBytes := make([]byte, oauthStateBytes)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", "", fmt.Errorf("не удалось сгенерировать state: %w", err)
	}
	state = hex.EncodeToString(stateBytes)
	authURL = cfg.AuthCodeURL(state, oauth2.AccessTypeOffline)
	return authURL, state, nil
}

type yandexUserInfo struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	IsVerified bool   `json:"is_verified"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
}

type vkUserInfo struct {
	Response []struct {
		ID        int    `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	} `json:"response"`
}

func (s *OAuthService) ctxWithHTTPClient(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, s.httpClient)
}

func (s *OAuthService) Authenticate(ctx context.Context, provider, code, state string) (*User, error) {
	if state == "" {
		return nil, stderrors.New("неверный state-параметр")
	}
	cfg, ok := s.configs[provider]
	if !ok {
		return nil, stderrors.New("неподдерживаемый провайдер")
	}

	ctxWithClient := s.ctxWithHTTPClient(ctx)

	token, err := cfg.Exchange(ctxWithClient, code)
	if err != nil {
		return nil, fmt.Errorf("обмен кода на токен: %w", err)
	}

	client := cfg.Client(ctxWithClient, token)

	var emailStr, name, externalID string
	var emailVerified bool
	switch provider {
	case "yandex":
		req, reqErr := http.NewRequestWithContext(ctxWithClient, "GET", "https://login.yandex.ru/info?format=json", nil)
		if reqErr != nil {
			return nil, fmt.Errorf("создание запроса к Yandex API: %w", reqErr)
		}
		resp, respErr := client.Do(req)
		if respErr != nil {
			return nil, fmt.Errorf("запрос к Yandex API: %w", respErr)
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				log.Warn().Err(closeErr).Msg("OAuth Yandex: failed to close response body")
			}
		}()
		var yInfo yandexUserInfo
		if decodeErr := json.NewDecoder(resp.Body).Decode(&yInfo); decodeErr != nil {
			return nil, fmt.Errorf("декодирование ответа Yandex: %w", decodeErr)
		}
		emailStr = yInfo.Email
		externalID = yInfo.ID
		emailVerified = yInfo.IsVerified
		if emailStr == "" {
			return nil, stderrors.New("не удалось получить email от Yandex")
		}
		if !emailVerified {
			return nil, stderrors.New("email от Yandex не подтверждён")
		}
		name = yInfo.FirstName
		if name == "" {
			name = yInfo.LastName
		}
	case "vk":
		// VK возвращает email в токене, получаем имя через users.get
		emailStr, _ = token.Extra("email").(string)
		if emailStr == "" {
			return nil, stderrors.New("не удалось получить email от VK")
		}
		externalID = extraString(token.Extra("user_id"))

		userReq, reqErr := http.NewRequestWithContext(ctxWithClient, "GET",
			"https://api.vk.com/method/users.get?v=5.131&user_ids="+externalID, nil)
		if reqErr != nil {
			log.Warn().Err(reqErr).Str("external_id", externalID).Msg("VK: failed to create user request")
			name = emailStr
		} else {
			userResp, userErr := client.Do(userReq)
			if userErr == nil {
				defer func() { _ = userResp.Body.Close() }()
				var vkInfo vkUserInfo
				if decodeErr := json.NewDecoder(userResp.Body).Decode(&vkInfo); decodeErr == nil && len(vkInfo.Response) > 0 {
					name = vkInfo.Response[0].FirstName + " " + vkInfo.Response[0].LastName
				}
			}
		}
	default:
		return nil, stderrors.New("неподдерживаемый провайдер для получения информации")
	}
	if name == "" {
		name = emailStr
	}
	user, getUserErr := s.userRepo.GetByEmail(ctx, emailStr)
	if stderrors.Is(getUserErr, gorm.ErrRecordNotFound) {
		user = &User{
			Email:         emailStr,
			Name:          name,
			EmailVerified: emailVerified,
			Password:      "",
		}
		if createErr := s.userRepo.Create(ctx, user); createErr != nil {
			// #7: два параллельных OAuth-колбэка на новый email — один ловит
			// unique-violation; перечитываем созданного конкурента.
			var pgErr *pq.Error
			if stderrors.As(createErr, &pgErr) && pgErr.Code == "23505" {
				existing, rErr := s.userRepo.GetByEmail(ctx, emailStr)
				if rErr == nil {
					user = existing
				} else {
					return nil, fmt.Errorf("создание пользователя: %w", createErr)
				}
			} else {
				return nil, fmt.Errorf("создание пользователя: %w", createErr)
			}
		}
	} else if getUserErr != nil {
		return nil, fmt.Errorf("поиск пользователя: %w", getUserErr)
	} else {
		// S-L2: VK не подтверждает email. Привязка существующего аккаунта по
		// неверифицированному email могла бы захватить чужую учётку — отказываем.
		// Пользователь войдёт по паролю (или по passkey) и привяжет VK сам.
		if provider == "vk" && !emailVerified {
			return nil, stderrors.New("вход через VK невозможен: email не подтверждён. Войдите по паролю")
		}
		if user.Name != name {
			if updateErr := s.userRepo.Update(ctx, user.ID, map[string]any{"name": name}); updateErr != nil {
				log.Warn().Err(updateErr).Uint("user_id", user.ID).Msg("не удалось обновить имя пользователя")
			}
		}
		if !user.EmailVerified && emailVerified {
			if updateErr := s.userRepo.Update(ctx, user.ID, map[string]any{"email_verified": true}); updateErr != nil {
				log.Warn().Err(updateErr).Uint("user_id", user.ID).Msg("не удалось установить email_verified")
			}
		}
	}
	extLogin := &ExternalLogin{
		UserID:     user.ID,
		Provider:   provider,
		ExternalID: externalID,
	}
	if findErr := s.extLoginRepo.FindOrCreate(ctx, extLogin); findErr != nil {
		log.Warn().Err(findErr).Uint("user_id", user.ID).Str("provider", provider).Msg("FindOrCreate external login: failed, continuing")
	}
	return user, nil
}
