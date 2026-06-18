// internal/domain/export/handler.go
package export

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"github.com/utrack/gin-csrf"
)

// ExportHandler управляет экспортом и импортом данных игры.
type ExportHandler struct {
	exportService *ExportService
	gameService   *game.GameService
	storage       storage.FileStorage
}

// NewExportHandler создаёт новый экземпляр ExportHandler.
func NewExportHandler(
	exportService *ExportService,
	gameService *game.GameService,
	storage storage.FileStorage,
) *ExportHandler {
	return &ExportHandler{
		exportService: exportService,
		gameService:   gameService,
		storage:       storage,
	}
}

// parseGameID извлекает и валидирует ID игры из параметра пути.
func parseGameID(c *gin.Context) (uint, error) {
	idStr := c.Param("id")
	if idStr == "" {
		return 0, errors.New("пустой параметр id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		return 0, errors.New("некорректный id игры")
	}
	return uint(id), nil
}

// ExportGameCSV отдаёт CSV-файл со всеми уровнями, вопросами и ответами игры.
func (h *ExportHandler) ExportGameCSV(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportGameToCSV(gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("Ошибка экспорта игры в CSV")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=game_"+strconv.Itoa(int(gameID))+".csv")
	c.Data(http.StatusOK, "text/csv", buf.Bytes())
}

// ImportGameForm отображает форму загрузки CSV-файла для импорта.
func (h *ExportHandler) ImportGameForm(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "export_import-import.html",
		"GameID":       gameID,
		"csrf":         csrf.GetToken(c),
	})
}

// ImportGame обрабатывает загруженный CSV и импортирует данные в игру.
func (h *ExportHandler) ImportGame(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	file, header, err := c.Request.FormFile("csvfile")
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "export_import-import.html",
			"GameID":       gameID,
			"Error":        "Файл не выбран",
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > 10*1024*1024 {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "export_import-import.html",
			"GameID":       gameID,
			"Error":        "Размер файла не должен превышать 10 МБ",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	contentType := header.Header.Get("Content-Type")
	if contentType != "text/csv" && contentType != "application/csv" {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "export_import-import.html",
			"GameID":       gameID,
			"Error":        "Допустимы только CSV-файлы",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.exportService.ImportGameFromCSV(gameID, file); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("Ошибка импорта игры из CSV")
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "export_import-import.html",
			"GameID":       gameID,
			"Error":        "Ошибка импорта: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/levels")
}

// ExportResultsCSV отдаёт CSV-файл с итоговой таблицей результатов игры.
func (h *ExportHandler) ExportResultsCSV(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportResultsToCSV(gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("Ошибка экспорта результатов в CSV")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=results_"+strconv.Itoa(int(gameID))+".csv")
	c.Data(http.StatusOK, "text/csv", buf.Bytes())
}

// ExportGamePDF генерирует и отдаёт PDF-файл со всей структурой игры для печати.
func (h *ExportHandler) ExportGamePDF(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Права доступа уже проверены middleware GameManager, дополнительная проверка не требуется

	var buf bytes.Buffer
	if err := h.exportService.ExportGameToPDF(gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("Ошибка генерации PDF игры")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", "attachment; filename=game_"+strconv.Itoa(int(gameID))+".pdf")
	c.Data(http.StatusOK, "application/pdf", buf.Bytes())
}

// ExportStatisticsPDF генерирует и отдаёт PDF-отчёт с расширенной статистикой игры.
func (h *ExportHandler) ExportStatisticsPDF(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Права доступа уже проверены middleware GameManager

	var buf bytes.Buffer
	if err := h.exportService.ExportStatisticsToPDF(gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("Ошибка генерации PDF статистики")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", "attachment; filename=stats_"+strconv.Itoa(int(gameID))+".pdf")
	c.Data(http.StatusOK, "application/pdf", buf.Bytes())
}