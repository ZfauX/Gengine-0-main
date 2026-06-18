package calendar

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RegisterRoutes(router *gin.Engine, db *gorm.DB) {
	calendarHandler := NewCalendarHandler(db)
	router.GET("/calendar", calendarHandler.CalendarPage)
	router.GET("/api/v1/calendar", calendarHandler.CalendarData)
}