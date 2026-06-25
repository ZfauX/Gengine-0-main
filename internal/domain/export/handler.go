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
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// ExportHandler управляет экспортом и импортом данных игры.
type ExportHandler struct {
	exportService *ExportService
	gameService   *game.GameService
	storage       storage.FileStorage
	db            *gorm.DB // для импорта
}

// NewExportHandler создаёт новый экземпляр ExportHandler.
func NewExportHandler(
	exportService *ExportService,
	gameService *game.GameService,
	storage storage.FileStorage,
	db *gorm.DB,
) *ExportHandler {
	return &ExportHandler{
		exportService: exportService,
		gameService:   gameService,
		storage:       storage,
		db:            db,
	}
}

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
func (h *ExportHandler) ExportGameCSV(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportGameToCSV(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("Ошибка экспорта игры в CSV")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=game_"+strconv.Itoa(int(gameID))+".csv")
	c.Data(http.StatusOK, "text/csv", buf.Bytes())
}

// ImportGameForm отображает форму загрузки CSV-файла для импорта.
// @Summary Форма импорта игры
// @Description Возвращает HTML-страницу с формой для загрузки CSV-файла
// @Tags export
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Форма импорта"
// @Router /games/{id}/import [get]
// @Security JWT
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

	if err := h.exportService.ImportGameFromCSV(h.db, gameID, file); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("Ошибка импорта игры из CSV")
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "export_import-import.html",
			"GameID":       gameID,
			"Error":        "Ошибка импорта: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(int(gameID))+"/levels")
}

// ExportResultsCSV отдаёт CSV-файл с итоговой таблицей результатов игры.
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
func (h *ExportHandler) ExportResultsCSV(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportResultsToCSV(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("Ошибка экспорта результатов в CSV")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=results_"+strconv.Itoa(int(gameID))+".csv")
	c.Data(http.StatusOK, "text/csv", buf.Bytes())
}

// ExportGamePDF генерирует и отдаёт PDF-файл со всей структурой игры для печати.
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
func (h *ExportHandler) ExportGamePDF(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportGameToPDF(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("Ошибка генерации PDF игры")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", "attachment; filename=game_"+strconv.Itoa(int(gameID))+".pdf")
	c.Data(http.StatusOK, "application/pdf", buf.Bytes())
}

// ExportStatisticsPDF генерирует и отдаёт PDF-отчёт с расширенной статистикой игры.
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
func (h *ExportHandler) ExportStatisticsPDF(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportStatisticsToPDF(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("Ошибка генерации PDF статистики")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", "attachment; filename=stats_"+strconv.Itoa(int(gameID))+".pdf")
	c.Data(http.StatusOK, "application/pdf", buf.Bytes())
}
