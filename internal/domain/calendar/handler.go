// internal/domain/calendar/handler.go
package calendar

import (
	"net/http"
	"time"

	"gengine-0/internal/domain/game"
	apperrors "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

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
func (h *CalendarHandler) CalendarPage(c *gin.Context) {
	render.Page(c, http.StatusOK, "calendar-page.html", gin.H{})
}

// CalendarData возвращает события календаря в JSON-формате.
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
		appErr := apperrors.NewInternalError(err)
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
