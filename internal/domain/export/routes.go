// internal/domain/export/routes.go
package export

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/assets/fonts"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes регистрирует маршруты для экспорта и импорта данных игры.
// @tags export
func RegisterRoutes(
	router *gin.Engine,
	db *gorm.DB,
	store storage.FileStorage,
	cfg *config.Config,
	gameService *game.GameService,
	coAuthorSvc *game.CoAuthorService,
	authService *user.AuthService,
) {
	exportRepo := NewGormExportRepo(db)
	exportService := NewExportService(exportRepo, fonts.DejaVuSans, fonts.DejaVuSansBold)
	exportHandler := NewExportHandler(exportService, gameService, store, db)

	authRequired := middleware.AuthRequired(authService)
	gameManager := middleware.GameManager(coAuthorSvc)

	protected := router.Group("/games/:id")
	protected.Use(authRequired)

	csvGroup := protected.Group("")
	csvGroup.Use(gameManager)
	{
		// @Summary Экспорт игры в CSV
		// @Description Выгружает уровни, вопросы и ответы игры в CSV-формат
		// @Tags export
		// @Produce text/csv
		// @Param id path int true "ID игры"
		// @Success 200 {file} file "CSV-файл"
		// @Failure 400 {object} map[string]interface{} "Неверный ID"
		// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
		// @Router /games/{id}/export [get]
		// @Security JWT
		csvGroup.GET("/export", exportHandler.ExportGameCSV)

		// @Summary Экспорт результатов в CSV
		// @Description Выгружает итоговую таблицу результатов игры в CSV-формат
		// @Tags export
		// @Produce text/csv
		// @Param id path int true "ID игры"
		// @Success 200 {file} file "CSV-файл"
		// @Failure 400 {object} map[string]interface{} "Неверный ID"
		// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
		// @Router /games/{id}/export-results [get]
		// @Security JWT
		csvGroup.GET("/export-results", exportHandler.ExportResultsCSV)
	}

	pdfGroup := protected.Group("")
	pdfGroup.Use(gameManager)
	{
		// @Summary Экспорт игры в PDF
		// @Description Генерирует PDF-файл со всеми уровнями, вопросами и ответами игры
		// @Tags export
		// @Produce application/pdf
		// @Param id path int true "ID игры"
		// @Success 200 {file} file "PDF-файл"
		// @Failure 400 {object} map[string]interface{} "Неверный ID"
		// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
		// @Router /games/{id}/export-pdf [get]
		// @Security JWT
		pdfGroup.GET("/export-pdf", exportHandler.ExportGamePDF)

		// @Summary Экспорт статистики в PDF
		// @Description Генерирует PDF-отчёт с расширенной статистикой игры (результаты команд по уровням)
		// @Tags export
		// @Produce application/pdf
		// @Param id path int true "ID игры"
		// @Success 200 {file} file "PDF-файл"
		// @Failure 400 {object} map[string]interface{} "Неверный ID"
		// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
		// @Router /games/{id}/export-statistics-pdf [get]
		// @Security JWT
		pdfGroup.GET("/export-statistics-pdf", exportHandler.ExportStatisticsPDF)
	}

	importGroup := protected.Group("")
	importGroup.Use(gameManager)
	{
		// @Summary Форма импорта игры
		// @Description Возвращает HTML-страницу с формой для загрузки CSV-файла
		// @Tags export
		// @Produce html
		// @Param id path int true "ID игры"
		// @Success 200 {string} html "Форма импорта"
		// @Router /games/{id}/import [get]
		// @Security JWT
		importGroup.GET("/import", exportHandler.ImportGameForm)

		// @Summary Импорт игры из CSV
		// @Description Загружает CSV-файл и импортирует уровни, вопросы и ответы в игру
		// @Tags export
		// @Accept multipart/form-data
		// @Produce html
		// @Param id path int true "ID игры"
		// @Param csvfile formData file true "CSV-файл"
		// @Success 302 {string} string "Перенаправление на /games/{id}/levels"
		// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
		// @Router /games/{id}/import [post]
		// @Security JWT
		importGroup.POST("/import", exportHandler.ImportGame)
	}
}
