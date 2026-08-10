// internal/domain/calendar/handler.go
package calendar

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/domain/game"
	apperrors "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const defaultEventDuration = 2 * time.Hour

// CalendarDataRequest используется для валидации query-параметров.
type CalendarDataRequest struct {
	Year  int `form:"year" binding:"omitempty,min=2000,max=2100"`
	Month int `form:"month" binding:"omitempty,min=1,max=12"`
}

type CalendarHandler struct {
	gameRepo game.GameRepository
	baseURL  string

	// Кэш данных месяца (5 мин): календарь — публичная страница, часто
	// опрашивается; данные меняются редко (при публикации/удалении игры).
	cacheMu sync.Mutex
	cache   map[string]calendarCacheEntry
}

type calendarCacheEntry struct {
	data    []byte
	expires time.Time
}

const calendarCacheTTL = 5 * time.Minute

func NewCalendarHandler(gameRepo game.GameRepository) *CalendarHandler {
	return &CalendarHandler{gameRepo: gameRepo, cache: make(map[string]calendarCacheEntry)}
}

// WithBaseURL устанавливает канонический base URL (защита от host-header injection).
func (h *CalendarHandler) WithBaseURL(baseURL string) *CalendarHandler {
	h.baseURL = strings.TrimRight(baseURL, "/")
	return h
}

// CalendarPage отображает HTML-страницу календаря.
// @Summary Страница календаря
// @Description Возвращает HTML-страницу с календарём игр, где отображаются опубликованные игры по месяцам
// @Tags calendar
// @Produce html
// @Success 200 {string} html "Страница календаря"
// @Router /calendar [get]
func (h *CalendarHandler) CalendarPage(c *gin.Context) {
	render.Page(c, http.StatusOK, "calendar-page.html", gin.H{
		"Title": "Календарь игр",
	})
}

