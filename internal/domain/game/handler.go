// internal/domain/game/handler.go
package game

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/validation"

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
	IsUserManager(ctx context.Context, gameID, userID uint) (bool, error)
	GetSettingsWithDefaults(ctx context.Context, gameID uint) (*GameSetting, error)
	SaveSettings(ctx context.Context, gameID uint, settings GameSetting) (*GameSetting, error)
}

type CoAuthorServiceInterface interface {
	IsUserManager(ctx context.Context, gameID, userID uint) (bool, error)
	HasPermission(ctx context.Context, gameID, userID uint, requiredRole string) (bool, error)
	CanModerateGame(ctx context.Context, gameID, userID uint) (bool, error)
	CanEditContent(ctx context.Context, gameID, userID uint) (bool, error)
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
	SubmitCode(ctx context.Context, passingID, userID uint, code string) (*SubmitResult, error)
	SubmitFile(ctx context.Context, passingID, userID uint, filePath string) (*Attempt, error)
	UseHint(ctx context.Context, passingID, userID uint) (string, error)
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
	ForceFinishGame(ctx context.Context, gameID, userID uint) error
	DisqualifyTeam(ctx context.Context, gameID, teamID, userID uint) error
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

// ---------- Вспомогательная функция для ограничения размера тела запроса ----------

// multipartOverhead — примерный размер multipart boundary и заголовков формы.
const multipartOverhead = 2 * 1024 // 2 КБ

func limitRequestBody(c *gin.Context, maxBytes int64) error {
	// Для multipart форм добавляем overhead на boundary и заголовки
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+multipartOverhead)
	if c.Request.ContentLength > maxBytes+multipartOverhead {
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

	isManager, err := h.coAuthorService.IsUserManager(c.Request.Context(), uint(id), userID)
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

	isManager, err := h.coAuthorService.IsUserManager(c.Request.Context(), uint(id), userID)
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
			return
		}
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

// ListPassings отображает все заявки и прохождения игры — делегируется в passing_handler.go.

// ApplyForm отображает форму подачи заявки — делегируется в passing_handler.go.

// Apply подаёт заявку на игру — делегируется в passing_handler.go.

// UpdatePassingStatus изменяет статус заявки — делегируется в passing_handler.go.

// StartGame запускает игру для конкретного прохождения — делегируется в passing_handler.go.

// ForceFinish принудительно завершает игру — делегируется в passing_handler.go.

// DisqualifyTeam дисквалифицирует команду — делегируется в passing_handler.go.

// ManageCoAuthors отображает страницу управления соавторами — делегируется в coauthor_handler.go.

// AddCoAuthor добавляет соавтора — делегируется в coauthor_handler.go.

// RemoveCoAuthor удаляет соавтора — делегируется в coauthor_handler.go.

// Notes возвращает заметки к игре в формате JSON — делегируется в note_handler.go.

// CreateNote создаёт новую заметку — делегируется в note_handler.go.

// DeleteNote удаляет заметку — делегируется в note_handler.go.

// Simulate запускает симуляцию прохождения игры — делегируется в simulate_handler.go.

// SettingsPage отображает страницу настроек игры — делегируется в settings_handler.go.

// SaveSettings сохраняет настройки игры — делегируется в settings_handler.go.

// TestPage отображает страницу управления тестовыми прохождениями — делегируется в test_handler.go.

// PhotosPage отображает страницу фотогалереи — делегируется в photo_handler.go.

// UploadPhoto загружает новое фото в галерею игры — делегируется в photo_handler.go.

// DeletePhoto удаляет фото из галереи — делегируется в photo_handler.go.

// FullPreview возвращает полную структуру игры для быстрого просмотра — делегируется в fullpreview_handler.go.
