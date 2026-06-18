// internal/domain/export/routes.go
package export

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/assets/fonts" // встроенные шрифты
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RegisterRoutes(
	router *gin.Engine,
	db *gorm.DB,
	store storage.FileStorage,
	cfg *config.Config,
	gameService *game.GameService,
	coAuthorSvc *game.CoAuthorService,
) {
	// Передаём оба шрифта в сервис экспорта
	exportService := NewExportService(db, fonts.DejaVuSans, fonts.DejaVuSansBold)
	exportHandler := NewExportHandler(exportService, gameService, store)

	authService := user.NewAuthService(db, cfg)
	authRequired := middleware.AuthRequired(authService)
	gameManager := middleware.GameManager(coAuthorSvc)

	protected := router.Group("/games/:id")
	protected.Use(authRequired)

	// CSV-экспорты доступны только автору/соавтору
	csvGroup := protected.Group("")
	csvGroup.Use(gameManager)
	{
		csvGroup.GET("/export", exportHandler.ExportGameCSV)
		csvGroup.GET("/export-results", exportHandler.ExportResultsCSV)
	}

	// PDF‑экспорты доступны только автору/соавтору
	pdfGroup := protected.Group("")
	pdfGroup.Use(gameManager)
	{
		pdfGroup.GET("/export-pdf", exportHandler.ExportGamePDF)
		pdfGroup.GET("/export-statistics-pdf", exportHandler.ExportStatisticsPDF)
	}

	// Импорт доступен только автору/соавтору
	importGroup := protected.Group("")
	importGroup.Use(gameManager)
	{
		importGroup.GET("/import", exportHandler.ImportGameForm)
		importGroup.POST("/import", exportHandler.ImportGame)
	}
}