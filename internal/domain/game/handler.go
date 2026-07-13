// internal/domain/game/handler.go
package game

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	apperr "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/storage"
	"gengine-0/internal/pkg/validation"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// =============================================================================
// ИНТЕРФЕЙСЫ ДЛЯ ТЕСТИРУЕМОСТИ
// =============================================================================

type GameServiceInterface interface {
	GetByID(ctx context.Context, id, userID uint) (*Game, error)
	CreateGameWithCover(ctx context.Context, dto *CreateGameDTO, authorID uint) (*Game, error)
	UpdateGameWithCover(ctx context.Context, gameID uint, dto *UpdateGameDTO, userID uint) error
	ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error)
	Delete(ctx context.Context, id uint, userID uint) error
	Publish(ctx context.Context, id uint, userID uint) error
	ListReviews(ctx context.Context, gameID uint) ([]Review, error)
	GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error)
	IsUserManager(gameID, userID uint) (bool, error)
	GetSettingsWithDefaults(ctx context.Context, gameID uint) (*GameSetting, error)
	SaveSettings(ctx context.Context, gameID uint, settings GameSetting) (*GameSetting, error)
}

type CoAuthorServiceInterface interface {
	IsUserManager(gameID, userID uint) (bool, error)
	HasPermission(gameID, userID uint, requiredRole string) (bool, error)
	CanModerateGame(gameID, userID uint) (bool, error)
	CanEditContent(gameID, userID uint) (bool, error)
	Add(gameID, newCoAuthorID, ownerID uint) error
	Remove(gameID, coAuthorUserID, ownerID uint) error
	List(gameID uint) ([]CoAuthor, error)
}

type AuditServiceInterface interface {
	Log(userID uint, action, objectType string, objectID uint, details string)
}

type GamePassingServiceInterface interface {
	Apply(ctx context.Context, gameID, teamID, userID uint) error
	ListByGame(ctx context.Context, gameID uint) ([]GamePassing, error)
	ListTestPassings(ctx context.Context, gameID uint, result *[]GamePassing) error
	UpdateStatus(ctx context.Context, passingID uint, status GamePassingStatus, userID uint) error
	StartGame(ctx context.Context, passingID, userID uint) error
	GetTeamsByCaptain(ctx context.Context, userID uint) ([]team.Team, error)
}

type GamePlayServiceInterface interface {
	SubmitCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error)
	SubmitFile(ctx context.Context, passingID, userID uint, filePath string) (*Attempt, error)
	UseHint(ctx context.Context, passingID, userID uint) error
	AcceptBlackboxAnswer(ctx context.Context, passingID, userID uint) error
	StartTesting(ctx context.Context, gameID, userID uint) (*GamePassing, error)
	SubmitTestCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error)
	SkipLevelTest(ctx context.Context, passingID, userID uint) error
	GetGameplayData(ctx context.Context, passingID uint) (*GameplayData, error)
	GetPassingWithGame(ctx context.Context, passingID uint) (*GamePassing, error)
	IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error)
}

// GameplayData содержит все данные, необходимые для рендеринга страницы геймплея.
type GameplayData struct {
	Passing      GamePassing
	Level        level.Level
	Settings     GameSetting
	Attempts     []Attempt
	VotingActive bool
	TimeLimitSec int
}

type GameAdminServiceInterface interface {
	ForceFinishGame(ctx context.Context, gameID uint) error
	DisqualifyTeam(ctx context.Context, gameID, teamID uint) error
	DeleteLevelFromActiveGame(ctx context.Context, gameID, levelID, userID uint) error
}

// =============================================================================
// ВХОДНЫЕ СТРУКТУРЫ
// =============================================================================

