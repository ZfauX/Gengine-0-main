// internal/domain/game/settings_handler.go
package game

import (
	"errors"
	"net/http"
	"strconv"

	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
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
// @Summary Настройки игры
// @Tags games
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Страница настроек"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Failure 404 {object} map[string]interface{} render.Tr(c, "handler.game_not_found")
// @Router /games/{id}/settings [get]
// @Security JWT
func (h *SettingsHandler) SettingsPage(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID, middleware.IsAdmin(c))
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
	log.Info().Uint("game_id", g.ID).Uint("setting_id", settings.ID).Bool("ah", settings.AllowHints).Msg("SettingsPage: loaded settings")

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "games-settings.html", gin.H{
		"Title":         render.Tr(c, "game.settings_title"),
		"Game":          g,
		"Settings":      settings,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.games", "url": "/games"},
			{"name": g.Name, "url": "/games/" + c.Param("id")},
			{"name": "nav.settings"},
		},
	})
}

// SaveSettings сохраняет настройки игры.
// @Summary Сохранение настроек
// @Tags games
// @Param id path int true "ID игры"
// @Success 302 {string} string "Перенаправление на /games/{id}/settings"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/settings [post]
// @Security JWT
func (h *SettingsHandler) SaveSettings(c *gin.Context) {
	gameID, parseErr := strconv.Atoi(c.Param("id"))
	if parseErr != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")

	if limitErr := LimitRequestBody(c, 1*1024*1024); limitErr != nil {
		g, _ := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID, middleware.IsAdmin(c))
		render.Page(c, http.StatusBadRequest, "games-settings.html", gin.H{
			"Title": "Настройки игры",
			"Game":  g,
			"Error": limitErr.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Парсим числовые поля
	hintPenaltySeconds, _ := strconv.Atoi(c.PostForm("hint_penalty_seconds"))
	maxHints, _ := strconv.Atoi(c.PostForm("max_hints"))
	perLevelTimeLimit, _ := strconv.Atoi(c.PostForm("per_level_time_limit"))

	allowHints := c.PostForm("allow_hints") == "true"
	hideAnswersUntilFinished := c.PostForm("hide_answers_until_finished") == "true"
	autoStart := c.PostForm("auto_start") == "true"

	log.Info().Int("hps", hintPenaltySeconds).Int("mh", maxHints).Int("pltl", perLevelTimeLimit).Bool("ah", allowHints).Bool("hauf", hideAnswersUntilFinished).Bool("as", autoStart).Msg("SaveSettings: parsed form values")

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
		g, _ := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID, middleware.IsAdmin(c))
		render.Page(c, http.StatusBadRequest, "games-settings.html", gin.H{
			"Title": "Настройки игры",
			"Game":  g,
			"Error": "Лимит времени на уровень не может превышать 3600 минут",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	g, err := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID, middleware.IsAdmin(c))
	if err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}
	if !middleware.IsAdmin(c) {
		isMgr, checkErr := h.coAuthorSvc.IsUserManager(c.Request.Context(), g.ID, userID)
		if checkErr != nil {
			log.Error().Err(checkErr).Int("game_id", gameID).Msg("SettingsHandler.SaveSettings: failed to check manager")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
		if !isMgr {
			render.RenderErrorPage(c, http.StatusForbidden)
			return
		}
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
		// Спасаем nil-deref (C-C2): SaveSettings возвращает (nil, err) при сбое БД.
		if settings == nil {
			settings = &GameSetting{}
		}
		render.Page(c, http.StatusInternalServerError, "games-settings.html", gin.H{
			"Title":    "Настройки игры",
			"Game":     g,
			"Settings": *settings,
			"Error":    render.LocalizeError(c, err.Error()),
			"csrf":     csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/settings")
}
