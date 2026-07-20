// internal/domain/game/settings_handler.go
package game

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// SettingsHandler обрабатывает настройки игр.
type SettingsHandler struct {
	gameService GameServiceInterface
	coAuthorSvc CoAuthorServiceInterface
}

// NewSettingsHandler создаёт новый SettingsHandler.
func NewSettingsHandler(
	gameService GameServiceInterface,
	coAuthorSvc CoAuthorServiceInterface,
) *SettingsHandler {
	return &SettingsHandler{
		gameService: gameService,
		coAuthorSvc: coAuthorSvc,
	}
}

// SettingsPage отображает страницу настроек игры.
func (h *SettingsHandler) SettingsPage(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.SettingsPage: failed to get game")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	var settings *GameSetting
	// Игра уже загружена с Preload("GameSetting") через GetByID
	if g.GameSetting.ID != 0 {
		settings = &g.GameSetting
	} else {
		settings = &GameSetting{
			GameID:                   g.ID,
			AllowHints:               true,
			HintPenaltySeconds:       300,
			MaxHints:                 3,
			PerLevelTimeLimit:        0,
			HideAnswersUntilFinished: false,
			AutoStart:                false,
		}
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "games-settings.html", gin.H{
		"Game":          g,
		"Settings":      settings,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// SaveSettings сохраняет настройки игры.
func (h *SettingsHandler) SaveSettings(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		g, _ := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
		render.Page(c, http.StatusBadRequest, "games-settings.html", gin.H{
			"Game":  g,
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Парсим числовые поля
	hintPenaltySeconds, _ := strconv.Atoi(c.PostForm("hint_penalty_seconds"))
	maxHints, _ := strconv.Atoi(c.PostForm("max_hints"))
	perLevelTimeLimit, _ := strconv.Atoi(c.PostForm("per_level_time_limit"))

	// Парсим чекбоксы: если в POST есть ключ со значением "true", то true, иначе false
	allowHints := c.PostForm("allow_hints") == "true"
	hideAnswersUntilFinished := c.PostForm("hide_answers_until_finished") == "true"
	autoStart := c.PostForm("auto_start") == "true"

	// Валидация
	if hintPenaltySeconds < 0 {
		hintPenaltySeconds = 0
	}
	if maxHints < 0 {
		maxHints = 0
	}
	if perLevelTimeLimit < 0 {
		perLevelTimeLimit = 0
	}
	if perLevelTimeLimit > 3600 {
		g, _ := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
		render.Page(c, http.StatusBadRequest, "games-settings.html", gin.H{
			"Game":  g,
			"Error": "Лимит времени на уровень не может превышать 3600 минут",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	g, err := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}
	isManager, err := h.coAuthorSvc.IsUserManager(c.Request.Context(), g.ID, userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("SettingsHandler.SaveSettings: failed to check manager")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	if !isManager {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	// Поиск и сохранение настроек
	settings, err := h.gameService.SaveSettings(c.Request.Context(), g.ID, GameSetting{
		AllowHints:               allowHints,
		HintPenaltySeconds:       hintPenaltySeconds,
		MaxHints:                 maxHints,
		PerLevelTimeLimit:        perLevelTimeLimit,
		HideAnswersUntilFinished: hideAnswersUntilFinished,
		AutoStart:                autoStart,
	})
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("SettingsHandler.SaveSettings: failed to save settings")
		render.Page(c, http.StatusInternalServerError, "games-settings.html", gin.H{
			"Game":     g,
			"Settings": *settings,
			"Error":    "Ошибка сохранения: " + err.Error(),
			"csrf":     csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/settings")
}
