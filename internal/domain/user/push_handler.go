package user

import (
	"errors"
	"net"
	"net/http"
	"net/url"

	"gengine-0/internal/config"
	"gengine-0/internal/pkg/render"

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

// pushSubscriptionDTO — структура подписки из браузера Push API.
// Браузер шлёт { endpoint, expirationTime, keys: { p256dh, auth } }.
type pushSubscriptionDTO struct {
	Endpoint string `json:"endpoint" binding:"required"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// Subscribe подписывает пользователя на push-уведомления.
// @Summary Подписка на push-уведомления
// @Tags push
// @Accept json
// @Produce json
// @Param subscription body object true "Данные подписки"
// @Success 200 {object} map[string]interface{} "Подписка оформлена"
// @Failure 400 {object} map[string]interface{} "Неверный формат"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /api/push/subscribe [post]
// @Security JWT
func (h *PushHandler) Subscribe(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": render.Tr(c, "handler.unauthorized")})
		return
	}

	var req pushSubscriptionDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "неверный формат подписки"})
		return
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "подписка должна содержать endpoint и ключи p256dh/auth"})
		return
	}
	// Защита от SSRF: endpoint должен быть https и не указывать на локальные/приватные адреса.
	if !validPushEndpoint(req.Endpoint) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "недопустимый endpoint push-подписки"})
		return
	}

	sub := PushSubscription{
		UserID:   userID,
		Endpoint: req.Endpoint,
		Auth:     req.Keys.Auth,
		P256dh:   req.Keys.P256dh,
	}

	var existing PushSubscription
	result := h.db.Where("endpoint = ?", sub.Endpoint).First(&existing)
	if result.Error == nil {
		if existing.UserID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "subscription belongs to another user"})
			return
		}
		sub.Model = existing.Model
		if err := h.db.Model(&existing).Updates(map[string]any{
			"auth":   sub.Auth,
			"p256dh": sub.P256dh,
		}).Error; err != nil {
			log.Error().Err(err).Msg("Push: failed to update subscription")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "ошибка обновления подписки"})
			return
		}
	} else if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		if err := h.db.Create(&sub).Error; err != nil {
			log.Error().Err(err).Msg("Push: failed to save subscription")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "ошибка сохранения подписки"})
			return
		}
	} else {
		// Реальная ошибка БД — не маскируем под «создать»
		log.Error().Err(result.Error).Msg("Push: failed to look up subscription")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "ошибка проверки подписки"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "subscribed"})
}

// Unsubscribe отписывает пользователя от push-уведомлений.
// @Summary Отписка от push-уведомлений
// @Tags push
// @Accept json
// @Produce json
// @Param endpoint body object true "Endpoint подписки"
// @Success 200 {object} map[string]interface{} "Подписка удалена"
// @Failure 400 {object} map[string]interface{} "Неверный запрос"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /api/push/unsubscribe [post]
// @Security JWT
func (h *PushHandler) Unsubscribe(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": render.Tr(c, "handler.unauthorized")})
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

// VapidPublicKey возвращает публичный ключ VAPID для push-уведомлений.
// @Summary Публичный ключ VAPID
// @Tags push
// @Produce json
// @Success 200 {object} map[string]interface{} "Публичный ключ"
// @Router /api/push/vapid-public-key [get]
func (h *PushHandler) VapidPublicKey(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"public_key": h.vapidCfg.PublicKey,
	})
}

// validPushEndpoint проверяет endpoint push-подписки: https-схема,
// валидный URL и не локальный/приватный адрес (защита от SSRF).
func validPushEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	if u.Scheme != "https" || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return false
		}
	}
	return true
}
