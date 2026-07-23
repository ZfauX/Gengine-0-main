// internal/domain/notification/routes.go
package notification

import (
	"net/http"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// RegisterRoutes СЂРµРіРёСЃС‚СЂРёСЂСѓРµС‚ API-РјР°СЂС€СЂСѓС‚С‹ РґР»СЏ СѓРІРµРґРѕРјР»РµРЅРёР№.
func RegisterRoutes(r *gin.RouterGroup, db *gorm.DB, authService *user.AuthService, hub *ws.RoomHub, sseMgr *game.SSEManager) {
	service := NewNotificationService(db, hub).WithHub(hub).WithSSEManager(sseMgr)
	settingsHandler := NewSettingsHandler(service)

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
	}
}
