// internal/domain/game/gameplay_handler.go
package game

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/storage"
	"gengine-0/internal/pkg/validation"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
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
			// Нет активного уровня — игра завершена
			render.Page(c, http.StatusOK, "gameplay-finished.html", gin.H{
				"PassingID": passingID,
				"GameID":    c.Query("game_id"),
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

	render.Page(c, http.StatusOK, "gameplay-show.html", gin.H{
		"PassingID":        passingID,
		"Level":            data.Level,
		"Attempts":         data.Attempts,
		"TimeLimitSeconds": data.TimeLimitSec,
		"HideAnswers":      hideAnswers,
		"VotingActive":     data.VotingActive,
		"TeamID":           data.Passing.TeamID,
		"GameID":           data.Passing.GameID,
		"csrf":             csrf.GetToken(c),
	})
}

// SubmitCode обрабатывает ввод текстового кода.
func (h *GameplayHandler) SubmitCode(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	userID := c.GetUint("userID")

	if !h.isUserInPassing(c.Request.Context(), uint(passingID), userID) {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	var input SubmitCodeInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	code := strings.TrimSpace(input.Code)
	if err := validation.ValidateString("Код", code, 1, 10000); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Пытаемся отправить код
	attempt, err := h.gamePlaySvc.SubmitCode(c.Request.Context(), uint(passingID), userID, code)
	if err != nil {
		// Если ошибка говорит о том, что нет активного уровня — игра завершена
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrNoActiveLevel) {
			// Перенаправляем на страницу завершения игры
			c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id")+"/finished")
			return
		}
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if attempt.Success {
		c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id"))
	} else {
		c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id")+"?error=wrong_code")
	}
}

// UseHint использует подсказку для текущего уровня.
func (h *GameplayHandler) UseHint(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	if err := h.gamePlaySvc.UseHint(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id"))
}

// SubmitFile обрабатывает файловый ответ.
func (h *GameplayHandler) SubmitFile(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	userID := c.GetUint("userID")

	if !h.isUserInPassing(c.Request.Context(), uint(passingID), userID) {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	if err := limitRequestBody(c, 10*1024*1024); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	file, header, err := c.Request.FormFile("answer_file")
	if err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": "Файл не выбран",
			"csrf":  csrf.GetToken(c),
		})
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > 10*1024*1024 {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": "Размер файла не должен превышать 10 МБ",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/gif", "application/pdf", "text/plain"}
	contentType := header.Header.Get("Content-Type")
	if !slices.Contains(allowedTypes, contentType) {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": "Недопустимый тип файла",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	webPath, err := h.storage.Save("uploads/answers", file, header.Filename, userID, 10*1024*1024, allowedTypes)
	if err != nil {
		log.Error().Err(err).Str("filename", filepath.Base(header.Filename)).Msg("SubmitFile: failed to save file")
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": "Ошибка сохранения файла",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	_, err = h.gamePlaySvc.SubmitFile(c.Request.Context(), uint(passingID), userID, webPath)
	if err != nil {
		log.Error().Err(err).Uint("passing", uint(passingID)).Msg("SubmitFile: service error")
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
func (h *GameplayHandler) AcceptAnswer(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	if err := h.gamePlaySvc.AcceptBlackboxAnswer(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Query("game_id")+"/monitor")
}

// ---------- Тестовое прохождение ----------

// StartTesting инициирует тестовое прохождение.
func (h *GameplayHandler) StartTesting(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	passing, err := h.gamePlaySvc.StartTesting(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+strconv.Itoa(int(passing.ID)))
}

// ShowTestGame отображает страницу тестового прохождения.
func (h *GameplayHandler) ShowTestGame(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	userID := c.GetUint("userID")

	progress, err := h.progressSvc.GetCurrentProgress(c.Request.Context(), uint(passingID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrNoActiveLevel) {
			render.Page(c, http.StatusOK, "gameplay-test-finished.html", gin.H{
				"PassingID": passingID,
				"GameID":    c.Query("game_id"),
				"csrf":      csrf.GetToken(c),
			})
			return
		}
		log.Error().Err(err).Int("passing_id", passingID).Msg("ShowTestGame: failed to get current progress")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	// Проверяем права через gameService
	passing, err := h.gamePlaySvc.GetPassingWithGame(c.Request.Context(), uint(passingID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Int("passing_id", passingID).Msg("ShowTestGame: failed to get passing")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	g, err := h.gameService.GetByID(c.Request.Context(), passing.GameID, userID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", passing.GameID).Msg("ShowTestGame: failed to get game")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	ok, err := h.gameService.IsUserManager(c.Request.Context(), g.ID, userID)
	if err != nil || !ok {
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
func (h *GameplayHandler) SubmitTestCode(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}

	userID := c.GetUint("userID")

	// Проверяем права: пользователь должен быть автором или соавтором игры
	passing, err := h.gamePlaySvc.GetPassingWithGame(c.Request.Context(), uint(passingID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Int("passing_id", passingID).Msg("SubmitTestCode: failed to get passing")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	g, err := h.gameService.GetByID(c.Request.Context(), passing.GameID, userID)
	if err != nil {
		log.Error().Err(err).Uint("game_id", passing.GameID).Msg("SubmitTestCode: failed to get game")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	ok, err := h.gameService.IsUserManager(c.Request.Context(), g.ID, userID)
	if err != nil || !ok {
		render.RenderError(c, http.StatusForbidden, "Доступ запрещён")
		return
	}

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-test.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	var input SubmitTestCodeInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-test.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	code := strings.TrimSpace(input.Code)
	if err := validation.ValidateString("Код", code, 1, 10000); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-test.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if _, err := h.gamePlaySvc.SubmitTestCode(c.Request.Context(), uint(passingID), c.GetUint("userID"), code); err != nil {
		log.Error().Err(err).Int("passing_id", passingID).Msg("SubmitTestCode: service error")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+c.Param("passing_id"))
}

// SkipTestLevel пропускает уровень в тестовом режиме.
func (h *GameplayHandler) SkipTestLevel(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	if err := h.gamePlaySvc.SkipLevelTest(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
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
	ok, _ := h.gamePlaySvc.IsTeamMember(ctx, passing.TeamID, userID)
	return ok
}
