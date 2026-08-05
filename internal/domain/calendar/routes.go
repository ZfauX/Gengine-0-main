// internal/domain/calendar/routes.go
package calendar

import (
	"gengine-0/internal/domain/game"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты календаря.
func RegisterRoutes(router *gin.RouterGroup, gameRepo game.GameRepository, baseURL string) {
	calendarHandler := NewCalendarHandler(gameRepo).WithBaseURL(baseURL)

	router.GET("/calendar", calendarHandler.CalendarPage)

	router.GET("/api/v1/calendar", calendarHandler.CalendarData)

	router.GET("/calendar/export.ics", calendarHandler.CalendarICal)
}
