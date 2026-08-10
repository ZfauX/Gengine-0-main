// internal/domain/game/game_handler.go
package game

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/validation"

	csrf "gengine-0/internal/pkg/csrf"
)

const multipartOverhead = 2 * 1024
const gameFormMaxBodySize = 5 * 1024 * 1024

type levelPreview struct {
	ID          uint              `json:"id"`
	Position    int               `json:"position"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Questions   []questionPreview `json:"questions"`
}

type questionPreview struct {
	Text    string   `json:"text"`
	Hint    string   `json:"hint"`
	Answers []string `json:"answers"`
}

type GameHandler struct {
	gameService     GameServiceInterface
	coAuthorService CoAuthorServiceInterface
	auditService    AuditServiceInterface
}

func NewGameHandler(
	gameService GameServiceInterface,
	coAuthorService CoAuthorServiceInterface,
	auditSvc AuditServiceInterface,
) *GameHandler {
	return &GameHandler{
		gameService:     gameService,
		coAuthorService: coAuthorService,
		auditService:    auditSvc,
	}
}

func LimitRequestBody(c *gin.Context, maxBytes int64) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+multipartOverhead)
	if c.Request.ContentLength > maxBytes+multipartOverhead {
		return errors.New("размер тела запроса превышает допустимый лимит")
	}
	return nil
}

// parseDateTime парсит datetime-local строку из формы в UTC (UX-1, pass 31).
// Пользователь вводит локальное время; вычитаем его TZOffset (минуты от UTC
// из cookie tz_offset), чтобы в БД сохранялось корректное абсолютное время.
func parseDateTime(c *gin.Context, s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02T15:04", s)
	if err != nil {
		return nil, err
	}
	// t интерпретируется как локальное время пользователя → UTC = t - offset.
	offset := render.TZOffsetFromCookie(c)
	t = t.Add(-time.Duration(offset) * time.Minute)
	return &t, nil
}

func parseGameDatesFromForm(c *gin.Context, startsAtStr, registrationDeadlineStr string) (*time.Time, *time.Time, error) {
	startsAt, err := parseDateTime(c, startsAtStr)
	if err != nil {
		return nil, nil, err
	}
	registrationDeadline, err := parseDateTime(c, registrationDeadlineStr)
	if err != nil {
		return nil, nil, err
	}
	if err := validation.ValidateGameDates(startsAt, registrationDeadline); err != nil {
		return nil, nil, err
	}
	return startsAt, registrationDeadline, nil
}

// List отображает список игр.
// @Summary Список игр
// @Description Возвращает страницу со списком игр с фильтрацией по статусу, поиском и пагинацией
// @Tags games
// @Produce html
// @Param status query string false "Статус игры (draft, published, started, finished)"
// @Param search query string false "Поиск по названию"
// @Param page query int false "Номер страницы" default(1)
// @Success 200 {string} html "Список игр"
// @Router /games [get]
func (h *GameHandler) List(c *gin.Context) {
	userID := c.GetUint("userID")

	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		page = 1
	}
	perPage, err := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if err != nil || perPage < 1 || perPage > 100 {
		perPage = 20
	}

	sortField := c.DefaultQuery("sort", "created_at")
	sortOrder := c.DefaultQuery("order", "desc")
	validSortFields := map[string]bool{"created_at": true, "starts_at": true, "name": true, "rating": true}
	validSortOrders := map[string]bool{"asc": true, "desc": true}
	if !validSortFields[sortField] {
		sortField = "created_at"
	}
	if !validSortOrders[sortOrder] {
		sortOrder = "desc"
	}

	filter := GameFilter{
		Status:   c.Query("status"),
		Search:   c.Query("search"),
		DateFrom: c.Query("date_from"),
		DateTo:   c.Query("date_to"),
		ViewerID: userID,
	}
	if authorIDStr := c.Query("author_id"); authorIDStr != "" {
		if id, idParseErr := strconv.Atoi(authorIDStr); idParseErr == nil {
			uid := uint(id)
			filter.AuthorID = &uid
		}
	}

	sort := &GameSort{Field: sortField, Order: SortOrder(sortOrder)}

	games, total, err := h.gameService.ListFilteredPaginated(c.Request.Context(), filter, sort, page, perPage)
	if err != nil {
		log.Error().Err(err).Msg("GameHandler.List: failed to list games")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	totalPages := (total + int64(perPage) - 1) / int64(perPage)
	render.Page(c, http.StatusOK, "games-list.html", gin.H{
		"Games":         games,
		"CurrentUserID": userID,
		"Filter":        filter,
		"Page":          page,
		"PerPage":       perPage,
		"TotalPages":    totalPages,
		"Total":         total,
		// U-3: серверное предпочтение вида — без FOUC и ожидания JS.
		"PreferredView": h.gameService.GetUserGamesView(c.Request.Context(), userID),
		"Title":         "Игры",
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.games"},
		},
	})
}

// Show отображает детальную информацию об игре.
// @Summary Детали игры
// @Tags games
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Страница игры"
// @Failure 404 {object} map[string]interface{} render.Tr(c, "handler.game_not_found")
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id} [get]
func (h *GameHandler) Show(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")
	isAdmin := middleware.IsAdmin(c)

	g, reviews, avgRating, reviewsCount, err := h.gameService.ShowGame(c.Request.Context(), uint(id), userID, isAdmin)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrGameNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Int("game_id", id).Msg("GameHandler.Show: failed to get game")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	isManager, err := h.coAuthorService.IsUserManager(c.Request.Context(), uint(id), userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", id).Msg("GameHandler.Show: failed to check manager")
		isManager = false
	}
	if isAdmin {
		isManager = true
	}

	canApply := !g.IsDraft &&
		(g.RegistrationDeadline == nil || g.RegistrationDeadline.After(time.Now()))

	// UX-5 (pass 36) + UX-1 (pass 37): absolute og:image для шеринга в соцсетях.
	// CoverPath относительный (/uploads/...) — нужен абсолютный URL. Схему
	// определяем как HSTS: собственный TLS ИЛИ X-Forwarded-Proto (за
	// reverse-proxy/nginx) — иначе og:image уходил бы как http:// в продакшене.
	ogImage := ""
	if g.CoverPath != "" {
		if strings.HasPrefix(g.CoverPath, "http://") || strings.HasPrefix(g.CoverPath, "https://") {
			ogImage = g.CoverPath
		} else {
			scheme := "http"
			if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			ogImage = scheme + "://" + c.Request.Host + g.CoverPath
		}
	}

	render.Page(c, http.StatusOK, "games-show.html", gin.H{
		"Game":           g,
		"CurrentUserID":  userID,
		"IsManager":      isManager,
		"Reviews":        reviews,
		"AvgRating":      avgRating,
		"ReviewsCount":   reviewsCount,
		"CanApply":       canApply,
		"IncludeLeaflet": true,
		"csrf":           csrf.GetToken(c),
		// SEO-1 (pass 39): суффикс «· Gengine-0» добавляет layout —
		// иначе было «Name · Gengine-0 · Gengine-0».
		"Title": g.Name,
		// UX-04 (pass 45): Description для layout-og:description — раньше layout
		// эмитил generic-описание, а ExtraHead добавлял второй og:description.
		"Description": g.Description,
		"OGImage":     ogImage,
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.games", "url": "/games"},
			{"name": g.Name},
		},
	})
}

// NewForm отображает форму создания игры.
// @Summary Форма создания игры
// @Tags games
// @Produce html
// @Success 200 {string} html "Форма создания"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /games/new [get]
// @Security JWT
func (h *GameHandler) NewForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "games-new.html", gin.H{
		"csrf":  csrf.GetToken(c),
		"Title": "Создание игры",
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.games", "url": "/games"},
			{"name": "nav.new_game"},
		},
	})
}

// Create создаёт новую игру.
// @Summary Создание игры
// @Tags games
// @Accept multipart/form-data
// @Produce html
// @Param name formData string true "Название игры"
// @Param description formData string false "Описание игры"
// @Param category formData string false "Категория"
// @Param cover_image formData file false "Изображение обложки"
// @Success 302 {string} string "Перенаправление на страницу игры"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Router /games/new [post]
// @Security JWT
func (h *GameHandler) Create(c *gin.Context) {
	userID := c.GetUint("userID")

	if limitErr := LimitRequestBody(c, gameFormMaxBodySize); limitErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("form", limitErr)
		render.Page(c, http.StatusBadRequest, "games-new.html", gin.H{
			"Error":  errs.Error(),
			"Errors": errs,
			"csrf":   csrf.GetToken(c),
			"Title":  "Создание игры",
		})
		return
	}

	var input CreateGameInput
	if bindErr := c.ShouldBind(&input); bindErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("name", validation.ValidateString("Название", input.Name, 3, 100))
		errs.Add("description", validation.ValidateString("Описание", input.Description, 0, 2000))
		if input.Visibility != "public" && input.Visibility != "private" {
			errs.Add("visibility", errors.New("видимость должна быть public или private"))
		}
		if input.MaxTeamNumber < 1 || input.MaxTeamNumber > 100 {
			errs.Add("max_team_number", errors.New("максимальное количество участников должно быть от 1 до 100"))
		}
		render.Page(c, http.StatusBadRequest, "games-new.html", gin.H{
			"Error":  errs.Error(),
			"Errors": errs,
			"csrf":   csrf.GetToken(c),
			"Title":  "Создание игры",
		})
		return
	}

	startsAt, parseErr := parseDateTime(c, input.StartsAt)
	if parseErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("starts_at", errors.New("неверный формат даты начала"))
		render.Page(c, http.StatusBadRequest, "games-new.html", gin.H{
			"Error":  errs.Error(),
			"Errors": errs,
			"csrf":   csrf.GetToken(c),
			"Title":  "Создание игры",
		})
		return
	}
	registrationDeadline, deadlineErr := parseDateTime(c, input.RegistrationDeadline)
	if deadlineErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("registration_deadline", errors.New("неверный формат крайнего срока регистрации"))
		render.Page(c, http.StatusBadRequest, "games-new.html", gin.H{
			"Error":  errs.Error(),
			"Errors": errs,
			"csrf":   csrf.GetToken(c),
			"Title":  "Создание игры",
		})
		return
	}
	if validateErr := validation.ValidateGameDates(startsAt, registrationDeadline); validateErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("form", validateErr)
		render.Page(c, http.StatusBadRequest, "games-new.html", gin.H{
			"Error":  errs.Error(),
			"Errors": errs,
			"csrf":   csrf.GetToken(c),
			"Title":  "Создание игры",
		})
		return
	}

	createDTO := &CreateGameDTO{
		Name:                 sanitize.StripHTML(input.Name),
		Description:          sanitize.StripHTML(input.Description),
		MaxTeamNumber:        input.MaxTeamNumber,
		Visibility:           input.Visibility,
		StartsAt:             startsAt,
		RegistrationDeadline: registrationDeadline,
		IsDraft:              input.IsDraft,
	}

	if input.CoverFile != nil && input.CoverFile.Size > 0 {
		createDTO.CoverFile = input.CoverFile
	}

	game, createErr := h.gameService.CreateGameWithCover(c.Request.Context(), createDTO, userID)
	if createErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("form", createErr)
		render.Page(c, http.StatusInternalServerError, "games-new.html", gin.H{
			"Error":  errs.Error(),
			"Errors": errs,
			"csrf":   csrf.GetToken(c),
			"Title":  "Создание игры",
		})
		return
	}

	h.auditService.Log(userID, "create", "game", game.ID, game.Name)
	c.Redirect(http.StatusFound, "/games/"+strconv.Itoa(int(game.ID)))
}

// EditForm отображает форму редактирования игры.
// @Summary Форма редактирования игры
// @Tags games
// @Produce html
// @Param id path int true "ID игры"
// @Success 200 {string} html "Форма редактирования"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Failure 404 {object} map[string]interface{} render.Tr(c, "handler.game_not_found")
// @Router /games/{id}/edit [get]
// @Security JWT
func (h *GameHandler) EditForm(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(c.Request.Context(), uint(id), userID, middleware.IsAdmin(c))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Int("game_id", id).Msg("GameHandler.EditForm: failed to get game")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	isManager, err := h.coAuthorService.IsUserManager(c.Request.Context(), uint(id), userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", id).Msg("GameHandler.EditForm: failed to check manager")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	if !isManager && !middleware.IsAdmin(c) {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	render.Page(c, http.StatusOK, "games-edit.html", gin.H{
		"Game":          g,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
		"Title":         "Редактирование игры",
		"Breadcrumbs": []map[string]string{
			{"name": "nav.home", "url": "/"},
			{"name": "nav.games", "url": "/games"},
			{"name": g.Name, "url": "/games/" + c.Param("id")},
			{"name": "nav.edit_game"},
		},
	})
}

// Update обновляет игру.
// @Summary Обновление игры
// @Tags games
// @Accept multipart/form-data
// @Produce html
// @Param id path int true "ID игры"
// @Param name formData string false "Название игры"
// @Param description formData string false "Описание игры"
// @Param category formData string false "Категория"
// @Param cover_image formData file false "Изображение обложки"
// @Success 302 {string} string "Перенаправление на страницу игры"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/edit [post]
// @Security JWT
func (h *GameHandler) Update(c *gin.Context) {
	id, parseErr := strconv.Atoi(c.Param("id"))
	if parseErr != nil || id <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")

	if limitErr := LimitRequestBody(c, gameFormMaxBodySize); limitErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("form", limitErr)
		render.Page(c, http.StatusBadRequest, "games-edit.html", gin.H{
			"Error":  errs.Error(),
			"Errors": errs,
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	var input UpdateGameInput
	if bindErr := c.ShouldBind(&input); bindErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("name", validation.ValidateString("Название", input.Name, 3, 100))
		errs.Add("description", validation.ValidateString("Описание", input.Description, 0, 2000))
		if input.Visibility != "public" && input.Visibility != "private" {
			errs.Add("visibility", errors.New("видимость должна быть public или private"))
		}
		if input.MaxTeamNumber < 1 || input.MaxTeamNumber > 100 {
			errs.Add("max_team_number", errors.New("максимальное количество участников должно быть от 1 до 100"))
		}
		render.Page(c, http.StatusBadRequest, "games-edit.html", gin.H{
			"Error":  errs.Error(),
			"Errors": errs,
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	existingGame, getErr := h.gameService.GetByID(c.Request.Context(), uint(id), userID, middleware.IsAdmin(c))
	if getErr != nil {
		if errors.Is(getErr, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	startsAt, parseErr := parseDateTime(c, input.StartsAt)
	if parseErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("starts_at", errors.New("неверный формат даты начала"))
		render.Page(c, http.StatusBadRequest, "games-edit.html", gin.H{
			"Game":                 existingGame,
			"Error":                errs.Error(),
			"Errors":               errs,
			"csrf":                 csrf.GetToken(c),
			"Name":                 input.Name,
			"Description":          input.Description,
			"MaxTeamNumber":        input.MaxTeamNumber,
			"Visibility":           input.Visibility,
			"StartsAt":             input.StartsAt,
			"RegistrationDeadline": input.RegistrationDeadline,
		})
		return
	}
	registrationDeadline, deadlineErr := parseDateTime(c, input.RegistrationDeadline)
	if deadlineErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("registration_deadline", errors.New("неверный формат крайнего срока регистрации"))
		render.Page(c, http.StatusBadRequest, "games-edit.html", gin.H{
			"Game":                 existingGame,
			"Error":                errs.Error(),
			"Errors":               errs,
			"csrf":                 csrf.GetToken(c),
			"Name":                 input.Name,
			"Description":          input.Description,
			"MaxTeamNumber":        input.MaxTeamNumber,
			"Visibility":           input.Visibility,
			"StartsAt":             input.StartsAt,
			"RegistrationDeadline": input.RegistrationDeadline,
		})
		return
	}
	if validateErr := validation.ValidateGameDates(startsAt, registrationDeadline); validateErr != nil {
		errs := validation.FieldErrors{}
		errs.Add("form", validateErr)
		render.Page(c, http.StatusBadRequest, "games-edit.html", gin.H{
			"Game":                 existingGame,
			"Error":                errs.Error(),
			"Errors":               errs,
			"csrf":                 csrf.GetToken(c),
			"Name":                 input.Name,
			"Description":          input.Description,
			"MaxTeamNumber":        input.MaxTeamNumber,
			"Visibility":           input.Visibility,
			"StartsAt":             input.StartsAt,
			"RegistrationDeadline": input.RegistrationDeadline,
		})
		return
	}

	updateDTO := &UpdateGameDTO{
		Name:                 sanitize.StripHTML(input.Name),
		Description:          sanitize.StripHTML(input.Description),
		MaxTeamNumber:        input.MaxTeamNumber,
		Visibility:           input.Visibility,
		StartsAt:             startsAt,
		RegistrationDeadline: registrationDeadline,
		IsDraft:              existingGame.IsDraft,
		DeleteCover:          input.DeleteCover,
	}

	if input.CoverFile != nil && input.CoverFile.Size > 0 {
		updateDTO.CoverFile = input.CoverFile
	}

	isAdmin := middleware.IsAdmin(c)
	if updateErr := h.gameService.UpdateGameWithCover(c.Request.Context(), uint(id), updateDTO, userID, isAdmin); updateErr != nil {
		if errors.Is(updateErr, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
			return
		} else if strings.Contains(updateErr.Error(), "только автор") {
			// Бизнес-ошибка прав — 403 с локализованным сообщением.
			render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, updateErr.Error()))
			return
		} else {
			// Реальная ошибка БД/файлов — 500 generic (C-1), не показываем сырой текст.
			log.Error().Err(updateErr).Int("game_id", id).Msg("Update: failed to update game")
			render.RenderErrorPage(c, http.StatusInternalServerError)
			return
		}
	}

	h.auditService.Log(userID, "update", "game", uint(id), input.Name)
	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}

// Delete удаляет игру.
// @Summary Удаление игры
// @Tags games
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID игры"
// @Success 302 {string} string "Перенаправление на /games"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/delete [post]
// @Security JWT
func (h *GameHandler) Delete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")

	if err := h.gameService.Delete(c.Request.Context(), uint(id), userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else if strings.Contains(err.Error(), "только владелец") {
			// Бизнес-ошибка прав — 403 с локализованным сообщением.
			render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, err.Error()))
		} else {
			// Реальная ошибка БД — 500 generic (C-1).
			log.Error().Err(err).Int("game_id", id).Uint("user", userID).Msg("GameHandler.Delete: failed to delete game")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	h.auditService.Log(userID, "delete", "game", uint(id), "")
	c.Redirect(http.StatusFound, "/games")
}

// Publish публикует игру.
// @Summary Публикация игры
// @Tags games
// @Accept x-www-form-urlencoded
// @Produce html
// @Param id path int true "ID игры"
// @Success 302 {string} string "Перенаправление на страницу игры"
// @Failure 400 {object} map[string]interface{} "Ошибка валидации"
// @Failure 401 {object} map[string]interface{} "Требуется аутентификация"
// @Failure 403 {object} map[string]interface{} render.Tr(c, "handler.forbidden")
// @Router /games/{id}/publish [post]
// @Security JWT
func (h *GameHandler) Publish(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		render.RenderError(c, http.StatusBadRequest, render.Tr(c, "handler.invalid_game_id"))
		return
	}
	userID := c.GetUint("userID")

	if err := h.gameService.Publish(c.Request.Context(), uint(id), userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			render.RenderError(c, http.StatusForbidden, render.LocalizeError(c, err.Error()))
		}
		return
	}

	h.auditService.Log(userID, "publish", "game", uint(id), "")
	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}
