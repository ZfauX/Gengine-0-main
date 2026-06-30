// internal/domain/game/handler.go
package game

import (
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/storage"
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

// GameServiceInterface определяет методы GameService, используемые в хендлере.
type GameServiceInterface interface {
	CreateGameWithCover(ctx context.Context, dto *CreateGameDTO, authorID uint) (*Game, error)
	UpdateGameWithCover(ctx context.Context, gameID uint, dto *UpdateGameDTO, userID uint) error
	GetByID(ctx context.Context, id uint, viewerID uint) (*Game, error)
	ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error)
	Delete(ctx context.Context, id uint, userID uint) error
	Publish(ctx context.Context, id uint, userID uint) error
	ForceFinishGame(ctx context.Context, gameID uint) error
	DisqualifyTeam(ctx context.Context, gameID, teamID uint) error
	SubmitCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error)
	SubmitFile(ctx context.Context, passingID, userID uint, filePath string) (*Attempt, error)
	UseHint(ctx context.Context, passingID, userID uint) error
	AcceptBlackboxAnswer(ctx context.Context, passingID, userID uint) error
	StartTesting(ctx context.Context, gameID, userID uint) (*GamePassing, error)
	SubmitTestCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error)
	SkipLevelTest(ctx context.Context, passingID, userID uint) error
	// Новые методы для работы с отзывами
	ListReviews(ctx context.Context, gameID uint) ([]Review, error)
	GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error)
}

// CoAuthorServiceInterface определяет методы CoAuthorService, используемые в хендлере.
type CoAuthorServiceInterface interface {
	IsUserManager(gameID, userID uint) (bool, error)
	HasPermission(gameID, userID uint, requiredRole string) (bool, error)
	CanModerateGame(gameID, userID uint) (bool, error)
	CanEditContent(gameID, userID uint) (bool, error)
	Add(gameID, newCoAuthorID, ownerID uint) error
	Remove(gameID, coAuthorUserID, ownerID uint) error
	List(gameID uint) ([]CoAuthor, error)
}

// AuditServiceInterface определяет методы audit.Service, используемые в хендлере.
type AuditServiceInterface interface {
	Log(userID uint, action, objectType string, objectID uint, details string)
}

// GamePassingServiceInterface определяет методы GamePassingService, используемые в хендлере.
type GamePassingServiceInterface interface {
	Apply(ctx context.Context, gameID, teamID, userID uint) error
	ListByGame(ctx context.Context, gameID uint) ([]GamePassing, error)
	UpdateStatus(ctx context.Context, passingID uint, status GamePassingStatus, userID uint) error
	StartGame(ctx context.Context, passingID, userID uint) error
	GetTeamsByCaptain(ctx context.Context, userID uint) ([]team.Team, error)
}

// =============================================================================
// ВХОДНЫЕ СТРУКТУРЫ
// =============================================================================

// CreateGameInput используется для создания игры.
type CreateGameInput struct {
	Name                 string                `form:"name" binding:"required,min=3,max=100"`
	Description          string                `form:"description" binding:"required,min=10,max=2000"`
	MaxTeamNumber        int                   `form:"max_team_number" binding:"required,min=1,max=100"`
	Visibility           string                `form:"visibility" binding:"required,oneof=public private"`
	StartsAt             *time.Time            `form:"starts_at" binding:"omitempty,start_date_valid"`
	RegistrationDeadline *time.Time            `form:"registration_deadline" binding:"omitempty"`
	IsDraft              bool                  `form:"is_draft"`
	CoverFile            *multipart.FileHeader `form:"cover"`
}

