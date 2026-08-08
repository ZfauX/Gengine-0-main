// internal/domain/calendar/routes.go
package calendar

import (
	"time"

	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты календаря.
func RegisterRoutes(router *gin.RouterGroup, calendarHandler *CalendarHandler) {
	router.GET("/calendar", calendarHandler.CalendarPage)

	// #9: публичный API-эндпоинт — dedicated per-IP лимитер.
	router.GET("/api/v1/calendar", middleware.LoginRateLimit(time.Minute, 30), calendarHandler.CalendarData)

	router.GET("/calendar/export.ics", calendarHandler.CalendarICal)
}
