// internal/domain/game/gameplay_handler.go
package game

import (
	"context"
	"errors"
	"fmt"
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
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Failure 404 {object} map[string]interface{} render.Tr(c, "handler.passing_not_found")
// @Router /game/{passing_id} [get]
// @Security JWT
func (h *GameplayHandler) ShowGame(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_passing_id"))
		return
	}
	userID := c.GetUint("userID")

	// Получаем все данные через сервис
	data, err := h.gamePlaySvc.GetGameplayData(c.Request.Context(), uint(passingID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrGameNotActive) {
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

	if isMember, memErr := h.isTeamMember(c.Request.Context(), data.Passing.TeamID, userID); memErr != nil {
		log.Error().Err(memErr).Uint("team_id", data.Passing.TeamID).Msg("ShowGame: failed to check team membership")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	} else if !isMember {
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
		"IncludeLeaflet":   true,
		"csrf":             csrf.GetToken(c),
	})
}

// renderGameplayError рендерит страницу ошибки с полными данными уровня.
func (h *GameplayHandler) renderGameplayError(c *gin.Context, passingID uint, errMsg string) {
	// Пытаемся получить все данные геймплея для корректного рендера шаблона
	data, dataErr := h.gamePlaySvc.GetGameplayData(c.Request.Context(), passingID)
	if dataErr == nil {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"PassingID":        passingID,
			"Level":            data.Level,
			"Attempts":         data.Attempts,
			"TimeLimitSeconds": data.TimeLimitSec,
			"HideAnswers":      data.Settings.HideAnswersUntilFinished && data.Passing.Status != StatusFinished,
			"TeamID":           data.Passing.TeamID,
			"GameID":           data.Passing.GameID,
			"Error":            errMsg,
			"csrf":             csrf.GetToken(c),
		})
		return
	}

	// Fallback: пытаемся получить хотя бы GameID
	gameID := uint(0)
	if passing, passErr := h.gamePlaySvc.GetPassingWithGame(c.Request.Context(), passingID); passErr == nil {
		gameID = passing.GameID
	}
	render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
		"PassingID": passingID,
		"GameID":    gameID,
		"Error":     errMsg,
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
// @Failure 400 {object} map[string]interface{} render.Tr(c, "handler.invalid_code")
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Failure 429 {object} map[string]interface{} "Слишком много попыток"
// @Router /game/{passing_id}/submit [post]
// @Security JWT
func (h *GameplayHandler) SubmitCode(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_passing_id"))
		return
	}
	userID := c.GetUint("userID")

	if isMember, memErr := h.isUserInPassing(c.Request.Context(), uint(passingID), userID); memErr != nil {
		log.Error().Err(memErr).Int("passing_id", passingID).Msg("Gameplay: failed to check passing membership")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	} else if !isMember {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	if limitErr := LimitRequestBody(c, codeSubmitMaxBodySize); limitErr != nil {
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
		h.renderGameplayError(c, uint(passingID), render.LocalizeError(c, submitErr.Error()))
		return
	}

	if attempt.Attempt.Success {
		c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id"))
	} else {
		render.SetFlash(c, "gameplay_error", render.Tr(c, "handler.invalid_code"))
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
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /game/{passing_id}/hint [post]
// @Security JWT
func (h *GameplayHandler) UseHint(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_passing_id"))
		return
	}
	hintText, hintErr := h.gamePlaySvc.UseHint(c.Request.Context(), uint(passingID), c.GetUint("userID"))
	if hintErr != nil {
		// Машинно-читаемый код для клиента (UX16): клиент не должен определять
		// тип ошибки по локализованному тексту.
		if errors.Is(hintErr, ErrHintLimitReached) {
			c.Header("X-Error-Code", "hint_limit")
		}
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
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /game/{passing_id}/file [post]
// @Security JWT
func (h *GameplayHandler) SubmitFile(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_passing_id"))
		return
	}
	userID := c.GetUint("userID")

	if isMember, memErr := h.isUserInPassing(c.Request.Context(), uint(passingID), userID); memErr != nil {
		log.Error().Err(memErr).Int("passing_id", passingID).Msg("Gameplay: failed to check passing membership")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	} else if !isMember {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	if limitErr := LimitRequestBody(c, answerFileMaxSize); limitErr != nil {
		h.renderGameplayError(c, uint(passingID), limitErr.Error())
		return
	}

	file, header, formErr := c.Request.FormFile("answer_file")
	if formErr != nil {
		h.renderGameplayError(c, uint(passingID), render.Tr(c, "handler.file_not_selected"))
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Debug().Err(err).Msg("gameplay: file close")
		}
	}()

	if header.Size > answerFileMaxSize {
		h.renderGameplayError(c, uint(passingID), render.Tr(c, "handler.file_too_large"))
		return
	}

	// Content-Type из заголовка может быть подделан — итоговая проверка в storage.Save
	allowedTypes := validation.AllowedUploadTypes

	webPath, saveErr := h.storage.Save("uploads/answers", file, header.Filename, userID, answerFileMaxSize, allowedTypes)
	if saveErr != nil {
		log.Error().Err(saveErr).Str("filename", filepath.Base(header.Filename)).Msg("SubmitFile: failed to save file")
		h.renderGameplayError(c, uint(passingID), render.Tr(c, "handler.file_save_error"))
		return
	}

	_, serviceErr := h.gamePlaySvc.SubmitFile(c.Request.Context(), uint(passingID), userID, webPath)
	if serviceErr != nil {
		log.Error().Err(serviceErr).Uint("passing", uint(passingID)).Msg("SubmitFile: service error")
		if delErr := h.storage.Delete(webPath); delErr != nil {
			log.Error().Err(delErr).Str("path", webPath).Msg("SubmitFile: failed to delete uploaded file")
		}
		h.renderGameplayError(c, uint(passingID), render.Tr(c, "handler.attempt_save_error"))
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
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /game/{passing_id}/accept [post]
// @Security JWT
func (h *GameplayHandler) AcceptAnswer(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_passing_id"))
		return
	}
	userID := c.GetUint("userID")
	if isMember, memErr := h.isUserInPassing(c.Request.Context(), uint(passingID), userID); memErr != nil {
		log.Error().Err(memErr).Int("passing_id", passingID).Msg("Gameplay: failed to check passing membership")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	} else if !isMember {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}
	if acceptErr := h.gamePlaySvc.AcceptBlackboxAnswer(c.Request.Context(), uint(passingID), userID); acceptErr != nil {
		// Различаем бизнес-отказы (403), «не найдено» (404) и технические
		// ошибки (500) вместо маскировки всего как 403 (B4/M5).
		switch {
		case errors.Is(acceptErr, ErrOnlyAuthor), errors.Is(acceptErr, ErrBlackboxOnly):
			render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, acceptErr.Error()))
		case errors.Is(acceptErr, gorm.ErrRecordNotFound):
			render.RenderErrorPage(c, http.StatusNotFound)
		default:
			log.Error().Err(acceptErr).Int("passing_id", passingID).Msg("AcceptAnswer: service error")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	gameID, err := strconv.Atoi(c.Query("game_id"))
	if err != nil || gameID <= 0 {
		render.RenderErrorPage(c, http.StatusBadRequest)
		return
	}
	c.Redirect(http.StatusFound, fmt.Sprintf("/games/%d/monitor", gameID))
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
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/testing/start [get]
// @Security JWT
func (h *GameplayHandler) StartTesting(c *gin.Context) {
	gameID, parseErr := strconv.Atoi(c.Param("id"))
	if parseErr != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")

	passing, startErr := h.gamePlaySvc.StartTesting(c.Request.Context(), uint(gameID), userID)
	if startErr != nil {
		render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, startErr.Error()))
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
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Failure 404 {object} map[string]interface{} render.Tr(c, "handler.passing_not_found")
// @Router /testing/{passing_id} [get]
// @Security JWT
func (h *GameplayHandler) ShowTestGame(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_passing_id"))
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

	// Проверяем права через gameService (passing + game загружены через JOIN)
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
	ok, permErr := h.gameService.IsUserManager(c.Request.Context(), passing.GameID, userID)
	if permErr != nil || !ok {
		render.RenderError(c, http.StatusForbidden, render.Tr(c, "handler.forbidden"))
		return
	}

	render.Page(c, http.StatusOK, "gameplay-test.html", gin.H{
		"PassingID":      passingID,
		"GameID":         passing.GameID,
		"Level":          progress.Level,
		"IncludeLeaflet": true,
		"csrf":           csrf.GetToken(c),
	})
}

// SubmitTestCode обрабатывает ввод кода в тестовом режиме.
// SubmitTestCode отправляет код ответа в тестовом режиме.
// @Summary Отправка кода в тестовом режиме
// @Tags testing
// @Param passing_id path int true "ID прохождения"
// @Param code formData string true "Код ответа"
// @Success 302 {string} string "Перенаправление на страницу тестового прохождения"
// @Failure 400 {object} map[string]interface{} render.Tr(c, "handler.invalid_code")
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Failure 429 {object} map[string]interface{} "Слишком много попыток"
// @Router /testing/{passing_id}/submit [post]
// @Security JWT
func (h *GameplayHandler) SubmitTestCode(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_passing_id"))
		return
	}

	userID := c.GetUint("userID")

	// Проверяем права: пользователь должен быть автором или соавтором игры (passing + game через JOIN)
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
	ok, permErr := h.gameService.IsUserManager(c.Request.Context(), passing.GameID, userID)
	if permErr != nil || !ok {
		render.RenderError(c, http.StatusForbidden, render.Tr(c, "handler.forbidden"))
		return
	}

	if limitErr := LimitRequestBody(c, codeSubmitMaxBodySize); limitErr != nil {
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
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /testing/{passing_id}/skip [post]
// @Security JWT
func (h *GameplayHandler) SkipTestLevel(c *gin.Context) {
	passingID, parseErr := strconv.Atoi(c.Param("passing_id"))
	if parseErr != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_passing_id"))
		return
	}
	if skipErr := h.gamePlaySvc.SkipLevelTest(c.Request.Context(), uint(passingID), c.GetUint("userID")); skipErr != nil {
		render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, skipErr.Error()))
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+c.Param("passing_id"))
}

// ---------- Вспомогательные методы ----------

func (h *GameplayHandler) isTeamMember(ctx context.Context, teamID uint, userID uint) (bool, error) {
	return h.gamePlaySvc.IsTeamMember(ctx, teamID, userID)
}

func (h *GameplayHandler) isUserInPassing(ctx context.Context, passingID uint, userID uint) (bool, error) {
	passing, err := h.gamePlaySvc.GetPassingWithGame(ctx, passingID)
	if err != nil {
		return false, err
	}
	if passing.Status != StatusStarted && passing.Status != StatusTesting {
		return false, nil
	}
	return h.gamePlaySvc.IsTeamMember(ctx, passing.TeamID, userID)
}
