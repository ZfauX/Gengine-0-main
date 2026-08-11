// internal/domain/notification/routes.go
package notification

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	ws "gengine-0/internal/pkg/websocket"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false // deny empty origins (CSRF via WebSocket)
		}
		// Точное сравнение origin с host (не prefix-match!) — защита от
		// подделки вида https://example.com.evil.com.
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		originHost := u.Host
		// Владельцы origin могут указывать порт; strip его для сравнения.
		if h, _, err := net.SplitHostPort(originHost); err == nil {
			originHost = h
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		// NB: X-Forwarded-Host не доверяем — при прямом доступе атакующий
		// подделывает и Origin, и X-Forwarded-Host, обходя проверку.
		return strings.EqualFold(originHost, host)
	},
}

// NotificationsWS обрабатывает WebSocket-соединение для real-time уведомлений.
func NotificationsWS(hub *ws.RoomHub) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": render.Tr(c, "handler.unauthorized")})
			return
		}

		remoteIP := c.ClientIP()
		if !hub.CanAccept(remoteIP) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "слишком много соединений"})
			return
		}

		conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Error().Err(err).Uint("user_id", userID).Msg("NotificationsWS: upgrade failed")
			return
		}

		roomID := fmt.Sprintf("user:%d", userID)
		client := ws.NewClient(conn, roomID, remoteIP)
		hub.RegisterClient(client)

		log.Debug().Uint("user_id", userID).Str("room", roomID).Msg("NotificationsWS: connected")

		// N1: контекст не привязываем к c.Request.Context() — после возврата
		// handler'а Gin отменяет его, что убило бы WS мгновенно.
		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			defer func() {
				hub.UnregisterClient(client)
				client.Close()
				cancel()
			}()
			ws.HandleWebSocketWithContext(ctx, client)
		}()
	}
}

