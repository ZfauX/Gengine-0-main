package calendar

import (
	"net/http"
	"strconv"
	"time"

	"gengine-0/internal/domain/game"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CalendarHandler struct {
	db *gorm.DB
}

func NewCalendarHandler(db *gorm.DB) *CalendarHandler {
	return &CalendarHandler{db: db}
}

func (h *CalendarHandler) CalendarPage(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
        "ContentBlock": "calendar-page.html",  
    })
}

func (h *CalendarHandler) CalendarData(c *gin.Context) {
	year, _ := strconv.Atoi(c.DefaultQuery("year", "0"))
	month, _ := strconv.Atoi(c.DefaultQuery("month", "0"))

	if year == 0 || month == 0 {
		now := time.Now()
		year = now.Year()
		month = int(now.Month())
	}

	startOfMonth := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endOfMonth := startOfMonth.AddDate(0, 1, -1)

	var games []game.Game
	h.db.Preload("Author").
		Where("is_draft = false AND visibility = 'public' AND starts_at BETWEEN ? AND ?", startOfMonth, endOfMonth).
		Order("starts_at ASC").
		Find(&games)

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