// UpdateGameInput используется для обновления игры.
type UpdateGameInput struct {
	Name                 string                `form:"name" binding:"required,min=3,max=100"`
	Description          string                `form:"description" binding:"required,min=10,max=2000"`
	MaxTeamNumber        int                   `form:"max_team_number" binding:"required,min=1,max=100"`
	Visibility           string                `form:"visibility" binding:"required,oneof=public private"`
	StartsAt             *time.Time            `form:"starts_at" binding:"omitempty,start_date_valid"`
	RegistrationDeadline *time.Time            `form:"registration_deadline" binding:"omitempty"`
	IsDraft              bool                  `form:"is_draft"`
	CoverFile            *multipart.FileHeader `form:"cover"`
	DeleteCover          bool                  `form:"delete_cover"`
}

// ApplyInput – заявка на игру.
type ApplyInput struct {
	TeamID uint `form:"team_id" binding:"required,gt=0"`
}

// DisqualifyInput – дисквалификация команды.
type DisqualifyInput struct {
	TeamID uint `form:"team_id" binding:"required,gt=0"`
}

// AddCoAuthorInput – добавление соавтора.
type AddCoAuthorInput struct {
	UserID uint `form:"user_id" binding:"required,gt=0"`
}

// SubmitCodeInput – ввод кода.
type SubmitCodeInput struct {
	Code string `form:"code" binding:"required"`
}

// SubmitTestCodeInput – ввод кода в тестовом режиме.
type SubmitTestCodeInput struct {
	Code string `form:"code" binding:"required"`
}

// ---------- Кастомные валидаторы ----------

// validateStartDate проверяет, что дата начала не в прошлом.
func validateStartDate(fl validator.FieldLevel) bool {
	t, ok := fl.Field().Interface().(*time.Time)
	if !ok || t == nil {
		return true
	}
	return !t.Before(time.Now())
}

// Регистрация кастомного валидатора при инициализации пакета.
func init() {
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		_ = v.RegisterValidation("start_date_valid", validateStartDate)
	}
}

// ---------- Вспомогательные валидаторы ----------

func validateString(field, value string, minLen, maxLen int) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New(field + " не может быть пустым")
	}
	if len(trimmed) < minLen {
		return errors.New(field + " должен содержать не менее " + strconv.Itoa(minLen) + " символов")
	}
	if len(trimmed) > maxLen {
		return errors.New(field + " не может превышать " + strconv.Itoa(maxLen) + " символов")
	}
	return nil
}

func validatePositiveUint(field string, value uint) error {
	if value == 0 {
		return errors.New(field + " должен быть положительным числом")
	}
	return nil
}

// validateGameDates проверяет корректность дат (дедлайн не позже старта).
func validateGameDates(startsAt, registrationDeadline *time.Time) error {
	if registrationDeadline != nil && registrationDeadline.Before(time.Now()) {
		return errors.New("крайний срок регистрации не может быть в прошлом")
	}
	if startsAt != nil && startsAt.Before(time.Now()) {
		return errors.New("дата начала не может быть в прошлом")
	}
	if registrationDeadline != nil && startsAt != nil && registrationDeadline.After(*startsAt) {
		return errors.New("крайний срок регистрации не может быть позже даты начала")
	}
	return nil
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
	}
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
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
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
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(c.Request.Context(), uint(id), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			log.Error().Err(err).Int("game_id", id).Msg("GameHandler.Show: failed to get game")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}

	isManager, err := h.coAuthorService.IsUserManager(uint(id), userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", id).Msg("GameHandler.Show: failed to check manager")
		isManager = false
	}

	// Получаем отзывы через интерфейс
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

// Create создаёт новую игру с использованием входной структуры.
func (h *GameHandler) Create(c *gin.Context) {
	userID := c.GetUint("userID")

	var input CreateGameInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "games-new.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := validateGameDates(input.StartsAt, input.RegistrationDeadline); err != nil {
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
		StartsAt:             input.StartsAt,
		RegistrationDeadline: input.RegistrationDeadline,
		IsDraft:              input.IsDraft,
	}

	if input.CoverFile != nil {
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
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(c.Request.Context(), uint(id), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			log.Error().Err(err).Int("game_id", id).Msg("GameHandler.EditForm: failed to get game")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}

	isManager, err := h.coAuthorService.IsUserManager(uint(id), userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", id).Msg("GameHandler.EditForm: failed to check manager")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	if !isManager {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	render.Page(c, http.StatusOK, "games-edit.html", gin.H{
		"Game": g,
		"csrf": csrf.GetToken(c),
	})
}