// RegisterRoutes регистрирует API-маршруты для уведомлений.
func RegisterRoutes(r *gin.RouterGroup, cfg *config.Config, service *NotificationService, authService *user.AuthService, hub *ws.RoomHub) {
	settingsHandler := NewSettingsHandler(service, cfg.VAPID)

	// API для настроек уведомлений (используется AJAX на странице профиля)
	api := r.Group("/api/settings")
	api.Use(middleware.AuthRequired(authService))
	{
		api.GET("/notifications", settingsHandler.APIEmailFlags)

		api.POST("/notifications", settingsHandler.APIEmailSave)
	}

	// Группа с обязательной аутентификацией
	apiNotifs := r.Group("/api/notifications")
	apiNotifs.Use(middleware.AuthRequired(authService))
	{
		apiNotifs.GET("/settings", func(c *gin.Context) {
			userID := c.GetUint("userID")
			if userID == 0 {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "Требуется аутентификация",
					"code":  "unauthorized",
				})
				return
			}
			settings, err := service.GetSettings(c.Request.Context(), userID)
			if err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("Failed to get notification settings")
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": render.Tr(c, "handler.internal_error"),
					"code":  "internal_error",
				})
				return
			}
			c.JSON(http.StatusOK, settings)
		})

		apiNotifs.PUT("/settings", func(c *gin.Context) {
			userID := c.GetUint("userID")
			if userID == 0 {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "Требуется аутентификация",
					"code":  "unauthorized",
				})
				return
			}

			// DEEP-REVIEW PASS-2 (#6): раньше PUT full-replace — клиент, приславший
			// только изменённые флаги, обнулял все остальные каналы. Теперь merge:
			// начинаем с текущих настроек и применяем только переданные *bool-поля.
			var input struct {
				EmailEnabled             *bool `json:"email_enabled"`
				BrowserEnabled           *bool `json:"browser_enabled"`
				PushEnabled              *bool `json:"push_enabled"`
				EmailGameStarted         *bool `json:"email_game_started"`
				EmailLevelCompleted      *bool `json:"email_level_completed"`
				EmailApplicationAccepted *bool `json:"email_application_accepted"`
				EmailApplicationRejected *bool `json:"email_application_rejected"`
				EmailTimeWarning         *bool `json:"email_time_warning"`
				EmailTimeExpired         *bool `json:"email_time_expired"`
			}
			if err := c.ShouldBindJSON(&input); err != nil {
				log.Warn().Err(err).Ctx(c.Request.Context()).Msg("notification settings invalid JSON")
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "Неверный формат данных",
					"code":  "bad_request",
				})
				return
			}

			settings, err := service.GetSettings(c.Request.Context(), userID)
			if err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("Failed to load notification settings")
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": render.Tr(c, "handler.internal_error"),
					"code":  "internal_error",
				})
				return
			}
			if input.EmailEnabled != nil {
				settings.EmailEnabled = *input.EmailEnabled
			}
			if input.BrowserEnabled != nil {
				settings.BrowserEnabled = *input.BrowserEnabled
			}
			if input.PushEnabled != nil {
				settings.PushEnabled = *input.PushEnabled
			}
			if input.EmailGameStarted != nil {
				settings.EmailGameStarted = *input.EmailGameStarted
			}
			if input.EmailLevelCompleted != nil {
				settings.EmailLevelCompleted = *input.EmailLevelCompleted
			}
			if input.EmailApplicationAccepted != nil {
				settings.EmailApplicationAccepted = *input.EmailApplicationAccepted
			}
			if input.EmailApplicationRejected != nil {
				settings.EmailApplicationRejected = *input.EmailApplicationRejected
			}
			if input.EmailTimeWarning != nil {
				settings.EmailTimeWarning = *input.EmailTimeWarning
			}
			if input.EmailTimeExpired != nil {
				settings.EmailTimeExpired = *input.EmailTimeExpired
			}

			if err := service.SaveSettings(c.Request.Context(), userID, settings); err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("Failed to save notification settings")
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": render.Tr(c, "handler.internal_error"),
					"code":  "internal_error",
				})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"status":  "ok",
				"message": "Настройки сохранены",
			})
		})

		apiNotifs.GET("/list", func(c *gin.Context) {
			userID := c.GetUint("userID")
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "10"))
			if page < 1 {
				page = 1
			}
			if perPage < 1 {
				perPage = 10
			} else if perPage > 100 {
				perPage = 100
			}
			// F-4 (pass 48): фильтр «только непрочитанные».
			onlyUnread := c.Query("unread") == "1"

			notifications, total, err := service.GetByUser(c.Request.Context(), userID, page, perPage, onlyUnread)
			if err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("Failed to list notifications")
				c.JSON(http.StatusInternalServerError, gin.H{"error": render.Tr(c, "handler.internal_error")})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"notifications": notifications,
				"total":         total,
				"page":          page,
				"per_page":      perPage,
			})
		})
	}

	// Страница уведомлений
	protectedNotifs := r.Group("/notifications")
	protectedNotifs.Use(middleware.AuthRequired(authService))
	{
		protectedNotifs.GET("/", func(c *gin.Context) {
			userID := c.GetUint("userID")
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			if page < 1 {
				page = 1
			}
			perPage := 50
			// F-4 (pass 48): фильтр «только непрочитанные».
			onlyUnread := c.Query("unread") == "1"
			notifications, total, err := service.GetByUser(c.Request.Context(), userID, page, perPage, onlyUnread)
			if err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("Failed to list notifications")
				render.RenderErrorPage(c, http.StatusInternalServerError)
				return
			}
			totalPages := (int(total) + perPage - 1) / perPage
			if totalPages < 1 {
				totalPages = 1
			}
			render.Page(c, http.StatusOK, "notifications-list.html", gin.H{
				"Title":         "Уведомления",
				"Notifications": notifications,
				"Total":         total,
				"Page":          page,
				"TotalPages":    totalPages,
				"OnlyUnread":    onlyUnread, // F-4 (pass 48): состояние фильтра.
				"CurrentUserID": userID,
				"csrf":          csrf.GetToken(c),
				"Breadcrumbs": []map[string]string{
					{"name": "nav.home", "url": "/"},
					{"name": "nav.notifications"},
				},
			})
		})
	}

	// WebSocket для real-time уведомлений
	wsGroup := r.Group("/ws")
	wsGroup.Use(middleware.AuthRequired(authService))
	{
		wsGroup.GET("/notifications", NotificationsWS(hub))
	}

	// Конец регистрации маршрутов
}
