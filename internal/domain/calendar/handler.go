// internal/domain/calendar/handler.go
package calendar

import (
	"net/http"
	"strconv"
	"time"

	"gengine-0/internal/domain/game"

	"github.com/gin-gonic/gin"
)

type CalendarHandler struct {
	gameRepo game.GameRepository
}

func NewCalendarHandler(gameRepo game.GameRepository) *CalendarHandler {
	return &CalendarHandler{gameRepo: gameRepo}
}

// CalendarPage отображает HTML-страницу календаря.
// @Summary Страница календаря
// @Description Возвращает HTML-страницу с календарём игр
// @Tags calendar
// @Produce html
// @Success 200 {string} html "Страница календаря"
// @Router /calendar [get]
func (h *CalendarHandler) CalendarPage(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "calendar-page.html",
	})
}

// CalendarData возвращает события календаря в JSON-формате.
// @Summary Данные календаря
// @Description Возвращает список игр за указанный месяц в формате JSON
// @Tags calendar
// @Produce json
// @Param year query int false "Год" default(текущий)
// @Param month query int false "Месяц (1-12)" default(текущий)
// @Success 200 {object} map[string]interface{} "События календаря"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /api/v1/calendar [get]
func (h *CalendarHandler) CalendarData(c *gin.Context) {
	year, _ := strconv.Atoi(c.DefaultQuery("year", "0"))
	month, _ := strconv.Atoi(c.DefaultQuery("month", "0"))

	if year == 0 || month == 0 {
		now := time.Now()
		year = now.Year()
		month = int(now.Month())
	}

	startOfMonth := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	// Конец месяца – последняя секунда последнего дня
	endOfMonth := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)

	ctx := c.Request.Context()
	games, err := h.gameRepo.ListByDateRange(ctx, startOfMonth, endOfMonth)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
