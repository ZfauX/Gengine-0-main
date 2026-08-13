// internal/domain/payment/routes.go
// G-1..G-3 (pass 45): маршруты платежей.
package payment

import (
	"time"

	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты платежей.
func RegisterRoutes(r *gin.RouterGroup, h *PaymentHandler, authService *user.AuthService) {
	protected := r.Group("/payments")
	protected.Use(middleware.AuthRequired(authService))
	{
		protected.GET("/", h.Index)
		// M4 (PASS-5): rate-limit на создание платежа — аутентифицированный
		// пользователь не должен флудить pending-записями и outbound-вызовами
		// к api.yookassa.ru (ресурсный спам).
		protected.POST("/create", middleware.PaymentRateLimit(1*time.Minute, 5), h.Create)
	}

	// Вебхук ЮKassa — публичный (не требует JWT), проверка по IP внутри сервиса.
	// PASS-8 LOW #3: per-IP лимит — защита от флуда при компрометации прокси
	// (сам вебхук уже защищён IP-allowlist ЮKassa).
	r.POST("/payments/webhook", middleware.IPRateLimit(1*time.Minute, 120), h.Webhook)
}