// CreateGameInput используется для создания игры.
type CreateGameInput struct {
	Name                 string                `form:"name" binding:"required,min=3,max=100"`
	Description          string                `form:"description" binding:"max=2000"`
	MaxTeamNumber        int                   `form:"max_team_number" binding:"required,min=1,max=100"`
	Visibility           string                `form:"visibility" binding:"required,oneof=public private"`
	StartsAt             string                `form:"starts_at"`
	RegistrationDeadline string                `form:"registration_deadline"`
	IsDraft              bool                  `form:"is_draft"`
	CoverFile            *multipart.FileHeader `form:"cover"`
}

// UpdateGameInput используется для обновления игры.
type UpdateGameInput struct {
	Name                 string                `form:"name" binding:"required,min=3,max=100"`
	Description          string                `form:"description" binding:"max=2000"`
	MaxTeamNumber        int                   `form:"max_team_number" binding:"required,min=1,max=100"`
	Visibility           string                `form:"visibility" binding:"required,oneof=public private"`
	StartsAt             string                `form:"starts_at"`
	RegistrationDeadline string                `form:"registration_deadline"`
	IsDraft              bool                  `form:"is_draft"`
	CoverFile            *multipart.FileHeader `form:"cover"`
	DeleteCover          bool                  `form:"delete_cover"`
}

type ApplyInput struct {
	TeamID uint `form:"team_id" binding:"required,gt=0"`
}

type DisqualifyInput struct {
	TeamID uint `form:"team_id" binding:"required,gt=0"`
}

type AddCoAuthorInput struct {
	UserID uint `form:"user_id" binding:"required,gt=0"`
}

// ---------- Кастомные валидаторы ----------

func validateStartDate(fl validator.FieldLevel) bool {
	val, ok := fl.Field().Interface().(string)
	if !ok || val == "" {
		return true
	}
	t, err := time.Parse("2006-01-02T15:04", val)
	if err != nil {
		return false
	}
	return !t.Before(time.Now())
}

func init() {
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		_ = v.RegisterValidation("start_date_valid", validateStartDate)
	}
}

// ---------- Вспомогательные типы для FullPreview ----------
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

// =============================================================================
// ОБРАБОТЧИКИ
// =============================================================================

type GameHandler struct {
	gameService     GameServiceInterface
	passingService  GamePassingServiceInterface
	coAuthorService CoAuthorServiceInterface
	noteService     *NoteService
	simulateService *SimulateService
	photoService    *PhotoService
	auditService    AuditServiceInterface
	storage         storage.FileStorage
	hub             *ws.RoomHub
	db              *gorm.DB
	gamePlaySvc     GamePlayServiceInterface
	gameAdminSvc    GameAdminServiceInterface
	levelService    *level.LevelService
}

func NewGameHandler(
	gameService GameServiceInterface,
	passingService GamePassingServiceInterface,
	coAuthorService CoAuthorServiceInterface,
	noteService *NoteService,
	simulateService *SimulateService,
	photoService *PhotoService,
	storage storage.FileStorage,
	hub *ws.RoomHub,
	auditSvc AuditServiceInterface,
	db *gorm.DB,
	gamePlaySvc GamePlayServiceInterface,
	gameAdminSvc GameAdminServiceInterface,
	levelSvc *level.LevelService,
) *GameHandler {
	return &GameHandler{
		gameService:     gameService,
		passingService:  passingService,
		coAuthorService: coAuthorService,
		noteService:     noteService,
		simulateService: simulateService,
		photoService:    photoService,
		auditService:    auditSvc,
		storage:         storage,
		hub:             hub,
		db:              db,
		gamePlaySvc:     gamePlaySvc,
		gameAdminSvc:    gameAdminSvc,
		levelService:    levelSvc,
	}
}

// ---------- Вспомогательная функция для ограничения размера тела запроса ----------

func limitRequestBody(c *gin.Context, maxBytes int64) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	if c.Request.ContentLength > maxBytes {
		return fmt.Errorf("размер тела запроса превышает допустимый лимит (%d байт)", maxBytes)
	}
	return nil
}

