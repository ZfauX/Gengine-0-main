package user

import (
	"context"
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
	pushRepo PushSubscriptionRepository
	vapidCfg config.VAPIDConfig
}

func NewPushHandler(pushRepo PushSubscriptionRepository, vapidCfg config.VAPIDConfig) *PushHandler {
	return &PushHandler{pushRepo: pushRepo, vapidCfg: vapidCfg}
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

	existing, findErr := h.pushRepo.FindByEndpoint(c.Request.Context(), sub.Endpoint)
	if findErr == nil {
		if existing.UserID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "subscription belongs to another user"})
			return
		}
		existing.Auth = sub.Auth
		existing.P256dh = sub.P256dh
		if err := h.pushRepo.Update(c.Request.Context(), existing); err != nil {
			log.Error().Err(err).Msg("Push: failed to update subscription")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "ошибка обновления подписки"})
			return
		}
	} else if errors.Is(findErr, gorm.ErrRecordNotFound) {
		if err := h.pushRepo.Create(c.Request.Context(), &sub); err != nil {
			log.Error().Err(err).Msg("Push: failed to save subscription")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "ошибка сохранения подписки"})
			return
		}
	} else {
		// Реальная ошибка БД — не маскируем под «создать»
		log.Error().Err(findErr).Msg("Push: failed to look up subscription")
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

	if err := h.pushRepo.DeleteByEndpointAndUser(c.Request.Context(), req.Endpoint, userID); err != nil {
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
// валидный URL и не локальный/приватный адрес (защита от SSRF, включая
// DNS-rebinding — резолвим имя и проверяем все полученные IP).
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
		return !isPrivateIP(ip)
	}
	// DNS-rebinding: имя может резолвиться в приватный адрес на момент отправки.
	addrs, err := net.DefaultResolver.LookupHost(context.Background(), host)
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil || isPrivateIP(ip) {
			return false
		}
	}
	return true
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