// CalendarData возвращает события календаря в JSON-формате.
// @Summary Данные календаря
// @Description Возвращает список игр за указанный месяц в формате JSON для отображения в календаре
// @Tags calendar
// @Produce json
// @Param year query int false "Год" default(текущий)
// @Param month query int false "Месяц (1-12)" default(текущий)
// @Success 200 {object} map[string]interface{} "События календаря (ключ — дата, значение — массив игр)"
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Router /api/v1/calendar [get]
func (h *CalendarHandler) CalendarData(c *gin.Context) {
	var req CalendarDataRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		log.Warn().Err(err).Msg("CalendarData: invalid query parameters, using defaults")
		now := time.Now()
		req.Year = now.Year()
		req.Month = int(now.Month())
	}

	if req.Year == 0 {
		req.Year = time.Now().Year()
	}
	if req.Month == 0 {
		req.Month = int(time.Now().Month())
	}

	if req.Month < 1 || req.Month > 12 {
		now := time.Now()
		req.Year = now.Year()
		req.Month = int(now.Month())
	}

	startOfMonth := time.Date(req.Year, time.Month(req.Month), 1, 0, 0, 0, 0, time.UTC)
	endOfMonth := time.Date(req.Year, time.Month(req.Month)+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)

	// TZ-1 (pass 33): календарь показывает даты/время в локальной таймзоне
	// пользователя (tz_offset cookie) — иначе UTC+3 видит время на 3ч раньше,
	// а игры около полуночи попадают в неправильную ячейку дня. Offset также
	// входит в ключ кэша (разные пользователи видят разные события).
	tzOffset := render.TZOffsetFromCookie(c)

	cacheKey := fmt.Sprintf("%d-%d-%d", req.Year, req.Month, tzOffset)
	h.cacheMu.Lock()
	if e, ok := h.cache[cacheKey]; ok && time.Now().Before(e.expires) {
		h.cacheMu.Unlock()
		c.Data(200, "application/json; charset=utf-8", e.data)
		return
	}
	h.cacheMu.Unlock()

	ctx := c.Request.Context()
	games, err := h.gameRepo.ListByDateRange(ctx, startOfMonth, endOfMonth)
	if err != nil {
		log.Error().Err(err).Int("year", req.Year).Int("month", req.Month).Msg("CalendarData: failed to list games")
		appErr := apperrors.Wrap(err, "CalendarHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	events := make(map[string][]gin.H)
	for _, g := range games {
		if g.IsDraft || g.Visibility != "public" {
			continue
		}
		if g.StartsAt == nil {
			continue
		}
		// TZ-1 (pass 33): локальное время пользователя для даты-ячейки и времени.
		localStart := g.StartsAt.Add(time.Duration(tzOffset) * time.Minute)
		dateStr := localStart.Format("2006-01-02")
		events[dateStr] = append(events[dateStr], gin.H{
			"id":   g.ID,
			"name": g.Name,
			"time": localStart.Format("15:04"),
		})
	}

	body, err := json.Marshal(gin.H{
		"year":   req.Year,
		"month":  req.Month,
		"events": events,
	})
	if err != nil {
		appErr := apperrors.Wrap(err, "CalendarHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{"error": appErr.Message, "code": appErr.Code})
		return
	}
	h.cacheMu.Lock()
	h.cache[cacheKey] = calendarCacheEntry{data: body, expires: time.Now().Add(calendarCacheTTL)}
	// F-1 (pass 34): evict просроченные записи — иначе map растёт с каждым
	// (year, month, tzOffset) навсегда (медленная утечка памяти).
	const calendarCacheMax = 512
	if len(h.cache) > calendarCacheMax {
		now := time.Now()
		for k, e := range h.cache {
			if now.After(e.expires) {
				delete(h.cache, k)
			}
		}
	}
	h.cacheMu.Unlock()

	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

// CalendarICal экспортирует предстоящие игры в формате iCalendar (.ics).
// @Summary Экспорт календаря в iCal
// @Description Возвращает .ics файл с предстоящими играми для импорта в внешние календари (Google Calendar, Apple Calendar и др.)
// @Tags calendar
// @Produce text/calendar
// @Success 200 {string} string "iCalendar файл"
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Router /calendar/export.ics [get]
func (h *CalendarHandler) CalendarICal(c *gin.Context) {
	// P-6 (pass 39): собранный .ics кэшируем на 5 мин — Google/Apple опрашивают
	// endpoint регулярно, а полногодовой запрос с Preload дорогой.
	const icsCacheKey = "ics"
	h.cacheMu.Lock()
	if e, ok := h.cache[icsCacheKey]; ok && time.Now().Before(e.expires) {
		ics := e.data
		h.cacheMu.Unlock()
		h.writeICS(c, ics)
		return
	}
	h.cacheMu.Unlock()

	now := time.Now()
	startRange := now
	endRange := now.AddDate(1, 0, 0) // 1 год вперёд

	ctx := c.Request.Context()
	games, err := h.gameRepo.ListByDateRange(ctx, startRange, endRange)
	if err != nil {
		log.Error().Err(err).Msg("CalendarICal: failed to list games")
		appErr := apperrors.Wrap(err, "CalendarHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	var sb strings.Builder
	sb.WriteString("BEGIN:VCALENDAR\r\n")
	sb.WriteString("VERSION:2.0\r\n")
	sb.WriteString("PRODID:-//Gengine//Gengine-0//RU\r\n")
	sb.WriteString("CALSCALE:GREGORIAN\r\n")
	sb.WriteString("METHOD:PUBLISH\r\n")

	for _, g := range games {
		if g.IsDraft || g.Visibility != "public" {
			continue
		}
		if g.StartsAt == nil {
			continue
		}
		start := g.StartsAt.UTC()
		// Длительность по умолчанию — 2 часа
		end := start.Add(defaultEventDuration)

		sb.WriteString("BEGIN:VEVENT\r\n")
		fmt.Fprintf(&sb, "UID:%d-gengine@gengine-0\r\n", g.ID)
		fmt.Fprintf(&sb, "DTSTAMP:%s\r\n", now.UTC().Format("20060102T150405Z"))
		fmt.Fprintf(&sb, "DTSTART:%s\r\n", start.Format("20060102T150405Z"))
		fmt.Fprintf(&sb, "DTEND:%s\r\n", end.Format("20060102T150405Z"))
		fmt.Fprintf(&sb, "SUMMARY:%s\r\n", EscapeICalText(g.Name))
		if g.Description != "" {
			fmt.Fprintf(&sb, "DESCRIPTION:%s\r\n", EscapeICalText(g.Description))
		}
		baseURL := h.baseURL
		if baseURL == "" {
			// Fallback на Host из запроса, но только если это валидный host
			// (host-header injection не должен попасть в iCal-ссылку).
			baseURL = "https://" + SanitizeHost(c.Request.Host)
		}
		fmt.Fprintf(&sb, "URL:%s/games/%d\r\n", baseURL, g.ID)
		sb.WriteString("END:VEVENT\r\n")
	}

	sb.WriteString("END:VCALENDAR\r\n")

	// Сохраняем в кэш.
	h.cacheMu.Lock()
	h.cache[icsCacheKey] = calendarCacheEntry{data: []byte(sb.String()), expires: time.Now().Add(calendarCacheTTL)}
	h.cacheMu.Unlock()

	h.writeICS(c, []byte(sb.String()))
}

// writeICS отдаёт собранный .ics (общий для кэша и не-кэш-пути).
func (h *CalendarHandler) writeICS(c *gin.Context, ics []byte) {
	c.Header("Content-Type", "text/calendar; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="gengine-0-calendar.ics"`)
	c.Data(http.StatusOK, "text/calendar; charset=utf-8", ics)
}

// EscapeICalText экранирует спецсимволы для формата iCalendar.
// Экспортирована для тестируемости (MED-13, pass 29).
func EscapeICalText(text string) string {
	// Сначала backslash (иначе экранированные далее символы станут двойными).
	text = strings.ReplaceAll(text, `\`, `\\`)
	text = strings.ReplaceAll(text, ";", `\;`)
	text = strings.ReplaceAll(text, ",", `\,`)
	text = strings.ReplaceAll(text, "\n", `\n`)
	return text
}

// SanitizeHost разрешает только символы валидного hostname (латиница, цифры, точка, дефис, двоеточие для порта).
// Экспортирована для тестируемости (MED-13, pass 29).
func SanitizeHost(host string) string {
	var sb strings.Builder
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == ':' || r == '_' {
			sb.WriteRune(r)
			continue
		}
		sb.WriteRune('x')
	}
	if sb.Len() == 0 {
		return "localhost"
	}
	return sb.String()
}