// Update обновляет игру с использованием входной структуры.
func (h *GameHandler) Update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	var input UpdateGameInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "games-edit.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := validateGameDates(input.StartsAt, input.RegistrationDeadline); err != nil {
		render.Page(c, http.StatusBadRequest, "games-edit.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	updateDTO := &UpdateGameDTO{
		Name:                 sanitize.StripHTML(input.Name),
		Description:          sanitize.StripHTML(input.Description),
		MaxTeamNumber:        input.MaxTeamNumber,
		Visibility:           input.Visibility,
		StartsAt:             input.StartsAt,
		RegistrationDeadline: input.RegistrationDeadline,
		IsDraft:              input.IsDraft,
		DeleteCover:          input.DeleteCover,
	}
	if input.CoverFile != nil {
		updateDTO.CoverFile = input.CoverFile
	}

	if err := h.gameService.UpdateGameWithCover(c.Request.Context(), uint(id), updateDTO, userID); err != nil {
		render.Page(c, http.StatusForbidden, "games-edit.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
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
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.gameService.Delete(c.Request.Context(), uint(id), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	h.auditService.Log(userID, "delete", "game", uint(id), "")
	c.Redirect(http.StatusFound, "/games")
}

// Publish публикует черновик игры.
func (h *GameHandler) Publish(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	if err := h.gameService.Publish(c.Request.Context(), uint(id), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
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
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	passings, err := h.passingService.ListByGame(c.Request.Context(), uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.ListPassings: failed to list passings")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	render.Page(c, http.StatusOK, "game_passings-list.html", gin.H{
		"GameID":   gameID,
		"Passings": passings,
		"UserID":   userID,
		"csrf":     csrf.GetToken(c),
	})
}

// ApplyForm отображает форму подачи заявки.
func (h *GameHandler) ApplyForm(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	teams, err := h.passingService.GetTeamsByCaptain(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("GameHandler.ApplyForm: failed to get teams")
		teams = []team.Team{}
	}

	render.Page(c, http.StatusOK, "game_passings-apply.html", gin.H{
		"GameID": gameID,
		"Teams":  teams,
		"csrf":   csrf.GetToken(c),
	})
}

// Apply подаёт заявку на игру.
func (h *GameHandler) Apply(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	var input ApplyInput
	if err := c.ShouldBind(&input); err != nil {
		teams, _ := h.passingService.GetTeamsByCaptain(c.Request.Context(), userID)
		render.Page(c, http.StatusBadRequest, "game_passings-apply.html", gin.H{
			"GameID": gameID,
			"Teams":  teams,
			"Error":  "Неверные данные: " + err.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	if err := validatePositiveUint("ID команды", input.TeamID); err != nil {
		teams, _ := h.passingService.GetTeamsByCaptain(c.Request.Context(), userID)
		render.Page(c, http.StatusBadRequest, "game_passings-apply.html", gin.H{
			"GameID": gameID,
			"Teams":  teams,
			"Error":  err.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	if err := h.passingService.Apply(c.Request.Context(), uint(gameID), input.TeamID, userID); err != nil {
		teams, _ := h.passingService.GetTeamsByCaptain(c.Request.Context(), userID)
		render.Page(c, http.StatusBadRequest, "game_passings-apply.html", gin.H{
			"GameID": gameID,
			"Teams":  teams,
			"Error":  err.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}

// UpdatePassingStatus изменяет статус заявки (принять/отклонить).
func (h *GameHandler) UpdatePassingStatus(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
		return
	}
	status := GamePassingStatus(c.PostForm("status"))
	if status != StatusAccepted && status != StatusRejected {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Недопустимый статус"})
		return
	}

	if err := h.passingService.UpdateStatus(c.Request.Context(), uint(passingID), status, c.GetUint("userID")); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/passings")
}

// StartGame запускает игру для конкретного прохождения.
func (h *GameHandler) StartGame(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
		return
	}

	if err := h.passingService.StartGame(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/monitor")
}

// ForceFinish принудительно завершает игру.
func (h *GameHandler) ForceFinish(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}

	if err := h.gameService.ForceFinishGame(c.Request.Context(), uint(gameID)); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/results")
}

// DisqualifyTeam дисквалифицирует команду.
func (h *GameHandler) DisqualifyTeam(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}

	var input DisqualifyInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверные данные: " + err.Error()})
		return
	}
	if err := validatePositiveUint("ID команды", input.TeamID); err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": err.Error()})
		return
	}

	if err := h.gameService.DisqualifyTeam(c.Request.Context(), uint(gameID), input.TeamID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/monitor")
}

// ----- Соавторы -----

// ManageCoAuthors отображает страницу управления соавторами.
func (h *GameHandler) ManageCoAuthors(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}

	coAuthors, err := h.coAuthorService.List(uint(gameID))
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.ManageCoAuthors: failed to list coauthors")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	render.Page(c, http.StatusOK, "co_authors-manage.html", gin.H{
		"GameID":    gameID,
		"CoAuthors": coAuthors,
		"csrf":      csrf.GetToken(c),
	})
}

// AddCoAuthor добавляет соавтора.
func (h *GameHandler) AddCoAuthor(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	ownerID := c.GetUint("userID")

	var input AddCoAuthorInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверные данные: " + err.Error()})
		return
	}
	if err := validatePositiveUint("ID пользователя", input.UserID); err != nil {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": err.Error()})
		return
	}

	if err := h.coAuthorService.Add(uint(gameID), input.UserID, ownerID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/co-authors")
}

// RemoveCoAuthor удаляет соавтора.
func (h *GameHandler) RemoveCoAuthor(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || userID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID пользователя"})
		return
	}
	ownerID := c.GetUint("userID")

	if err := h.coAuthorService.Remove(uint(gameID), uint(userID), ownerID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/co-authors")
}

// ----- Заметки автора -----

// Notes отображает заметки к игре (JSON API).
func (h *GameHandler) Notes(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")
	notes, err := h.noteService.ListByGame(uint(gameID), userID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"notes": notes})
}

// CreateNote создаёт новую заметку.
func (h *GameHandler) CreateNote(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")
	var input struct {
		LevelID *uint  `json:"level_id"`
		Text    string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateString("Текст заметки", input.Text, 1, 1000); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	input.Text = sanitize.StripHTML(input.Text)

	note, err := h.noteService.Create(uint(gameID), input.LevelID, userID, input.Text)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"note": note})
}

// DeleteNote удаляет заметку.
func (h *GameHandler) DeleteNote(c *gin.Context) {
	noteID, err := strconv.Atoi(c.Param("note_id"))
	if err != nil || noteID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID заметки"})
		return
	}
	userID := c.GetUint("userID")
	if err := h.noteService.Delete(uint(noteID), userID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ----- Симуляция -----

// Simulate запускает симуляцию прохождения игры.
func (h *GameHandler) Simulate(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")
	result, err := h.simulateService.Simulate(uint(gameID), userID)
	if err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
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
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.SettingsPage: failed to get game")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}

	var settings GameSetting
	if err := h.db.Where("game_id = ?", g.ID).First(&settings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			settings = GameSetting{
				GameID:                   g.ID,
				AllowHints:               true,
				HintPenaltySeconds:       300,
				MaxHints:                 3,
				PerLevelTimeLimit:        0,
				HideAnswersUntilFinished: false,
				AutoStart:                false,
			}
		} else {
			log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.SettingsPage: failed to get settings")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
			return
		}
	}

	render.Page(c, http.StatusOK, "games-settings.html", gin.H{
		"Game":     g,
		"Settings": settings,
		"csrf":     csrf.GetToken(c),
	})
}

// SaveSettings сохраняет настройки игры.
func (h *GameHandler) SaveSettings(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	var settings GameSetting
	if err := c.ShouldBind(&settings); err != nil {
		g, _ := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
		render.Page(c, http.StatusBadRequest, "games-settings.html", gin.H{
			"Game":     g,
			"Settings": settings,
			"Error":    "Неверные данные: " + err.Error(),
			"csrf":     csrf.GetToken(c),
		})
		return
	}

	if settings.HintPenaltySeconds < 0 {
		settings.HintPenaltySeconds = 0
	}
	if settings.MaxHints < 0 {
		settings.MaxHints = 0
	}
	if settings.PerLevelTimeLimit < 0 {
		settings.PerLevelTimeLimit = 0
	}
	if settings.PerLevelTimeLimit > 3600 {
		render.Page(c, http.StatusBadRequest, "games-settings.html", gin.H{
			"Game":     nil,
			"Settings": settings,
			"Error":    "Лимит времени на уровень не может превышать 3600 минут",
			"csrf":     csrf.GetToken(c),
		})
		return
	}

	g, err := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	isManager, err := h.coAuthorService.IsUserManager(g.ID, userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.SaveSettings: failed to check manager")
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	if !isManager {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	settings.GameID = g.ID
	if err := h.db.Save(&settings).Error; err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.SaveSettings: failed to save settings")
		render.Page(c, http.StatusInternalServerError, "games-settings.html", gin.H{
			"Game":     g,
			"Settings": settings,
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
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.HTML(http.StatusNotFound, "errors/404.html", nil)
		} else {
			log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.TestPage: failed to get game")
			c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		}
		return
	}

	var testPassings []GamePassing
	if err := h.db.Where("game_id = ? AND status = ?", g.ID, StatusTesting).Find(&testPassings).Error; err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.TestPage: failed to list test passings")
	}

	render.Page(c, http.StatusOK, "games-test.html", gin.H{
		"Game":         g,
		"TestPassings": testPassings,
		"csrf":         csrf.GetToken(c),
	})
}

// PhotosPage отображает страницу фотогалереи.
func (h *GameHandler) PhotosPage(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	file, header, err := c.Request.FormFile("photo")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Файл не выбран"})
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > 10*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Размер файла не должен превышать 10 МБ"})
		return
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
	contentType := header.Header.Get("Content-Type")
	if !slices.Contains(allowedTypes, contentType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Допустимы только JPEG, PNG и WebP"})
		return
	}

	webPath, err := h.storage.Save("uploads/photos", file, header.Filename, userID, 10*1024*1024, allowedTypes)
	if err != nil {
		log.Error().Err(err).Str("filename", header.Filename).Msg("UploadPhoto: failed to save file")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	photo := &Photo{
		GameID: uint(gameID),
		UserID: userID,
		Path:   webPath,
	}
	if err := h.photoService.Create(photo); err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("UploadPhoto: failed to create photo record")
		_ = h.storage.Delete(webPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Не удалось сохранить фото"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "photo": photo})
}

// DeletePhoto удаляет фото из галереи.
func (h *GameHandler) DeletePhoto(c *gin.Context) {
	photoID, err := strconv.Atoi(c.Param("photo_id"))
	if err != nil || photoID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID фото"})
		return
	}
	userID := c.GetUint("userID")

	var photo Photo
	if err := h.db.First(&photo, photoID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Фото не найдено"})
		} else {
			log.Error().Err(err).Int("photo_id", photoID).Msg("DeletePhoto: failed to get photo")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Внутренняя ошибка"})
		}
		return
	}

	isOwner := photo.UserID == userID
	isManager, err := h.coAuthorService.IsUserManager(photo.GameID, userID)
	if err != nil {
		log.Error().Err(err).Int("photo_id", photoID).Msg("DeletePhoto: failed to check manager")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Внутренняя ошибка"})
		return
	}

	if !isOwner && !isManager {
		c.JSON(http.StatusForbidden, gin.H{"error": "Нет прав на удаление"})
		return
	}

	if err := h.photoService.Delete(photo.ID, userID); err != nil {
		log.Error().Err(err).Uint("photo_id", photo.ID).Msg("DeletePhoto: failed to delete record")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Не удалось удалить фото"})
		return
	}

	if err := h.storage.Delete(photo.Path); err != nil {
		log.Error().Err(err).Str("path", photo.Path).Msg("DeletePhoto: failed to delete file")
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// FullPreview возвращает структуру игры для быстрого просмотра.
func (h *GameHandler) FullPreview(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	_, err = h.gameService.GetByID(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Нет доступа"})
		return
	}

	var levels []level.Level
	if err := h.db.Preload("Questions.Answers").Where("game_id = ?", gameID).Order("position ASC").Find(&levels).Error; err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("FullPreview: failed to load levels")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Не удалось загрузить уровни"})
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

// ---------- Игровой процесс (бывший GameplayHandler) ----------

type GameplayHandler struct {
	gameService    *GameService
	attemptService *AttemptService
	progressSvc    *LevelProgressService
	monitorService *MonitorService
	hub            *ws.RoomHub
	storage        storage.FileStorage
	db             *gorm.DB
}

func NewGameplayHandler(
	gameService *GameService,
	attemptSvc *AttemptService,
	progressSvc *LevelProgressService,
	monitorSvc *MonitorService,
	hub *ws.RoomHub,
	store storage.FileStorage,
	db *gorm.DB,
) *GameplayHandler {
	return &GameplayHandler{
		gameService:    gameService,
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

	var input SubmitCodeInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	code := strings.TrimSpace(input.Code)
	if err := validateString("Код", code, 1, 10000); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	attempt, err := h.gameService.SubmitCode(c.Request.Context(), uint(passingID), userID, code)
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
	if err := h.gameService.UseHint(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
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

	_, err = h.gameService.SubmitFile(c.Request.Context(), uint(passingID), userID, webPath)
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

// ---------- Тестовое прохождение ----------

// StartTesting инициирует тестовое прохождение.
func (h *GameplayHandler) StartTesting(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID игры"})
		return
	}
	userID := c.GetUint("userID")

	passing, err := h.gameService.StartTesting(c.Request.Context(), uint(gameID), userID)
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

	var input SubmitTestCodeInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-test.html", gin.H{
			"Error": "Неверные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	code := strings.TrimSpace(input.Code)
	if err := validateString("Код", code, 1, 10000); err != nil {
		render.Page(c, http.StatusBadRequest, "gameplay-test.html", gin.H{
			"Error": err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if _, err := h.gameService.SubmitTestCode(c.Request.Context(), uint(passingID), c.GetUint("userID"), code); err != nil {
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
	if err := h.gameService.SkipLevelTest(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+c.Param("passing_id"))
}

// ---------- Ручное подтверждение автором ----------

// AcceptAnswer принимает ответ.
func (h *GameplayHandler) AcceptAnswer(c *gin.Context) {
	passingID, err := strconv.Atoi(c.Param("passing_id"))
	if err != nil || passingID <= 0 {
		c.HTML(http.StatusBadRequest, "errors/400.html", gin.H{"Error": "Неверный ID прохождения"})
		return
	}
	if err := h.gameService.AcceptBlackboxAnswer(c.Request.Context(), uint(passingID), c.GetUint("userID")); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Query("game_id")+"/monitor")
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
