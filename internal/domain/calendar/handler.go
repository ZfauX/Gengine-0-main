// internal/domain/calendar/handler.go
package calendar

import (
	"fmt"
	"net/http"
	"strings"
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
}

func NewCalendarHandler(gameRepo game.GameRepository) *CalendarHandler {
	return &CalendarHandler{gameRepo: gameRepo}
}

// CalendarPage отображает HTML-страницу календаря.
// @Summary Страница календаря
// @Description Возвращает HTML-страницу с календарём игр, где отображаются опубликованные игры по месяцам
// @Tags calendar
// @Produce html
// @Success 200 {string} html "Страница календаря"
// @Router /calendar [get]
func (h *CalendarHandler) CalendarPage(c *gin.Context) {
	render.Page(c, http.StatusOK, "calendar-page.html", gin.H{})
}

// CalendarData возвращает события календаря в JSON-формате.
// @Summary Данные календаря
// @Description Возвращает список игр за указанный месяц в формате JSON для отображения в календаре
// @Tags calendar
// @Produce json
// @Param year query int false "Год" default(текущий)
// @Param month query int false "Месяц (1-12)" default(текущий)
// @Success 200 {object} map[string]interface{} "События календаря (ключ — дата, значение — массив игр)"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
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
		if g.StartsAt == nil {
			continue
		}
		dateStr := g.StartsAt.Format("2006-01-02")
		events[dateStr] = append(events[dateStr], gin.H{
			"id":   g.ID,
			"name": g.Name,
			"time": g.StartsAt.Format("15:04"),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"year":   req.Year,
		"month":  req.Month,
		"events": events,
	})
}

// CalendarICal экспортирует предстоящие игры в формате iCalendar (.ics).
// @Summary Экспорт календаря в iCal
// @Description Возвращает .ics файл с предстоящими играми для импорта в внешние календари (Google Calendar, Apple Calendar и др.)
// @Tags calendar
// @Produce text/calendar
// @Success 200 {string} string "iCalendar файл"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /calendar/export.ics [get]
func (h *CalendarHandler) CalendarICal(c *gin.Context) {
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
	sb.WriteString("PRODID:-//Gengine//Encounter Engine//RU\r\n")
	sb.WriteString("CALSCALE:GREGORIAN\r\n")
	sb.WriteString("METHOD:PUBLISH\r\n")

	for _, g := range games {
		if g.StartsAt == nil {
			continue
		}
		start := g.StartsAt.UTC()
		// Длительность по умолчанию — 2 часа
		end := start.Add(defaultEventDuration)

		sb.WriteString("BEGIN:VEVENT\r\n")
		fmt.Fprintf(&sb, "UID:%d-gengine@encounter\r\n", g.ID)
		fmt.Fprintf(&sb, "DTSTAMP:%s\r\n", now.UTC().Format("20060102T150405Z"))
		fmt.Fprintf(&sb, "DTSTART:%s\r\n", start.Format("20060102T150405Z"))
		fmt.Fprintf(&sb, "DTEND:%s\r\n", end.Format("20060102T150405Z"))
		fmt.Fprintf(&sb, "SUMMARY:%s\r\n", escapeICalText(g.Name))
		if g.Description != "" {
			fmt.Fprintf(&sb, "DESCRIPTION:%s\r\n", escapeICalText(g.Description))
		}
		fmt.Fprintf(&sb, "URL:https://%s/games/%d\r\n", c.Request.Host, g.ID)
		sb.WriteString("END:VEVENT\r\n")
	}

	sb.WriteString("END:VCALENDAR\r\n")

	c.Header("Content-Type", "text/calendar; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="encounter-calendar.ics"`)
	c.String(http.StatusOK, sb.String())
}

// escapeICalText экранирует спецсимволы для формата iCalendar.
func escapeICalText(text string) string {
	text = strings.ReplaceAll(text, `\`, `\`)
	text = strings.ReplaceAll(text, ";", `\;`)
	text = strings.ReplaceAll(text, ",", `\,`)
	text = strings.ReplaceAll(text, "\n", `\n`)
	return text
}
