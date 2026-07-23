// internal/domain/game/gameplay_handler.go
package game

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/storage"
	"gengine-0/internal/pkg/validation"
	ws "gengine-0/internal/pkg/websocket"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

const (
	codeSubmitMaxBodySize = 1 * 1024 * 1024
	codeMaxLength         = 10000
	answerFileMaxSize     = 10 * 1024 * 1024
)

// ---------- GameplayHandler ----------

type GameplayHandler struct {
	gameService    GameServiceInterface
	gamePlaySvc    GamePlayServiceInterface
	progressSvc    *LevelProgressService
	monitorService *MonitorService
	hub            *ws.RoomHub
	storage        storage.FileStorage
}

func NewGameplayHandler(
	gameService GameServiceInterface,
	gamePlaySvc GamePlayServiceInterface,
	_ *AttemptService,
	progressSvc *LevelProgressService,
	monitorSvc *MonitorService,
	hub *ws.RoomHub,
	store storage.FileStorage,
) *GameplayHandler {
	return &GameplayHandler{
		gameService:    gameService,
		gamePlaySvc:    gamePlaySvc,
		progressSvc:    progressSvc,
		monitorService: monitorSvc,
		hub:            hub,
		storage:        store,
	}
}

// ShowGame отображает страницу прохождения уровня для команды.
// ShowGame отображает страницу прохождения уровня.
// @Summary Страница прохождения уровня
// @Tags gameplay
// @Produce html
// @Param passing_id path int true "ID прохождения"
// @Success 200 {string} html "Страница прохождения"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Failure 404 {object} map[string]interface{} "Прохождение не найдено"
// @Router /game/{passing_id} [get]
// @Security JWT
func (h *GameplayHandler) ShowGame(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	userID := c.GetUint("userID")

	// Получаем все данные через сервис
	data, err := h.gamePlaySvc.GetGameplayData(c.Request.Context(), uint(passingID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gameID, _ := strconv.Atoi(c.Query("game_id"))
			render.Page(c, http.StatusOK, "gameplay-finished.html", gin.H{
				"PassingID": passingID,
				"GameID":    gameID,
				"csrf":      csrf.GetToken(c),
			})
			return
		}
		log.Error().Err(err).Int("passing_id", passingID).Msg("ShowGame: failed to get gameplay data")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	if !h.isTeamMember(c.Request.Context(), data.Passing.TeamID, userID) {
		render.RenderError(c, http.StatusForbidden, "Вы не являетесь участником этой команды")
		return
	}

	hideAnswers := data.Settings.HideAnswersUntilFinished && data.Passing.Status != StatusFinished

	flashError := render.GetFlash(c, "gameplay_error")
	flashHint := render.GetFlash(c, "gameplay_hint")

	render.Page(c, http.StatusOK, "gameplay-show.html", gin.H{
		"PassingID":        passingID,
		"Level":            data.Level,
		"Attempts":         data.Attempts,
		"TimeLimitSeconds": data.TimeLimitSec,
		"HideAnswers":      hideAnswers,
		"VotingActive":     data.VotingActive,
		"TeamID":           data.Passing.TeamID,
		"GameID":           data.Passing.GameID,
		"Error":            flashError,
		"Hint":             flashHint,
		"csrf":             csrf.GetToken(c),
	})
}

// renderGameplayError рендерит страницу ошибки с полными данными уровня.
func (h *GameplayHandler) renderGameplayError(c *gin.Context, passingID uint, err_msg string) {
	render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
		"PassingID": passingID,
		"Error":     err_msg,
		"csrf":      csrf.GetToken(c),
	})
}

