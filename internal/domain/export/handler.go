// internal/domain/export/handler.go
package export

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/pkg/render"
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
	db            *gorm.DB
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

// streamExport выполняет общий сценарий выгрузки файла: парсит ID игры,
// вызывает функцию экспорта в буфер и отдаёт результат как вложение.
// filenamePrefix и ext формируют имя файла вида "<prefix>_<id><ext>".
func (h *ExportHandler) streamExport(
	c *gin.Context,
	contentType, filenamePrefix, ext, logMsg string,
	export func(ctx context.Context, gameID uint, w io.Writer) error,
) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var buf bytes.Buffer
	if err := export(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg(logMsg)
		c.HTML(http.StatusInternalServerError, "errors-500.html", nil)
		return
	}

	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", "attachment; filename="+filenamePrefix+"_"+strconv.Itoa(int(gameID))+ext)
	c.Data(http.StatusOK, contentType, buf.Bytes())
}

// ExportGameCSV отдаёт CSV-файл со всеми уровнями, вопросами и ответами игры.
// @Summary Экспорт игры в CSV
// @Description Выгружает уровни, вопросы и ответы игры в CSV-формат для редактирования или резервного копирования
// @Tags export
// @Produce text/csv
// @Param id path int true "ID игры"
// @Success 200 {file} file "CSV-файл с данными игры"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /games/{id}/export [get]
// @Security JWT
func (h *ExportHandler) ExportGameCSV(c *gin.Context) {
	h.streamExport(c, "text/csv", "game", ".csv",
		"ExportGameCSV: failed to export game to CSV",
		h.exportService.ExportGameToCSV)
}

// ImportGameForm отображает форму загрузки CSV-файла для импорта.
// @Summary Форма импорта игры
// @Description Возвращает HTML-страницу с формой для загрузки CSV-файла для импорта уровней
// @Tags export
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Форма импорта"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Router /games/{id}/import [get]
// @Security JWT
func (h *ExportHandler) ImportGameForm(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	render.Page(c, http.StatusOK, "export_import-import.html", gin.H{
		"GameID": gameID,
		"csrf":   csrf.GetToken(c),
	})
}

// ImportGame обрабатывает загруженный CSV и импортирует данные в игру.
// @Summary Импорт игры из CSV
// @Description Загружает CSV-файл и импортирует уровни, вопросы и ответы в игру (добавляет или обновляет уровни по позиции)
// @Tags export
// @Accept multipart/form-data
// @Produce html
// @Param id path int true "ID игры"
// @Param csvfile formData file true "CSV-файл с данными (до 10 МБ)"
// @Success 302 {string} string "Перенаправление на /games/{id}/levels"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
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
		render.Page(c, http.StatusBadRequest, "export_import-import.html", gin.H{
			"GameID": gameID,
			"Error":  "Файл не выбран",
			"csrf":   csrf.GetToken(c),
		})
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > 10*1024*1024 {
		render.Page(c, http.StatusBadRequest, "export_import-import.html", gin.H{
			"GameID": gameID,
			"Error":  "Размер файла не должен превышать 10 МБ",
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	contentType := header.Header.Get("Content-Type")
	if contentType != "text/csv" && contentType != "application/csv" {
		render.Page(c, http.StatusBadRequest, "export_import-import.html", gin.H{
			"GameID": gameID,
			"Error":  "Допустимы только CSV-файлы",
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	if err := h.exportService.ImportGameFromCSV(h.db, gameID, file); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ImportGame: failed to import game from CSV")
		render.Page(c, http.StatusInternalServerError, "export_import-import.html", gin.H{
			"GameID": gameID,
			"Error":  "Ошибка импорта: " + err.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(int(gameID))+"/levels")
}

// ExportResultsCSV отдаёт CSV-файл с итоговой таблицей результатов игры.
// @Summary Экспорт результатов в CSV
// @Description Выгружает итоговую таблицу результатов игры (места, команды, время, попытки) в CSV-формат
// @Tags export
// @Produce text/csv
// @Param id path int true "ID игры"
// @Success 200 {file} file "CSV-файл с результатами"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /games/{id}/export-results [get]
// @Security JWT
func (h *ExportHandler) ExportResultsCSV(c *gin.Context) {
	h.streamExport(c, "text/csv", "results", ".csv",
		"ExportResultsCSV: failed to export results to CSV",
		h.exportService.ExportResultsToCSV)
}

// ExportGamePDF генерирует и отдаёт PDF-файл со всей структурой игры для печати.
// @Summary Экспорт игры в PDF
// @Description Генерирует PDF-файл со всеми уровнями, вопросами и ответами игры для печати
// @Tags export
// @Produce application/pdf
// @Param id path int true "ID игры"
// @Success 200 {file} file "PDF-файл с данными игры"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /games/{id}/export-pdf [get]
// @Security JWT
func (h *ExportHandler) ExportGamePDF(c *gin.Context) {
	h.streamExport(c, "application/pdf", "game", ".pdf",
		"ExportGamePDF: failed to generate PDF",
		h.exportService.ExportGameToPDF)
}

// ExportStatisticsPDF генерирует и отдаёт PDF-отчёт с расширенной статистикой игры.
// @Summary Экспорт статистики в PDF
// @Description Генерирует PDF-отчёт с расширенной статистикой игры (результаты команд по уровням: время, количество попыток)
// @Tags export
// @Produce application/pdf
// @Param id path int true "ID игры"
// @Success 200 {file} file "PDF-файл со статистикой"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /games/{id}/export-statistics-pdf [get]
// @Security JWT
func (h *ExportHandler) ExportStatisticsPDF(c *gin.Context) {
	h.streamExport(c, "application/pdf", "stats", ".pdf",
		"ExportStatisticsPDF: failed to generate statistics PDF",
		h.exportService.ExportStatisticsToPDF)
}

// =============================================================================
// ЭКСПОРТ В EXCEL
// =============================================================================

// ExportGameExcel экспортирует игру в Excel.
// @Summary Экспорт игры в Excel
// @Description Генерирует Excel-файл (.xlsx) со всеми уровнями, вопросами и ответами игры
// @Tags export
// @Produce application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Param id path int true "ID игры"
// @Success 200 {file} file "Excel-файл с данными игры"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /games/{id}/export-excel [get]
// @Security JWT
func (h *ExportHandler) ExportGameExcel(c *gin.Context) {
	h.streamExport(c, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "game", ".xlsx",
		"ExportGameExcel: failed to generate Excel",
		h.exportService.ExportGameToExcel)
}

// ExportResultsExcel экспортирует результаты игры в Excel.
// @Summary Экспорт результатов в Excel
// @Description Генерирует Excel-файл (.xlsx) с итоговой таблицей результатов игры
// @Tags export
// @Produce application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Param id path int true "ID игры"
// @Success 200 {file} file "Excel-файл с результатами"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Failure 500 {object} map[string]interface{} "Внутренняя ошибка"
// @Router /games/{id}/export-results-excel [get]
// @Security JWT
func (h *ExportHandler) ExportResultsExcel(c *gin.Context) {
	h.streamExport(c, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "results", ".xlsx",
		"ExportResultsExcel: failed to generate Excel",
		h.exportService.ExportResultsToExcel)
}
