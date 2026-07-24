// internal/domain/notification/routes.go
package notification

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		host := r.Host
		if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
			host = forwardedHost
		}
		if strings.HasPrefix(origin, "http://"+host) || strings.HasPrefix(origin, "https://"+host) {
			return true
		}
		return false
	},
}

// NotificationsWS обрабатывает WebSocket-соединение для real-time уведомлений.
func NotificationsWS(hub *ws.RoomHub) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
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

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			defer func() {
				hub.UnregisterClient(client)
				client.Close()
			}()
			ws.HandleWebSocketWithContext(ctx, client)
		}()
	}
}

// RegisterRoutes СЂРµРіРёСЃС‚СЂРёСЂСѓРµС‚ API-РјР°СЂС€СЂСѓС‚С‹ РґР»СЏ СѓРІРµРґРѕРјР»РµРЅРёР№.
func RegisterRoutes(r *gin.RouterGroup, cfg *config.Config, db *gorm.DB, authService *user.AuthService, hub *ws.RoomHub, sseMgr *game.SSEManager) {
	service := NewNotificationService(db, hub).WithHub(hub).WithSSEManager(sseMgr)
	settingsHandler := NewSettingsHandler(service, cfg.VAPID)

	// HTML-РјР°СЂС€СЂСѓС‚С‹ РґР»СЏ СЃС‚СЂР°РЅРёС†С‹ РЅР°СЃС‚СЂРѕРµРє СѓРІРµРґРѕРјР»РµРЅРёР№
	protected := r.Group("/settings")
	protected.Use(middleware.AuthRequired(authService))
	{
		protected.GET("/notifications", settingsHandler.ShowForm)
	}

	// Р¤РѕСЂРјР° СЃРѕС…СЂР°РЅРµРЅРёСЏ РЅР°СЃС‚СЂРѕРµРє (POST)
	protected.POST("/notifications", settingsHandler.Save)

	// API-РјР°СЂС€СЂСѓС‚С‹ РґР»СЏ AJAX-РѕРїРµСЂР°С†РёР№
	api := r.Group("/api/settings")
	api.Use(middleware.AuthRequired(authService))
	{
		api.GET("/notifications", settingsHandler.APIEmailFlags)

		api.POST("/notifications", settingsHandler.APIEmailSave)
	}

	// Р“СЂСѓРїРїР° СЃ РѕР±СЏР·Р°С‚РµР»СЊРЅРѕР№ Р°СѓС‚РµРЅС‚РёС„РёРєР°С†РёРµР№
	apiNotifs := r.Group("/api/notifications")
	apiNotifs.Use(middleware.AuthRequired(authService))
	{
		api.GET("/settings", func(c *gin.Context) {
			userID := c.GetUint("userID")
			if userID == 0 {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "РўСЂРµР±СѓРµС‚СЃСЏ Р°СѓС‚РµРЅС‚РёС„РёРєР°С†РёСЏ",
					"code":  "unauthorized",
				})
				return
			}
			settings, err := service.GetSettings(c.Request.Context(), userID)
			if err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("Failed to get notification settings")
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "Р’РЅСѓС‚СЂРµРЅРЅСЏСЏ РѕС€РёР±РєР°",
					"code":  "internal_error",
				})
				return
			}
			c.JSON(http.StatusOK, settings)
		})

		api.PUT("/settings", func(c *gin.Context) {
			userID := c.GetUint("userID")
			if userID == 0 {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "РўСЂРµР±СѓРµС‚СЃСЏ Р°СѓС‚РµРЅС‚РёС„РёРєР°С†РёСЏ",
					"code":  "unauthorized",
				})
				return
			}

			var settings Settings
			if err := c.ShouldBindJSON(&settings); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "РќРµРІРµСЂРЅС‹Р№ С„РѕСЂРјР°С‚ РґР°РЅРЅС‹С…: " + err.Error(),
					"code":  "bad_request",
				})
				return
			}

			if err := service.SaveSettings(c.Request.Context(), userID, &settings); err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("Failed to save notification settings")
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "Р’РЅСѓС‚СЂРµРЅРЅСЏСЏ РѕС€РёР±РєР°",
					"code":  "internal_error",
				})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"status":  "ok",
				"message": "РќР°СЃС‚СЂРѕР№РєРё СЃРѕС…СЂР°РЅРµРЅС‹",
			})
		})

		apiNotifs.GET("/list", func(c *gin.Context) {
			userID := c.GetUint("userID")
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "10"))

			notifications, total, err := service.GetByUser(c.Request.Context(), userID, page, perPage)
			if err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("Failed to list notifications")
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Р’РЅСѓС‚СЂРµРЅРЅСЏСЏ РѕС€РёР±РєР°"})
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

	// WebSocket для real-time уведомлений
	wsGroup := r.Group("/ws")
	wsGroup.Use(middleware.AuthRequired(authService))
	{
		wsGroup.GET("/notifications", NotificationsWS(hub))
	}

	// Перенаправление /notifications → /settings/notifications
	r.GET("/notifications", middleware.AuthRequired(authService), func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/settings/notifications")
	})
}
