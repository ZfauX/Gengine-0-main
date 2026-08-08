// internal/domain/export/routes.go
package export

import (
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes регистрирует маршруты для экспорта и импорта данных игры.
func RegisterRoutes(
	router *gin.RouterGroup,
	exportService *ExportService,
	store storage.FileStorage,
	gameService *game.GameService,
	coAuthorSvc *game.CoAuthorService,
	authService *user.AuthService,
) error {
	exportHandler := NewExportHandler(exportService, gameService, store)

	authRequired := middleware.AuthRequired(authService)
	gameManager := middleware.GameManager(coAuthorSvc)

	protected := router.Group("/games/:id")
	protected.Use(authRequired)

	csvGroup := protected.Group("")
	csvGroup.Use(gameManager)
	{
		csvGroup.GET("/export", exportHandler.ExportGameCSV)

		csvGroup.GET("/export-results", exportHandler.ExportResultsCSV)

		// E1: endpoint существовал в handler, но маршрут не был зарегистрирован.
		csvGroup.GET("/teams/:team_id/export-results", exportHandler.ExportTeamResultsCSV)
	}

	pdfGroup := protected.Group("")
	pdfGroup.Use(gameManager)
	{
		pdfGroup.GET("/export-pdf", exportHandler.ExportGamePDF)

		pdfGroup.GET("/export-statistics-pdf", exportHandler.ExportStatisticsPDF)
	}

	importGroup := protected.Group("")
	importGroup.Use(gameManager)
	{
		importGroup.GET("/import", exportHandler.ImportGameForm)

		importGroup.POST("/import", exportHandler.ImportGame)
	}

	// =========================================================================
	// ЭКСПОРТ В EXCEL
	// =========================================================================
	excelGroup := protected.Group("")
	excelGroup.Use(gameManager)
	{
		excelGroup.GET("/export-excel", exportHandler.ExportGameExcel)

		excelGroup.GET("/export-results-excel", exportHandler.ExportResultsExcel)
	}

	return nil
}