// parseDateTime парсит строку из datetime-local в time.Time.
func parseDateTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02T15:04", s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// parseGameDatesFromForm парсит и валидирует даты из формы создания/редактирования игры.
// Возвращает startsAt, registrationDeadline и ошибку.
func parseGameDatesFromForm(startsAtStr, registrationDeadlineStr string) (*time.Time, *time.Time, error) {
	startsAt, err := parseDateTime(startsAtStr)
	if err != nil {
		return nil, nil, fmt.Errorf("неверный формат даты начала: %w", err)
	}
	registrationDeadline, err := parseDateTime(registrationDeadlineStr)
	if err != nil {
		return nil, nil, fmt.Errorf("неверный формат крайнего срока регистрации: %w", err)
	}
	if err := validation.ValidateGameDates(startsAt, registrationDeadline); err != nil {
		return nil, nil, err
	}
	return startsAt, registrationDeadline, nil
}

// List отображает список игр с фильтрацией и пагинацией.
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

	filter := GameFilter{
		Status:   c.Query("status"),
		Search:   c.Query("search"),
		DateFrom: c.Query("date_from"),
		DateTo:   c.Query("date_to"),
		ViewerID: userID,
	}
	if authorIDStr := c.Query("author_id"); authorIDStr != "" {
		if id, err := strconv.Atoi(authorIDStr); err == nil {
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
	})
}

