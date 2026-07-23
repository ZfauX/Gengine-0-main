// internal/domain/calendar/routes.go
package calendar

import (
	"gengine-0/internal/domain/game"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes СЂРµРіРёСЃС‚СЂРёСЂСѓРµС‚ РјР°СЂС€СЂСѓС‚С‹ РєР°Р»РµРЅРґР°СЂСЏ.
func RegisterRoutes(router *gin.RouterGroup, gameRepo game.GameRepository) {
	calendarHandler := NewCalendarHandler(gameRepo)

	router.GET("/calendar", calendarHandler.CalendarPage)

	router.GET("/api/v1/calendar", calendarHandler.CalendarData)

	router.GET("/calendar/export.ics", calendarHandler.CalendarICal)
}
