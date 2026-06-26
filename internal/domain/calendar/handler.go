// internal/domain/calendar/handler.go
package calendar

import (
	"net/http"
	"strconv"
	"time"

	"gengine-0/internal/domain/game"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

type CalendarHandler struct {
	gameRepo game.GameRepository
}

func NewCalendarHandler(gameRepo game.GameRepository) *CalendarHandler {
	return &CalendarHandler{gameRepo: gameRepo}
}

// CalendarPage отображает HTML-страницу календаря.
func (h *CalendarHandler) CalendarPage(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "calendar-page.html",
	})
}

// CalendarData возвращает события календаря в JSON-формате.
func (h *CalendarHandler) CalendarData(c *gin.Context) {
	year, err := strconv.Atoi(c.DefaultQuery("year", "0"))
	if err != nil {
		log.Warn().Err(err).Str("year", c.Query("year")).Msg("CalendarData: invalid year, using current")
		year = 0
	}
	month, err := strconv.Atoi(c.DefaultQuery("month", "0"))
	if err != nil {
		log.Warn().Err(err).Str("month", c.Query("month")).Msg("CalendarData: invalid month, using current")
		month = 0
	}

	if year == 0 || month == 0 {
		now := time.Now()
		year = now.Year()
		month = int(now.Month())
	}

	// Валидация месяца
	if month < 1 || month > 12 {
		log.Warn().Int("month", month).Msg("CalendarData: invalid month, using current")
		now := time.Now()
		year = now.Year()
		month = int(now.Month())
	}

	startOfMonth := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endOfMonth := startOfMonth.AddDate(0, 1, -1)

	ctx := c.Request.Context()
	games, err := h.gameRepo.ListByDateRange(ctx, startOfMonth, endOfMonth)
	if err != nil {
		log.Error().Err(err).Int("year", year).Int("month", month).Msg("CalendarData: failed to list games")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "не удалось загрузить данные календаря"})
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
		"year":   year,
		"month":  month,
		"events": events,
	})
}