// Show отображает одну игру.
func (h *GameHandler) Show(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(c.Request.Context(), uint(id), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Int("game_id", id).Msg("GameHandler.Show: failed to get game")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	isManager, err := h.coAuthorService.IsUserManager(uint(id), userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", id).Msg("GameHandler.Show: failed to check manager")
		isManager = false
	}

	reviews, err := h.gameService.ListReviews(c.Request.Context(), uint(id))
	if err != nil {
		log.Error().Err(err).Int("game_id", id).Msg("GameHandler.Show: failed to list reviews")
		reviews = []Review{}
	}
	avgRating, reviewsCount, err := h.gameService.GetAverageRating(c.Request.Context(), uint(id))
	if err != nil {
		log.Error().Err(err).Int("game_id", id).Msg("GameHandler.Show: failed to get average rating")
		avgRating = 0
		reviewsCount = 0
	}

	canApply := !g.IsDraft && (g.StartsAt == nil || g.StartsAt.After(time.Now()))

	render.Page(c, http.StatusOK, "games-show.html", gin.H{
		"Game":          g,
		"CurrentUserID": userID,
		"IsManager":     isManager,
		"Reviews":       reviews,
		"AvgRating":     avgRating,
		"ReviewsCount":  reviewsCount,
		"CanApply":      canApply,
		"csrf":          csrf.GetToken(c),
	})
}

// NewForm отображает форму создания игры.
func (h *GameHandler) NewForm(c *gin.Context) {
	render.Page(c, http.StatusOK, "games-new.html", gin.H{
		"csrf": csrf.GetToken(c),
	})
}

// Create создаёт новую игру.
func (h *GameHandler) Create(c *gin.Context) {
	userID := c.GetUint("userID")

	if err := limitRequestBody(c, 5*1024*1024); err != nil {
		render.Page(c, http.StatusBadRequest, "games-new.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	var input CreateGameInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "games-new.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Парсим и валидируем даты
	startsAt, registrationDeadline, err := parseGameDatesFromForm(input.StartsAt, input.RegistrationDeadline)
	if err != nil {
		render.Page(c, http.StatusBadRequest, "games-new.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
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

	game, err := h.gameService.CreateGameWithCover(c.Request.Context(), createDTO, userID)
	if err != nil {
		render.Page(c, http.StatusInternalServerError, "games-new.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	h.auditService.Log(userID, "create", "game", game.ID, game.Name)
	c.Redirect(http.StatusFound, "/games")
}

// EditForm отображает форму редактирования игры.
func (h *GameHandler) EditForm(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(c.Request.Context(), uint(id), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Int("game_id", id).Msg("GameHandler.EditForm: failed to get game")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	isManager, err := h.coAuthorService.IsUserManager(uint(id), userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", id).Msg("GameHandler.EditForm: failed to check manager")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}
	if !isManager {
		render.RenderErrorPage(c, http.StatusForbidden)
		return
	}

	render.Page(c, http.StatusOK, "games-edit.html", gin.H{
		"Game":          g,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       middleware.IsAdmin(c),
	})
}

// Update обновляет игру.
func (h *GameHandler) Update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	if err := limitRequestBody(c, 5*1024*1024); err != nil {
		render.Page(c, http.StatusBadRequest, "games-edit.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	var input UpdateGameInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "games-edit.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Получаем существующую игру (для сохранения состояния IsDraft и данных формы)
	existingGame, err := h.gameService.GetByID(c.Request.Context(), uint(id), userID)
	if err != nil {
		render.Page(c, http.StatusNotFound, "games-edit.html", gin.H{
			"Error": "Игра не найдена",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	// Парсим и валидируем даты
	startsAt, registrationDeadline, err := parseGameDatesFromForm(input.StartsAt, input.RegistrationDeadline)
	if err != nil {
		render.Page(c, http.StatusBadRequest, "games-edit.html", gin.H{
			"Game":  existingGame,
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
			// Передаём введённые значения для сохранения в форме
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
		IsDraft:              existingGame.IsDraft, // сохраняем текущее состояние
		DeleteCover:          input.DeleteCover,
	}

	if input.CoverFile != nil && input.CoverFile.Size > 0 {
		updateDTO.CoverFile = input.CoverFile
	}

	if err := h.gameService.UpdateGameWithCover(c.Request.Context(), uint(id), updateDTO, userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.Page(c, http.StatusNotFound, "games-edit.html", gin.H{
				"Error": "Игра не найдена",
				"csrf":  csrf.GetToken(c),
			})
		} else {
			render.Page(c, http.StatusForbidden, "games-edit.html", gin.H{
				"Game":  existingGame,
				"Error": err.Error(),
				"csrf":  csrf.GetToken(c),
				// Передаём введённые значения
				"Name":                 input.Name,
				"Description":          input.Description,
				"MaxTeamNumber":        input.MaxTeamNumber,
				"Visibility":           input.Visibility,
				"StartsAt":             input.StartsAt,
				"RegistrationDeadline": input.RegistrationDeadline,
			})
			return
		}

		h.auditService.Log(userID, "update", "game", uint(id), input.Name)
		c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
	}

	h.auditService.Log(userID, "update", "game", uint(id), input.Name)
	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}

// Delete удаляет игру (только владелец).
func (h *GameHandler) Delete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	if err := h.gameService.Delete(c.Request.Context(), uint(id), userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			render.RenderError(c, http.StatusForbidden, err.Error())
		}
		return
	}

	h.auditService.Log(userID, "delete", "game", uint(id), "")
	c.Redirect(http.StatusFound, "/games")
}

// Publish публикует черновик игры.
func (h *GameHandler) Publish(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	if err := h.gameService.Publish(c.Request.Context(), uint(id), userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			render.RenderError(c, http.StatusForbidden, err.Error())
		}
		return
	}

	h.auditService.Log(userID, "publish", "game", uint(id), "")
	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}

// ----- Прохождения и заявки -----

// ListPassings отображает все заявки и прохождения игры.
func (h *GameHandler) ListPassings(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	passings, err := h.passingService.ListByGame(c.Request.Context(), uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.ListPassings: failed to list passings")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "game_passings-list.html", gin.H{
		"GameID":        gameID,
		"Passings":      passings,
		"UserID":        userID,
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
		"csrf":          csrf.GetToken(c),
	})
}

// ApplyForm отображает форму подачи заявки.
func (h *GameHandler) ApplyForm(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	teams, err := h.passingService.GetTeamsByCaptain(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("GameHandler.ApplyForm: failed to get teams")
		teams = []team.Team{}
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "game_passings-apply.html", gin.H{
		"GameID":        gameID,
		"Teams":         teams,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// Apply подаёт заявку на игру.
func (h *GameHandler) Apply(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	renderTeams := func(teams []team.Team, errMsg string) {
		t, _ := h.passingService.GetTeamsByCaptain(c.Request.Context(), userID)
		if t != nil {
			teams = t
		}
		render.Page(c, http.StatusBadRequest, "game_passings-apply.html", gin.H{
			"GameID": gameID,
			"Teams":  teams,
			"Error":  errMsg,
			"csrf":   csrf.GetToken(c),
		})
	}

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		renderTeams(nil, err.Error())
		return
	}

	var input ApplyInput
	if err := c.ShouldBind(&input); err != nil {
		renderTeams(nil, "Неверные данные: "+err.Error())
		return
	}

	if err := validation.ValidatePositiveUint("ID команды", input.TeamID); err != nil {
		renderTeams(nil, err.Error())
		return
	}

	if err := h.passingService.Apply(c.Request.Context(), uint(gameID), input.TeamID, userID); err != nil {
		renderTeams(nil, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}

// UpdatePassingStatus изменяет статус заявки (принять/отклонить).
func (h *GameHandler) UpdatePassingStatus(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}
	status := GamePassingStatus(c.PostForm("status"))
	if status != StatusAccepted && status != StatusRejected {
		render.RenderError(c, http.StatusBadRequest, "Недопустимый статус")
		return
	}

	if err := h.passingService.UpdateStatus(c.Request.Context(), uint(passingID), status, c.GetUint("userID")); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/passings")
}

// StartGame запускает игру для конкретного прохождения.
func (h *GameHandler) StartGame(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID прохождения")
		return
	}

	if err := h.passingService.StartGame(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/monitor")
}

// ForceFinish принудительно завершает игру.
func (h *GameHandler) ForceFinish(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}

	if err := h.gameAdminSvc.ForceFinishGame(c.Request.Context(), uint(gameID)); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/results")
}

// DisqualifyTeam дисквалифицирует команду.
func (h *GameHandler) DisqualifyTeam(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	var input DisqualifyInput
	if err := c.ShouldBind(&input); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные данные: "+err.Error())
		return
	}
	if err := validation.ValidatePositiveUint("ID команды", input.TeamID); err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.gameAdminSvc.DisqualifyTeam(c.Request.Context(), uint(gameID), input.TeamID); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/monitor")
}

// ----- Соавторы -----

// ManageCoAuthors отображает страницу управления соавторами.
func (h *GameHandler) ManageCoAuthors(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	coAuthors, err := h.coAuthorService.List(uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.ManageCoAuthors: failed to list coauthors")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "co_authors-manage.html", gin.H{
		"GameID":        gameID,
		"CoAuthors":     coAuthors,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// AddCoAuthor добавляет соавтора.
func (h *GameHandler) AddCoAuthor(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	ownerID := c.GetUint("userID")

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	var input AddCoAuthorInput
	if err := c.ShouldBind(&input); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверные данные: "+err.Error())
		return
	}
	if err := validation.ValidatePositiveUint("ID пользователя", input.UserID); err != nil {
		render.RenderError(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.coAuthorService.Add(uint(gameID), input.UserID, ownerID); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/co-authors")
}

// RemoveCoAuthor удаляет соавтора.
func (h *GameHandler) RemoveCoAuthor(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || userID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID пользователя")
		return
	}
	ownerID := c.GetUint("userID")

	if err := h.coAuthorService.Remove(uint(gameID), uint(userID), ownerID); err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/co-authors")
}

// ----- Заметки автора -----

// Notes возвращает заметки к игре в формате JSON.
func (h *GameHandler) Notes(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID игры",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")
	notes, err := h.noteService.ListByGame(uint(gameID), userID)
	if err != nil {
		appErr := apperr.NewForbiddenError(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"notes": notes})
}

// CreateNote создаёт новую заметку.
func (h *GameHandler) CreateNote(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID игры",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")

	if err := limitRequestBody(c, 1*1024*1024); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  "bad_request",
		})
		return
	}

	var input struct {
		LevelID *uint  `json:"level_id"`
		Text    string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный формат данных: " + err.Error(),
			"code":  "validation_error",
		})
		return
	}
	if err := validation.ValidateString("Текст заметки", input.Text, 1, 1000); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  "validation_error",
		})
		return
	}
	input.Text = sanitize.StripHTML(input.Text)

	note, err := h.noteService.Create(uint(gameID), input.LevelID, userID, input.Text)
	if err != nil {
		appErr := apperr.NewForbiddenError(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"note": note})
}

// DeleteNote удаляет заметку.
func (h *GameHandler) DeleteNote(c *gin.Context) {
	noteID, err := strconv.Atoi(c.Param("note_id"))
	if err != nil || noteID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID заметки",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")
	if err := h.noteService.Delete(uint(noteID), userID); err != nil {
		appErr := apperr.NewForbiddenError(err.Error())
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ----- Симуляция -----

// Simulate запускает симуляцию прохождения игры.
func (h *GameHandler) Simulate(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")
	result, err := h.simulateService.Simulate(uint(gameID), userID)
	if err != nil {
		render.RenderError(c, http.StatusForbidden, err.Error())
		return
	}
	render.Page(c, http.StatusOK, "simulate-results.html", gin.H{
		"GameID": gameID,
		"Result": result,
	})
}

// ---------- Новые страницы ----------

// SettingsPage отображает страницу настроек игры.
func (h *GameHandler) SettingsPage(c *gin.Context) {
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
	settings, err = h.gameService.GetSettingsWithDefaults(c.Request.Context(), g.ID)
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.SettingsPage: failed to get settings")
		render.RenderErrorPage(c, http.StatusInternalServerError)
		return
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
func (h *GameHandler) SaveSettings(c *gin.Context) {
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
	isManager, err := h.coAuthorService.IsUserManager(g.ID, userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.SaveSettings: failed to check manager")
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
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.SaveSettings: failed to save settings")
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

// TestPage отображает страницу управления тестовыми прохождениями.
func (h *GameHandler) TestPage(c *gin.Context) {
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
			log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.TestPage: failed to get game")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}

	var testPassings []GamePassing
	if err := h.passingService.ListTestPassings(c.Request.Context(), g.ID, &testPassings); err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.TestPage: failed to list test passings")
	}

	isAdmin := middleware.IsAdmin(c)

	render.Page(c, http.StatusOK, "games-test.html", gin.H{
		"Game":          g,
		"TestPassings":  testPassings,
		"csrf":          csrf.GetToken(c),
		"CurrentUserID": userID,
		"IsAdmin":       isAdmin,
	})
}

// PhotosPage отображает страницу фотогалереи.
func (h *GameHandler) PhotosPage(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	var photos []Photo
	if h.photoService != nil {
		photos, err = h.photoService.List(uint(gameID))
		if err != nil {
			log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.PhotosPage: failed to list photos")
		}
	}
	isManager, err := h.coAuthorService.IsUserManager(uint(gameID), userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.PhotosPage: failed to check manager")
		isManager = false
	}

	render.Page(c, http.StatusOK, "games-photos.html", gin.H{
		"GameID":        gameID,
		"Photos":        photos,
		"CurrentUserID": userID,
		"IsManager":     isManager,
		"csrf":          csrf.GetToken(c),
	})
}

// UploadPhoto загружает новое фото в галерею игры.
func (h *GameHandler) UploadPhoto(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID игры",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")

	if err := limitRequestBody(c, 10*1024*1024); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  "bad_request",
		})
		return
	}

	file, header, err := c.Request.FormFile("photo")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Файл не выбран",
			"code":  "bad_request",
		})
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > 10*1024*1024 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Размер файла не должен превышать 10 МБ",
			"code":  "bad_request",
		})
		return
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
	contentType := header.Header.Get("Content-Type")
	if !slices.Contains(allowedTypes, contentType) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Допустимы только JPEG, PNG и WebP",
			"code":  "bad_request",
		})
		return
	}

	webPath, err := h.storage.Save("uploads/photos", file, header.Filename, userID, 10*1024*1024, allowedTypes)
	if err != nil {
		log.Error().Err(err).Str("filename", header.Filename).Msg("UploadPhoto: failed to save file")
		appErr := apperr.NewInternalError(err)
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	photo := &Photo{
		GameID: uint(gameID),
		UserID: userID,
		Path:   webPath,
	}
	if err := h.photoService.Create(photo); err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("UploadPhoto: failed to create photo record")
		if delErr := h.storage.Delete(webPath); delErr != nil {
			log.Error().Err(delErr).Str("path", webPath).Msg("UploadPhoto: failed to delete uploaded file")
		}
		appErr := apperr.NewInternalError(err)
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "photo": photo})
}

