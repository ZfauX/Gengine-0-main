// internal/domain/payment/handler.go
// G-1..G-3 (pass 45): HTTP-обработчики платежей ЮKassa.
package payment

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/render"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// PaymentHandler — HTTP-обработчики платежей.
type PaymentHandler struct {
	svc *PaymentService
}

func NewPaymentHandler(svc *PaymentService) *PaymentHandler {
	return &PaymentHandler{svc: svc}
}

// Index отображает страницу платежей пользователя.
// @Summary Платежи пользователя
// @Tags payments
// @Produce html
// @Success 200 {string} html
// @Router /payments [get]
// @Security JWT
func (h *PaymentHandler) Index(c *gin.Context) {
	userID := c.GetUint("userID")
	payments, err := h.svc.ListByUser(c.Request.Context(), userID, 20)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("PaymentHandler.Index: failed to list")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	render.Page(c, http.StatusOK, "payments-index.html", gin.H{
		"Title":           "Оплата",
		"Payments":        payments,
		"PaymentsEnabled": h.svc.Enabled(),
		"CurrentUserID":   userID,
		"csrf":            csrf.GetToken(c),
	})
}

// Create создаёт платёж и редиректит на confirmation_url.
// @Summary Создание платежа
// @Tags payments
// @Accept x-www-form-urlencoded
// @Param amount formData float64 true "Сумма"
// @Param description formData string false "Описание"
// @Success 302 {string} string "Редирект на ЮKassa"
// @Router /payments/create [post]
// PaymentMinRubles / PaymentMaxRubles (DEEP-REVIEW PASS-3 M10): пороги суммы
// платежа. Раньше принималась любая сумма > 0 (до 0.01) — при будущей привязке
// к привилегиям минимальная оплата дала бы привилегию даром.
const (
	PaymentMinRubles = 50.0
	PaymentMaxRubles = 100000.0
)

// Create создаёт платёж и редиректит на confirmation_url.
// @Summary Создание платежа
// @Tags payments
// @Accept x-www-form-urlencoded
// @Param amount formData float64 true "Сумма (рубли)"
// @Param description formData string false "Описание"
// @Success 302 {string} string "Редирект на ЮKassa"
// @Router /payments/create [post]
// @Security JWT
func (h *PaymentHandler) Create(c *gin.Context) {
	userID := c.GetUint("userID")
	// DEEP-REVIEW LOW #30 (pass 46): не игнорируем ошибку ParseFloat —
	// невалидная сумма теперь явно отклоняется.
	// M10 (PASS-3): рубли из формы → копейки (int64) на входе в сервис.
	amount, parseErr := strconv.ParseFloat(c.PostForm("amount"), 64)
	description := c.PostForm("description")
	metadata := c.PostForm("metadata")

	// M3 (PASS-4): NaN/Inf проходят обычные сравнения — явно отклоняем.
	// strconv.ParseFloat("NaN") не возвращает ошибку, а rublesToKopecks(NaN)
	// дал бы math.MinInt64 (отрицательная запись в БД).
	if parseErr != nil || math.IsNaN(amount) || math.IsInf(amount, 0) ||
		amount < PaymentMinRubles || amount > PaymentMaxRubles {
		render.SetFlash(c, "error", fmt.Sprintf("Сумма должна быть от %.0f до %.0f рублей", PaymentMinRubles, PaymentMaxRubles))
		c.Redirect(http.StatusFound, "/payments")
		return
	}
	if !h.svc.Enabled() {
		render.SetFlash(c, "error", "Платежи временно недоступны")
		c.Redirect(http.StatusFound, "/payments")
		return
	}

	_, confirmURL, err := h.svc.CreatePayment(c.Request.Context(), userID, rublesToKopecks(amount), description, metadata)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Float64("amount", amount).Msg("PaymentHandler.Create: failed")
		render.SetFlash(c, "error", "Не удалось создать платёж")
		c.Redirect(http.StatusFound, "/payments")
		return
	}
	if confirmURL == "" {
		render.SetFlash(c, "error", "Платёж не может быть подтверждён")
		c.Redirect(http.StatusFound, "/payments")
		return
	}
	c.Redirect(http.StatusFound, confirmURL)
}

// Webhook принимает уведомления от ЮKassa.
// @Summary Вебхук ЮKassa
// @Tags payments
// @Accept json
// @Success 200 {string} string "OK"
// @Failure 400 {object} map[string]interface{} "Некорректное тело"
// @Failure 403 {object} map[string]interface{} "Не доверенный IP"
// @Router /payments/webhook [post]
func (h *PaymentHandler) Webhook(c *gin.Context) {
	body, _ := io.ReadAll(io.LimitReader(c.Request.Body, 2*1024*1024))
	remoteIP := c.ClientIP()
	// M1 (PASS-3): Authorization (Basic ShopID:WebhookKey) — подпись вебхука.
	authHeader := c.GetHeader("Authorization")
	err := h.svc.HandleWebhook(c.Request.Context(), remoteIP, authHeader, body)
	if err == nil {
		c.Status(http.StatusOK)
		return
	}

	// S-46-3 (pass 46): rejected webhooks больше не «прячем» под always-200.
	//  - ErrWebhookInvalid / ErrWebhookUntrustedIP: 4xx — ретраить бессмысленно
	//    (ЮKassa перестанет долбить неподтверждённый платёж);
	//  - ErrWebhookUnauthorized (M1): 401 — нет/неверная подпись;
	//  - прочие (временные ошибки ЮKassa/БД): 500 — ЮKassa будет ретраить,
	//    а алерт в логе уровня error позволит заметить проблему.
	switch {
	case errors.Is(err, ErrWebhookUnauthorized):
		log.Error().Err(err).Str("ip", remoteIP).Msg("PaymentHandler.Webhook: unauthorized (bad signature)")
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	case errors.Is(err, ErrWebhookInvalid):
		log.Error().Err(err).Str("ip", remoteIP).Msg("PaymentHandler.Webhook: invalid body (no retry)")
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	case errors.Is(err, ErrWebhookUntrustedIP):
		log.Error().Err(err).Str("ip", remoteIP).Msg("PaymentHandler.Webhook: untrusted IP (no retry)")
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	default:
		log.Error().Err(err).Str("ip", remoteIP).Msg("PaymentHandler.Webhook: rejected, YooKassa will retry")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
}