// SubmitCode обрабатывает ввод текстового кода.
// SubmitCode отправляет код ответа на уровень.
// @Summary Отправка кода
// @Tags gameplay
// @Accept x-www-form-urlencoded
// @Produce html
// @Param passing_id path int true "ID прохождения"
// @Param code formData string true "Код ответа"
// @Success 302 {string} string "Перенаправление на страницу прохождения"
// @Failure 400 {object} map[string]interface{} "Неверный код"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Failure 429 {object} map[string]interface{} "Слишком много попыток"
// @Router /game/{passing_id}/submit [post]
// @Security JWT
func (h *GameplayHandler) SubmitCode(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	userID := c.GetUint("userID")

	if !h.isUserInPassing(c.Request.Context(), uint(passingID), userID) {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	if limitErr := limitRequestBody(c, codeSubmitMaxBodySize); limitErr != nil {
		h.renderGameplayError(c, uint(passingID), limitErr.Error())
		return
	}

	var input SubmitCodeInput
	if bindErr := c.ShouldBind(&input); bindErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("code", bindErr)
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"PassingID": passingID,
			"Error":     errs.Error(),
			"Errors":    errs,
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	code := strings.TrimSpace(input.Code)
	if validateErr := validation.ValidateString("Код", code, 1, codeMaxLength); validateErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("code", validateErr)
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"PassingID": passingID,
			"Error":     errs.Error(),
			"Errors":    errs,
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	// Пытаемся отправить код
	attempt, submitErr := h.gamePlaySvc.SubmitCode(c.Request.Context(), uint(passingID), userID, code)
	if submitErr != nil {
		// Если ошибка говорит о том, что нет активного уровня — игра завершена
		if errors.Is(submitErr, gorm.ErrRecordNotFound) || errors.Is(submitErr, ErrNoActiveLevel) {
			// Перенаправляем на страницу завершения игры
			c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id")+"/finished")
			return
		}
		h.renderGameplayError(c, uint(passingID), submitErr.Error())
		return
	}

	if attempt.Attempt.Success {
		c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id"))
	} else {
		render.SetFlash(c, "gameplay_error", "Неверный код")
		c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id"))
	}
}

// UseHint использует подсказку для текущего уровня.
// UseHint запрашивает подсказку для уровня.
// @Summary Использование подсказки
// @Tags gameplay
// @Param passing_id path int true "ID прохождения"
// @Success 302 {string} string "Перенаправление на страницу прохождения"
// @Failure 400 {object} map[string]interface{} "Ошибка"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /game/{passing_id}/hint [post]
// @Security JWT
func (h *GameplayHandler) UseHint(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	hintText, hintErr := h.gamePlaySvc.UseHint(c.Request.Context(), uint(passingID), c.GetUint("userID"))
	if hintErr != nil {
		h.renderGameplayError(c, uint(passingID), hintErr.Error())
		return
	}
	if hintText != "" {
		render.SetFlash(c, "gameplay_hint", hintText)
	}
	c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id"))
}

// SubmitFile обрабатывает файловый ответ.
// SubmitFile загружает файл ответа.
// @Summary Загрузка файла ответа
// @Tags gameplay
// @Accept multipart/form-data
// @Param passing_id path int true "ID прохождения"
// @Param file formData file true "Файл ответа"
// @Success 302 {string} string "Перенаправление на страницу прохождения"
// @Failure 400 {object} map[string]interface{} "Ошибка"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /game/{passing_id}/file [post]
// @Security JWT
func (h *GameplayHandler) SubmitFile(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	userID := c.GetUint("userID")

	if !h.isUserInPassing(c.Request.Context(), uint(passingID), userID) {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	if limitErr := limitRequestBody(c, answerFileMaxSize); limitErr != nil {
		h.renderGameplayError(c, uint(passingID), limitErr.Error())
		return
	}

	file, header, formErr := c.Request.FormFile("answer_file")
	if formErr != nil {
		h.renderGameplayError(c, uint(passingID), "Файл не выбран")
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > answerFileMaxSize {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": "Размер файла не должен превышать 10 МБ",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Content-Type из заголовка может быть подделан — итоговая проверка в storage.Save
	allowedTypes := validation.AllowedUploadTypes

	webPath, saveErr := h.storage.Save("uploads/answers", file, header.Filename, userID, answerFileMaxSize, allowedTypes)
	if saveErr != nil {
		log.Error().Err(saveErr).Str("filename", filepath.Base(header.Filename)).Msg("SubmitFile: failed to save file")
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": "Ошибка сохранения файла",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	_, serviceErr := h.gamePlaySvc.SubmitFile(c.Request.Context(), uint(passingID), userID, webPath)
	if serviceErr != nil {
		log.Error().Err(serviceErr).Uint("passing", uint(passingID)).Msg("SubmitFile: service error")
		if delErr := h.storage.Delete(webPath); delErr != nil {
			log.Error().Err(delErr).Str("path", webPath).Msg("SubmitFile: failed to delete uploaded file")
		}
		render.Page(c, http.StatusInternalServerError, "gameplay-show.html", gin.H{
			"Error": "Не удалось сохранить попытку",
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id"))
}

// AcceptAnswer принимает ответ (чёрный ящик).
// AcceptAnswer подтверждает ответ (только для чёрного ящика).
// @Summary Подтверждение ответа (только для чёрного ящика)
// @Tags gameplay
// @Param passing_id path int true "ID прохождения"
// @Success 302 {string} string "Перенаправление на страницу прохождения"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /game/{passing_id}/accept [post]
// @Security JWT
func (h *GameplayHandler) AcceptAnswer(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	if acceptErr := h.gamePlaySvc.AcceptBlackboxAnswer(c.Request.Context(), uint(passingID), c.GetUint("userID")); acceptErr != nil {
		render.RenderError(c, http.StatusForbidden, acceptErr.Error())
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Query("game_id")+"/monitor")
}

// ---------- Тестовое прохождение ----------

// StartTesting инициирует тестовое прохождение.
// StartTesting запускает тестовое прохождение.
// @Summary Запуск тестового прохождения
// @Tags games
// @Produce html
// @Param id path int true "ID игры"
// @Success 302 {string} string "Перенаправление на страницу тестового прохождения"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /games/{id}/testing/start [get]
// @Security JWT
func (h *GameplayHandler) StartTesting(c *gin.Context) {
	gameID, parseErr := strconv.Atoi(c.Param("id"))
	if parseErr != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	passing, startErr := h.gamePlaySvc.StartTesting(c.Request.Context(), uint(gameID), userID)
	if startErr != nil {
		render.RenderError(c, http.StatusForbidden, startErr.Error())
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+strconv.Itoa(int(passing.ID)))
}

// ShowTestGame отображает страницу тестового прохождения.
// ShowTestGame отображает страницу тестового прохождения.
// @Summary Страница тестового прохождения
// @Tags testing
// @Produce html
// @Param passing_id path int true "ID прохождения"
// @Success 200 {string} html "Страница тестового прохождения"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Failure 404 {object} map[string]interface{} "Прохождение не найдено"
// @Router /testing/{passing_id} [get]
// @Security JWT
func (h *GameplayHandler) ShowTestGame(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	userID := c.GetUint("userID")

	progress, progressErr := h.progressSvc.GetCurrentProgress(c.Request.Context(), uint(passingID))
	if progressErr != nil {
		if errors.Is(progressErr, gorm.ErrRecordNotFound) || errors.Is(progressErr, ErrNoActiveLevel) {
			render.Page(c, http.StatusOK, "gameplay-test-finished.html", gin.H{
				"PassingID": passingID,
				"GameID":    c.Query("game_id"),
				"csrf":      csrf.GetToken(c),
			})
			return
		}
		log.Error().Err(progressErr).Int("passing_id", passingID).Msg("ShowTestGame: failed to get current progress")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	// Проверяем права через gameService
	passing, passingErr := h.gamePlaySvc.GetPassingWithGame(c.Request.Context(), uint(passingID))
	if passingErr != nil {
		if errors.Is(passingErr, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(passingErr).Int("passing_id", passingID).Msg("ShowTestGame: failed to get passing")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	g, gameErr := h.gameService.GetByID(c.Request.Context(), passing.GameID, userID)
	if gameErr != nil {
		log.Error().Err(gameErr).Uint("game_id", passing.GameID).Msg("ShowTestGame: failed to get game")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	ok, permErr := h.gameService.IsUserManager(c.Request.Context(), g.ID, userID)
	if permErr != nil || !ok {
		render.RenderError(c, http.StatusForbidden, "Доступ запрещён")
		return
	}

	render.Page(c, http.StatusOK, "gameplay-test.html", gin.H{
		"PassingID": passingID,
		"GameID":    passing.GameID,
		"Level":     progress.Level,
		"csrf":      csrf.GetToken(c),
	})
}

// SubmitTestCode обрабатывает ввод кода в тестовом режиме.
// SubmitTestCode отправляет код ответа в тестовом режиме.
// @Summary Отправка кода в тестовом режиме
// @Tags testing
// @Param passing_id path int true "ID прохождения"
// @Param code formData string true "Код ответа"
// @Success 302 {string} string "Перенаправление на страницу тестового прохождения"
// @Failure 400 {object} map[string]interface{} "Неверный код"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Failure 429 {object} map[string]interface{} "Слишком много попыток"
// @Router /testing/{passing_id}/submit [post]
// @Security JWT
func (h *GameplayHandler) SubmitTestCode(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}

	userID := c.GetUint("userID")

	// Проверяем права: пользователь должен быть автором или соавтором игры
	passing, passingErr := h.gamePlaySvc.GetPassingWithGame(c.Request.Context(), uint(passingID))
	if passingErr != nil {
		if errors.Is(passingErr, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(passingErr).Int("passing_id", passingID).Msg("SubmitTestCode: failed to get passing")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	g, gameErr := h.gameService.GetByID(c.Request.Context(), passing.GameID, userID)
	if gameErr != nil {
		log.Error().Err(gameErr).Uint("game_id", passing.GameID).Msg("SubmitTestCode: failed to get game")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	ok, permErr := h.gameService.IsUserManager(c.Request.Context(), g.ID, userID)
	if permErr != nil || !ok {
		render.RenderError(c, http.StatusForbidden, "Доступ запрещён")
		return
	}

	if limitErr := limitRequestBody(c, codeSubmitMaxBodySize); limitErr != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-test.html", gin.H{
			"Error": limitErr.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	var input SubmitTestCodeInput
	if bindErr := c.ShouldBind(&input); bindErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("code", bindErr)
		render.Page(c, http.StatusBadRequest, "gameplay-test.html", gin.H{
			"PassingID": passingID,
			"Error":     errs.Error(),
			"Errors":    errs,
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	code := strings.TrimSpace(input.Code)
	if validateErr := validation.ValidateString("Код", code, 1, codeMaxLength); validateErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("code", validateErr)
		render.Page(c, http.StatusBadRequest, "gameplay-test.html", gin.H{
			"PassingID": passingID,
			"Error":     errs.Error(),
			"Errors":    errs,
			"csrf":      csrf.GetToken(c),
		})
		return
	}

	if _, submitErr := h.gamePlaySvc.SubmitTestCode(c.Request.Context(), uint(passingID), c.GetUint("userID"), code); submitErr != nil {
		log.Error().Err(submitErr).Int("passing_id", passingID).Msg("SubmitTestCode: service error")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+c.Param("passing_id"))
}

// SkipTestLevel пропускает уровень в тестовом режиме.
// SkipTestLevel пропускает уровень в тестовом режиме.
// @Summary Пропуск уровня в тестовом режиме
// @Tags testing
// @Param passing_id path int true "ID прохождения"
// @Success 302 {string} string "Перенаправление на страницу тестового прохождения"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} "Доступ запрещён"
// @Router /testing/{passing_id}/skip [post]
// @Security JWT
func (h *GameplayHandler) SkipTestLevel(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	if skipErr := h.gamePlaySvc.SkipLevelTest(c.Request.Context(), uint(passingID), c.GetUint("userID")); skipErr != nil {
		render.RenderError(c, http.StatusForbidden, skipErr.Error())
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+c.Param("passing_id"))
}

// ---------- Вспомогательные методы ----------

func (h *GameplayHandler) isTeamMember(ctx context.Context, teamID uint, userID uint) bool {
	ok, _ := h.gamePlaySvc.IsTeamMember(ctx, teamID, userID)
	return ok
}

func (h *GameplayHandler) isUserInPassing(ctx context.Context, passingID uint, userID uint) bool {
	passing, err := h.gamePlaySvc.GetPassingWithGame(ctx, passingID)
	if err != nil {
		return false
	}
	if passing.Status != StatusStarted && passing.Status != StatusTesting {
		return false
	}
	ok, _ := h.gamePlaySvc.IsTeamMember(ctx, passing.TeamID, userID)
	return ok
}
