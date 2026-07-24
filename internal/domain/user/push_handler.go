package user

import (
	"net/http"

	"gengine-0/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type PushHandler struct {
	db       *gorm.DB
	vapidCfg config.VAPIDConfig
}

func NewPushHandler(db *gorm.DB, vapidCfg config.VAPIDConfig) *PushHandler {
	return &PushHandler{db: db, vapidCfg: vapidCfg}
}

func (h *PushHandler) Subscribe(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
		return
	}

	var sub PushSubscription
	if err := c.ShouldBindJSON(&sub); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "неверный формат подписки"})
		return
	}

	sub.UserID = userID

	var existing PushSubscription
	result := h.db.Where("endpoint = ?", sub.Endpoint).First(&existing)
	if result.Error == nil {
		sub.Model = existing.Model
		if err := h.db.Model(&existing).Updates(map[string]any{
			"auth":   sub.Auth,
			"p256dh": sub.P256dh,
		}).Error; err != nil {
			log.Error().Err(err).Msg("Push: failed to update subscription")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "ошибка обновления подписки"})
			return
		}
	} else {
		if err := h.db.Create(&sub).Error; err != nil {
			log.Error().Err(err).Msg("Push: failed to save subscription")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "ошибка сохранения подписки"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "subscribed"})
}

func (h *PushHandler) Unsubscribe(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
		return
	}

	var req struct {
		Endpoint string `json:"endpoint" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "неверный запрос"})
		return
	}

	if err := h.db.Where("endpoint = ? AND user_id = ?", req.Endpoint, userID).Delete(&PushSubscription{}).Error; err != nil {
		log.Error().Err(err).Msg("Push: failed to delete subscription")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "ошибка удаления подписки"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "unsubscribed"})
}

func (h *PushHandler) VapidPublicKey(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"public_key": h.vapidCfg.PublicKey,
	})
}
