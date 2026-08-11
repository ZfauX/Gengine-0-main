// internal/domain/export/handler.go
package export

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/storage"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const importMaxFileSize = 10 * 1024 * 1024

// ExportHandler управляет экспортом и импортом данных игры.
type ExportHandler struct {
	exportService *ExportService
	gameService   *game.GameService
	coAuthorSvc   *game.CoAuthorService // DEEP-REVIEW PASS-2 (#3): гранулярные права
	storage       storage.FileStorage
}

// NewExportHandler создаёт новый экземпляр ExportHandler.
func NewExportHandler(
	exportService *ExportService,
	gameService *game.GameService,
	coAuthorSvc *game.CoAuthorService,
	storage storage.FileStorage,
) *ExportHandler {
	return &ExportHandler{
		exportService: exportService,
		gameService:   gameService,
		coAuthorSvc:   coAuthorSvc,
		storage:       storage,
	}
}

func (h *ExportHandler) checkGameAccess(c *gin.Context, gameID uint) bool {
	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": render.Tr(c, "handler.unauthorized")})
		return false
	}
	ok, err := h.gameService.IsUserManager(c.Request.Context(), gameID, userID)
	if err != nil || !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
		return false
	}
	return true
}

// requireModerate проверяет право модерации игры (DEEP-REVIEW PASS-2 #3):
// экспорт данных ВСЕХ команд (результаты, статистика) доступен автору/модератору,
// а не observer (read-only) — тот видит только свою команду через ExportTeamResultsCSV.
func (h *ExportHandler) requireModerate(c *gin.Context, gameID uint) bool {
	userID := c.GetUint("userID")
	ok, err := h.coAuthorSvc.HasPermission(c.Request.Context(), gameID, userID, game.RoleModerator)
	if err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("requireModerate: permission check error")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return false
	}
	if !ok {
		render.RenderErrorPage(c, http.StatusForbidden)
		return false
	}
	return true
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
// @Description Выгружает уровни, вопросы и ответы игры в CSV-формат для редактирования или резервного копирования
// @Tags export
// @Produce text/csv
// @Param id path int true "ID игры"
// @Success 200 {file} file "CSV-файл с данными игры"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Router /games/{id}/export [get]
// @Security JWT
func (h *ExportHandler) ExportGameCSV(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": render.LocalizeError(c, err.Error())})
		return
	}

	if !h.checkGameAccess(c, gameID) {
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportGameToCSV(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ExportGameCSV: failed to export game to CSV")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=game_"+strconv.Itoa(int(gameID))+".csv")
	c.Data(http.StatusOK, "text/csv", buf.Bytes())
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
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": render.LocalizeError(c, err.Error())})
		return
	}

	if !h.checkGameAccess(c, gameID) {
		return
	}

	render.Page(c, http.StatusOK, "export_import-import.html", gin.H{
		"Title":  "Импорт",
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
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": render.LocalizeError(c, err.Error())})
		return
	}

	if !h.checkGameAccess(c, gameID) {
		return
	}
	// DEEP-REVIEW PASS-2 (#3): CSV-импорт перезаписывает уровни/вопросы/ответы —
	// observer (read-only) не должен иметь права импорта. Требуется RoleContentEditor.
	userID := c.GetUint("userID")
	canEdit, permErr := h.coAuthorSvc.HasPermission(c.Request.Context(), gameID, userID, game.RoleContentEditor)
	if permErr != nil {
		log.Error().Err(permErr).Uint("game_id", gameID).Msg("ImportGame: permission check error")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	if !canEdit {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	// E2: ограничиваем тело запроса ДО multipart-разбора — иначе при
	// отсутствующем/завышенном Content-Length FormFile загрузит весь файл
	// в память/диск до проверки header.Size.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, importMaxFileSize+1024)
	file, header, err := c.Request.FormFile("csvfile")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			render.Page(c, http.StatusRequestEntityTooLarge, "export_import-import.html", gin.H{
				"Title":  "Импорт",
				"GameID": gameID,
				"Error":  "Размер файла не должен превышать 10 МБ",
				"csrf":   csrf.GetToken(c),
			})
			return
		}
		render.Page(c, http.StatusBadRequest, "export_import-import.html", gin.H{
			"Title":  "Импорт",
			"GameID": gameID,
			"Error":  "Файл не выбран",
			"csrf":   csrf.GetToken(c),
		})
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Debug().Err(err).Msg("export: file close")
		}
	}()

	if header.Size > importMaxFileSize {
		render.Page(c, http.StatusBadRequest, "export_import-import.html", gin.H{
			"Title":  "Импорт",
			"GameID": gameID,
			"Error":  "Размер файла не должен превышать 10 МБ",
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	var isCSV bool
	if strings.HasSuffix(header.Filename, ".csv") {
		isCSV = true
	}
	contentType := header.Header.Get("Content-Type")
	if !isCSV && contentType != "text/csv" && contentType != "application/csv" {
		render.Page(c, http.StatusBadRequest, "export_import-import.html", gin.H{
			"Title":  "Импорт",
			"GameID": gameID,
			"Error":  "Допустимы только CSV-файлы",
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	if err := h.exportService.ImportGameFromCSV(c.Request.Context(), gameID, file); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ImportGame: failed to import game from CSV")
		render.Page(c, http.StatusInternalServerError, "export_import-import.html", gin.H{
			"Title":  "Импорт",
			"GameID": gameID,
			"Error":  "Ошибка импорта",
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
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Router /games/{id}/export-results [get]
// @Security JWT
func (h *ExportHandler) ExportResultsCSV(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": render.LocalizeError(c, err.Error())})
		return
	}

	if !h.checkGameAccess(c, gameID) {
		return
	}
	// DEEP-REVIEW PASS-2 (#3): данные всех команд — только модератор/автор.
	if !h.requireModerate(c, gameID) {
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportResultsToCSV(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ExportResultsCSV: failed to export results to CSV")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=results_"+strconv.Itoa(int(gameID))+".csv")
	c.Data(http.StatusOK, "text/csv", buf.Bytes())
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
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Router /games/{id}/export-pdf [get]
// @Security JWT
func (h *ExportHandler) ExportGamePDF(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": render.LocalizeError(c, err.Error())})
		return
	}

	if !h.checkGameAccess(c, gameID) {
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportGameToPDF(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ExportGamePDF: failed to generate PDF")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", "attachment; filename=game_"+strconv.Itoa(int(gameID))+".pdf")
	c.Data(http.StatusOK, "application/pdf", buf.Bytes())
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
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Router /games/{id}/export-statistics-pdf [get]
// @Security JWT
func (h *ExportHandler) ExportStatisticsPDF(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": render.LocalizeError(c, err.Error())})
		return
	}

	if !h.checkGameAccess(c, gameID) {
		return
	}
	// DEEP-REVIEW PASS-2 (#3): статистика всех команд — только модератор/автор.
	if !h.requireModerate(c, gameID) {
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportStatisticsToPDF(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ExportStatisticsPDF: failed to generate statistics PDF")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", "attachment; filename=stats_"+strconv.Itoa(int(gameID))+".pdf")
	c.Data(http.StatusOK, "application/pdf", buf.Bytes())
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
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Router /games/{id}/export-excel [get]
// @Security JWT
func (h *ExportHandler) ExportGameExcel(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": render.LocalizeError(c, err.Error())})
		return
	}

	if !h.checkGameAccess(c, gameID) {
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportGameToExcel(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ExportGameExcel: failed to generate Excel")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=game_"+strconv.Itoa(int(gameID))+".xlsx")
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
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
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Router /games/{id}/export-results-excel [get]
// @Security JWT
func (h *ExportHandler) ExportResultsExcel(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": render.LocalizeError(c, err.Error())})
		return
	}

	if !h.checkGameAccess(c, gameID) {
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportResultsToExcel(c.Request.Context(), gameID, &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ExportResultsExcel: failed to generate Excel")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=results_"+strconv.Itoa(int(gameID))+".xlsx")
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
}

// ExportTeamResultsCSV отдаёт CSV-файл с результатами команды капитана.
// @Summary Экспорт результатов команды в CSV
// @Description Выгружает результаты конкретной команды в CSV-формат (доступно капитану или автору игры)
// @Tags export
// @Produce text/csv
// @Param id path int true "ID игры"
// @Param team_id path int true "ID команды"
// @Success 200 {file} file "CSV-файл с результатами команды"
// @Failure 400 {object} map[string]interface{} "Неверный ID"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Недостаточно прав"
// @Failure 500 {object} map[string]interface{} "handler.internal_error"
// @Router /games/{id}/teams/{team_id}/export-results [get]
// @Security JWT
func (h *ExportHandler) ExportTeamResultsCSV(c *gin.Context) {
	gameID, err := parseGameID(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": render.LocalizeError(c, err.Error())})
		return
	}

	teamIDStr := c.Param("team_id")
	teamID, err := strconv.Atoi(teamIDStr)
	if err != nil || teamID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": render.Tr(c, "handler.invalid_team_id")})
		return
	}

	userID := c.GetUint("userID")
	if userID == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": render.Tr(c, "handler.unauthorized")})
		return
	}

	// UX-1 (pass 36): НЕ требуем IsUserManager здесь — доступ должен быть у
	// капитана команды или автора игры. Раньше checkGameAccess (IsUserManager)
	// вызывался до проверки isCaptain, и капитан без manager-прав не мог
	// экспортировать результаты своей команды.

	// Проверяем, что пользователь — капитан команды или автор игры
	if _, passingErr := h.gameService.GetFinishedPassingForTeam(c.Request.Context(), gameID, uint(teamID)); passingErr != nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": render.Tr(c, "handler.forbidden")})
		return
	}

	// Проверяем права: капитан или автор
	isCaptain, err := h.gameService.IsTeamCaptain(c.Request.Context(), uint(teamID), userID)
	if err != nil {
		log.Error().Err(err).Uint("team_id", uint(teamID)).Msg("ExportTeamResultsCSV: failed to find team")
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}

	isAuthor := false
	g, err := h.gameService.GetGameByIDUnchecked(c.Request.Context(), gameID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("ExportTeamResultsCSV: failed to find game")
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}
	if g.AuthorID == userID {
		isAuthor = true
	}

	// L-1 (pass 37): соавтор (manager) тоже может экспортировать результаты —
	// раньше только капитан или автор (isAuthor по AuthorID).
	isManager := false
	if mgr, mgrErr := h.gameService.IsUserManager(c.Request.Context(), gameID, userID); mgrErr == nil && mgr {
		isManager = true
	}

	if !isCaptain && !isAuthor && !isManager {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Только капитан, автор или соавтор может экспортировать результаты"})
		return
	}

	var buf bytes.Buffer
	if err := h.exportService.ExportTeamResultsToCSV(c.Request.Context(), gameID, uint(teamID), &buf); err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Uint("team_id", uint(teamID)).Msg("ExportTeamResultsCSV: failed to export")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=team_"+strconv.Itoa(teamID)+"_results.csv")
	c.Data(http.StatusOK, "text/csv", buf.Bytes())
}
