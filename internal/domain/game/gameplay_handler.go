// internal/domain/game/gameplay_handler.go
package game

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"gengine-0/internal/domain/team"
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
	attemptService *AttemptService
	progressSvc    *LevelProgressService
	monitorService *MonitorService
	hub            *ws.RoomHub
	storage        storage.FileStorage
	db             *gorm.DB
}

func NewGameplayHandler(
	gameService GameServiceInterface,
	gamePlaySvc GamePlayServiceInterface,
	attemptSvc *AttemptService,
	progressSvc *LevelProgressService,
	monitorSvc *MonitorService,
	hub *ws.RoomHub,
	store storage.FileStorage,
	db *gorm.DB,
) *GameplayHandler {
	return &GameplayHandler{
		gameService:    gameService,
		gamePlaySvc:    gamePlaySvc,
		attemptService: attemptSvc,
		progressSvc:    progressSvc,
		monitorService: monitorSvc,
		hub:            hub,
		storage:        store,
		db:             db,
	}
}

// ShowGame отображает страницу прохождения уровня для команды.
func (h *GameplayHandler) ShowGame(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
		return
	}
	userID := c.GetUint("userID")

	progress, err := GetCurrentProgress(h.db, uint(passingID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", gin.H{"Error": "Нет активного уровня"})
		} else {
			log.Error().Err(err).Int("passing_id", passingID).Msg("ShowGame: failed to get current progress")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}

	var passing GamePassing
	if err := h.db.Preload("Team").First(&passing, passingID).Error; err != nil {
		log.Error().Err(err).Int("passing_id", passingID).Msg("ShowGame: failed to get passing")
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}
	if !h.isTeamMember(passing.TeamID, userID) {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": "Вы не являетесь участником этой команды"})
		return
	}

	var settings GameSetting
	timeLimitSec := 0
	if err := h.db.Where("game_id = ?", passing.GameID).First(&settings).Error; err == nil {
		if settings.PerLevelTimeLimit > 0 {
			elapsed := time.Since(progress.StartedAt)
			limit := time.Duration(settings.PerLevelTimeLimit) * time.Minute
			remaining := limit - elapsed
			if remaining < 0 {
				remaining = 0
			}
			timeLimitSec = int(remaining.Seconds())
		}
	}

	var attempts []Attempt
	if err := h.db.Where("level_progress_id = ?", progress.ID).Order("created_at DESC").Find(&attempts).Error; err != nil {
		log.Error().Err(err).Int("passing_id", passingID).Msg("ShowGame: failed to get attempts")
	}

	hideAnswers := settings.HideAnswersUntilFinished && passing.Status != StatusFinished

	// Используем локальный тип gameBlackboxVotingSession
	votingActive := h.db.Where("game_passing_id = ? AND level_id = ? AND is_open = true", passingID, progress.LevelID).First(&gameBlackboxVotingSession{}).Error == nil

	render.Page(c, http.StatusOK, "gameplay-show.html", gin.H{
		"PassingID":        passingID,
		"Level":            progress.Level,
		"Attempts":         attempts,
		"TimeLimitSeconds": timeLimitSec,
		"HideAnswers":      hideAnswers,
		"VotingActive":     votingActive,
		"TeamID":           passing.TeamID,
		"csrf":             csrf.GetToken(c),
	})
}

// SubmitCode обрабатывает ввод текстового кода.
func (h *GameplayHandler) SubmitCode(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
		return
	}
	userID := c.GetUint("userID")

	if !h.isUserInPassing(uint(passingID), userID) {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
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

	attempt, err := h.gamePlaySvc.SubmitCode(c.Request.Context(), uint(passingID), userID, code)
	if err != nil {
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
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
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
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
		return
	}
	userID := c.GetUint("userID")

	if !h.isUserInPassing(uint(passingID), userID) {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
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
		log.Error().Err(err).Str("filename", header.Filename).Msg("SubmitFile: failed to save file")
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": "Ошибка сохранения файла",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	_, err = h.gamePlaySvc.SubmitFile(c.Request.Context(), uint(passingID), userID, webPath)
	if err != nil {
		log.Error().Err(err).Uint("passing", uint(passingID)).Msg("SubmitFile: service error")
		_ = h.storage.Delete(webPath)
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
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
		return
	}
	if err := h.gamePlaySvc.AcceptBlackboxAnswer(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Query("game_id")+"/monitor")
}

// ---------- Тестовое прохождение ----------

// StartTesting инициирует тестовое прохождение.
func (h *GameplayHandler) StartTesting(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	passing, err := h.gamePlaySvc.StartTesting(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+strconv.Itoa(int(passing.ID)))
}

// ShowTestGame отображает страницу тестового прохождения.
func (h *GameplayHandler) ShowTestGame(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
		return
	}
	progress, err := GetCurrentProgress(h.db, uint(passingID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", gin.H{"Error": "Уровень не найден"})
		} else {
			log.Error().Err(err).Int("passing_id", passingID).Msg("ShowTestGame: failed to get current progress")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}
	render.Page(c, http.StatusOK, "gameplay-test.html", gin.H{
		"PassingID": passingID,
		"Level":     progress.Level,
		"csrf":      csrf.GetToken(c),
	})
}

// SubmitTestCode обрабатывает ввод кода в тестовом режиме.
func (h *GameplayHandler) SubmitTestCode(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
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
		c.HTML(http.StatusInternalServerError, "errors/500.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+c.Param("passing_id"))
}

// SkipTestLevel пропускает уровень в тестовом режиме.
func (h *GameplayHandler) SkipTestLevel(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
		return
	}
	if err := h.gamePlaySvc.SkipLevelTest(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+c.Param("passing_id"))
}

// ---------- Вспомогательные методы ----------

func (h *GameplayHandler) isTeamMember(teamID uint, userID uint) bool {
	var t team.Team
	if err := h.db.First(&t, teamID).Error; err != nil {
		return false
	}
	if t.CaptainID == userID {
		return true
	}
	var count int64
	h.db.Table("team_members").Where("team_id = ? AND user_id = ?", teamID, userID).Count(&count)
	return count > 0
}

func (h *GameplayHandler) isUserInPassing(passingID uint, userID uint) bool {
	var passing GamePassing
	if err := h.db.First(&passing, passingID).Error; err != nil {
		return false
	}
	return h.isTeamMember(passing.TeamID, userID)
}
