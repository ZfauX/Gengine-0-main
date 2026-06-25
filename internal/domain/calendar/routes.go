// internal/domain/calendar/routes.go
package calendar

import (
	"gengine-0/internal/domain/game"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты календаря.
// @tags calendar
func RegisterRoutes(router *gin.Engine, gameRepo game.GameRepository) {
	calendarHandler := NewCalendarHandler(gameRepo)

	// @Summary Страница календаря
	// @Description Возвращает HTML-страницу с календарём игр
	// @Tags calendar
	// @Produce html
	// @Success 200 {string} html "Страница календаря"
	// @Router /calendar [get]
	router.GET("/calendar", calendarHandler.CalendarPage)

	// @Summary Данные календаря
	// @Description Возвращает список игр за указанный месяц в формате JSON
	// @Tags calendar
	// @Produce json
	// @Param year query int false "Год" default(текущий)
	// @Param month query int false "Месяц (1-12)" default(текущий)
	// @Success 200 {object} map[string]interface{} "События календаря"
	// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
	// @Router /api/v1/calendar [get]
	router.GET("/api/v1/calendar", calendarHandler.CalendarData)
}