// DeletePhoto удаляет фото из галереи.
func (h *GameHandler) DeletePhoto(c *gin.Context) {
	photoID, err := strconv.Atoi(c.Param("photo_id"))
	if err != nil || photoID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID фото",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")

	photo, err := h.photoService.GetByID(uint(photoID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "Фото не найдено",
				"code":  "not_found",
			})
		} else {
			log.Error().Err(err).Int("photo_id", photoID).Msg("DeletePhoto: failed to get photo")
			appErr := apperr.NewInternalError(err)
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
				"error": appErr.Message,
				"code":  appErr.Code,
			})
		}
		return
	}

	isOwner := photo.UserID == userID
	isManager, err := h.coAuthorService.IsUserManager(photo.GameID, userID)
	if err != nil {
		log.Error().Err(err).Int("photo_id", photoID).Msg("DeletePhoto: failed to check manager")
		appErr := apperr.NewInternalError(err)
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if !isOwner && !isManager {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "Нет прав на удаление",
			"code":  "forbidden",
		})
		return
	}

	if err := h.photoService.Delete(photo.ID, userID); err != nil {
		log.Error().Err(err).Uint("photo_id", photo.ID).Msg("DeletePhoto: failed to delete record")
		appErr := apperr.NewInternalError(err)
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if err := h.storage.Delete(photo.Path); err != nil {
		log.Error().Err(err).Str("path", photo.Path).Msg("DeletePhoto: failed to delete file")
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// FullPreview возвращает полную структуру игры для быстрого просмотра.
func (h *GameHandler) FullPreview(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID игры",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")

	_, err = h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "Игра не найдена",
				"code":  "not_found",
			})
		} else {
			appErr := apperr.NewForbiddenError("Нет доступа к игре")
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
				"error": appErr.Message,
				"code":  appErr.Code,
			})
		}
		return
	}

	levels, err := h.levelService.ListWithQuestions(c.Request.Context(), uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("FullPreview: failed to load levels")
		appErr := apperr.NewInternalError(err)
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	var result []levelPreview
	for _, lvl := range levels {
		lp := levelPreview{
			ID:          lvl.ID,
			Position:    lvl.Position,
			Name:        lvl.Name,
			Description: lvl.Description,
		}
		for _, q := range lvl.Questions {
			qp := questionPreview{Text: q.Text, Hint: q.Hint}
			for _, a := range q.Answers {
				qp.Answers = append(qp.Answers, a.Code)
			}
			lp.Questions = append(lp.Questions, qp)
		}
		result = append(result, lp)
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}
