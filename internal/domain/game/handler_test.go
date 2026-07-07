// internal/domain/game/handler_test.go
package game

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/templatefuncs"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gorm.io/gorm"
)

// =============================================================================
// МОКИ
// =============================================================================

// MockGameService — мок для GameServiceInterface
type MockGameService struct {
	mock.Mock
}

func (m *MockGameService) CreateGameWithCover(ctx context.Context, dto *CreateGameDTO, authorID uint) (*Game, error) {
	args := m.Called(ctx, dto, authorID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Game), args.Error(1)
}

func (m *MockGameService) UpdateGameWithCover(ctx context.Context, gameID uint, dto *UpdateGameDTO, userID uint) error {
	args := m.Called(ctx, gameID, dto, userID)
	return args.Error(0)
}

func (m *MockGameService) GetByID(ctx context.Context, id uint, viewerID uint) (*Game, error) {
	args := m.Called(ctx, id, viewerID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Game), args.Error(1)
}

func (m *MockGameService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	args := m.Called(ctx, filter, sort, page, perPage)
	return args.Get(0).([]Game), args.Get(1).(int64), args.Error(2)
}

func (m *MockGameService) Delete(ctx context.Context, id uint, userID uint) error {
	args := m.Called(ctx, id, userID)
	return args.Error(0)
}

func (m *MockGameService) Publish(ctx context.Context, id uint, userID uint) error {
	args := m.Called(ctx, id, userID)
	return args.Error(0)
}

func (m *MockGameService) ForceFinishGame(ctx context.Context, gameID uint) error {
	args := m.Called(ctx, gameID)
	return args.Error(0)
}

func (m *MockGameService) DisqualifyTeam(ctx context.Context, gameID, teamID uint) error {
	args := m.Called(ctx, gameID, teamID)
	return args.Error(0)
}

func (m *MockGameService) SubmitCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error) {
	args := m.Called(ctx, passingID, userID, code)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Attempt), args.Error(1)
}

func (m *MockGameService) SubmitFile(ctx context.Context, passingID, userID uint, filePath string) (*Attempt, error) {
	args := m.Called(ctx, passingID, userID, filePath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Attempt), args.Error(1)
}

func (m *MockGameService) UseHint(ctx context.Context, passingID, userID uint) error {
	args := m.Called(ctx, passingID, userID)
	return args.Error(0)
}

func (m *MockGameService) AcceptBlackboxAnswer(ctx context.Context, passingID, userID uint) error {
	args := m.Called(ctx, passingID, userID)
	return args.Error(0)
}

func (m *MockGameService) StartTesting(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	args := m.Called(ctx, gameID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*GamePassing), args.Error(1)
}

func (m *MockGameService) SubmitTestCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error) {
	args := m.Called(ctx, passingID, userID, code)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Attempt), args.Error(1)
}

func (m *MockGameService) SkipLevelTest(ctx context.Context, passingID, userID uint) error {
	args := m.Called(ctx, passingID, userID)
	return args.Error(0)
}

func (m *MockGameService) ListReviews(ctx context.Context, gameID uint) ([]Review, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]Review), args.Error(1)
}

func (m *MockGameService) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	args := m.Called(ctx, gameID)
	return args.Get(0).(float64), args.Get(1).(int64), args.Error(2)
}

// MockCoAuthorService — мок для CoAuthorServiceInterface
type MockCoAuthorService struct {
	mock.Mock
}

func (m *MockCoAuthorService) IsUserManager(gameID, userID uint) (bool, error) {
	args := m.Called(gameID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *MockCoAuthorService) HasPermission(gameID, userID uint, requiredRole string) (bool, error) {
	args := m.Called(gameID, userID, requiredRole)
	return args.Bool(0), args.Error(1)
}

func (m *MockCoAuthorService) CanModerateGame(gameID, userID uint) (bool, error) {
	args := m.Called(gameID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *MockCoAuthorService) CanEditContent(gameID, userID uint) (bool, error) {
	args := m.Called(gameID, userID)
	return args.Bool(0), args.Error(1)
}

func (m *MockCoAuthorService) Add(gameID, newCoAuthorID, ownerID uint) error {
	args := m.Called(gameID, newCoAuthorID, ownerID)
	return args.Error(0)
}

func (m *MockCoAuthorService) Remove(gameID, coAuthorUserID, ownerID uint) error {
	args := m.Called(gameID, coAuthorUserID, ownerID)
	return args.Error(0)
}

func (m *MockCoAuthorService) List(gameID uint) ([]CoAuthor, error) {
	args := m.Called(gameID)
	return args.Get(0).([]CoAuthor), args.Error(1)
}

// MockAuditService — мок для AuditServiceInterface
type MockAuditService struct {
	mock.Mock
}

func (m *MockAuditService) Log(userID uint, action, objectType string, objectID uint, details string) {
	m.Called(userID, action, objectType, objectID, details)
}

// MockStorage — мок для storage.FileStorage
type MockStorage struct {
	mock.Mock
}

func (m *MockStorage) Save(baseDir string, reader io.Reader, originalName string, userID uint, maxSize int64, allowedMIMETypes []string) (string, error) {
	args := m.Called(baseDir, reader, originalName, userID, maxSize, allowedMIMETypes)
	return args.String(0), args.Error(1)
}

func (m *MockStorage) Delete(webPath string) error {
	args := m.Called(webPath)
	return args.Error(0)
}

// MockGamePassingService — мок для GamePassingServiceInterface
type MockGamePassingService struct {
	mock.Mock
}

func (m *MockGamePassingService) Apply(ctx context.Context, gameID, teamID, userID uint) error {
	args := m.Called(ctx, gameID, teamID, userID)
	return args.Error(0)
}

func (m *MockGamePassingService) ListByGame(ctx context.Context, gameID uint) ([]GamePassing, error) {
	args := m.Called(ctx, gameID)
	return args.Get(0).([]GamePassing), args.Error(1)
}

func (m *MockGamePassingService) UpdateStatus(ctx context.Context, passingID uint, status GamePassingStatus, userID uint) error {
	args := m.Called(ctx, passingID, status, userID)
	return args.Error(0)
}

func (m *MockGamePassingService) StartGame(ctx context.Context, passingID, userID uint) error {
	args := m.Called(ctx, passingID, userID)
	return args.Error(0)
}

func (m *MockGamePassingService) GetTeamsByCaptain(ctx context.Context, userID uint) ([]team.Team, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]team.Team), args.Error(1)
}

// =============================================================================
// ВСПОМОГАТЕЛЬНЫЕ ФУНКЦИИ
// =============================================================================

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)

	// Инициализируем минимальный набор шаблонов для тестов
	tmpl := template.New("layout.html")
	tmpl.Funcs(templatefuncs.FuncMap())
	// layout.html
	tmpl, err := tmpl.Parse(`{{define "layout.html"}}<html><body>{{.ContentHTML}}</body></html>{{end}}`)
	if err != nil {
		panic(err)
	}
	// Определяем все шаблоны, которые используются в хендлерах
	templates := []string{
		"games-list.html", "games-show.html", "games-new.html", "games-edit.html",
		"game_passings-list.html", "game_passings-apply.html",
		"co_authors-manage.html", "games-settings.html", "games-test.html",
		"games-photos.html", "simulate-results.html", "gameplay-show.html",
		"gameplay-test.html",
		"errors/500.html", "errors/404.html", "errors/400.html", "errors/403.html",
	}
	for _, t := range templates {
		tmpl, err = tmpl.Parse(`{{define "` + t + `"}}<h1>` + t + `</h1>{{end}}`)
		if err != nil {
			panic(err)
		}
	}
	render.SetTemplate(tmpl)

	router := gin.New()
	router.SetHTMLTemplate(tmpl)

	// Добавляем сессии
	store := cookie.NewStore([]byte("test-session-secret-32chars-long!!!"))
	router.Use(sessions.Sessions("gengine_test_session", store))

	// Вместо полноценной CSRF-защиты добавляем заглушку, которая устанавливает фиктивные значения
	// Это позволяет хендлерам использовать csrf.GetToken без ошибок
	router.Use(func(c *gin.Context) {
		c.Set("csrfSecret", "test-csrf-secret-32chars-long!!!")
		c.Set("csrfToken", "test-csrf-token")
		c.Next()
	})

	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Next()
	})
	return router
}

func createTestGame(id uint, name string) *Game {
	return &Game{
		Model:    gorm.Model{ID: id},
		Name:     name,
		IsDraft:  false,
		AuthorID: 1,
	}
}

// =============================================================================
// ТЕСТЫ
// =============================================================================

func TestGameHandler_List_Success(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	router.GET("/games", handler.List)

	expectedGames := []Game{*createTestGame(1, "Game 1"), *createTestGame(2, "Game 2")}
	mockGameService.On("ListFilteredPaginated",
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).Return(expectedGames, int64(2), nil)

	req := httptest.NewRequest("GET", "/games", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockGameService.AssertExpectations(t)
}

func TestGameHandler_List_Error(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	router.GET("/games", handler.List)

	mockGameService.On("ListFilteredPaginated",
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).Return([]Game{}, int64(0), errors.New("db error"))

	req := httptest.NewRequest("GET", "/games", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockGameService.AssertExpectations(t)
}

func TestGameHandler_Show_Success(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	router.GET("/games/:id", handler.Show)

	game := createTestGame(1, "Test Game")
	mockGameService.On("GetByID", mock.Anything, uint(1), uint(1)).Return(game, nil)
	mockCoAuthorService.On("IsUserManager", uint(1), uint(1)).Return(true, nil)
	mockGameService.On("ListReviews", mock.Anything, uint(1)).Return([]Review{}, nil)
	mockGameService.On("GetAverageRating", mock.Anything, uint(1)).Return(0.0, int64(0), nil)

	req := httptest.NewRequest("GET", "/games/1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockGameService.AssertExpectations(t)
	mockCoAuthorService.AssertExpectations(t)
}

func TestGameHandler_Show_NotFound(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	router.GET("/games/:id", handler.Show)

	mockGameService.On("GetByID", mock.Anything, uint(1), uint(1)).Return(nil, gorm.ErrRecordNotFound)

	req := httptest.NewRequest("GET", "/games/1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockGameService.AssertExpectations(t)
}

func TestGameHandler_Create_Success(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	router.POST("/games", handler.Create)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("name", "Test Game")
	_ = writer.WriteField("description", "Test Description")
	_ = writer.WriteField("max_team_number", "5")
	_ = writer.WriteField("visibility", "public")
	_ = writer.Close()

	expectedGame := createTestGame(1, "Test Game")
	mockGameService.On("CreateGameWithCover", mock.Anything, mock.Anything, uint(1)).Return(expectedGame, nil)
	mockAuditService.On("Log", uint(1), "create", "game", uint(1), "Test Game").Return()

	req := httptest.NewRequest("POST", "/games", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/games", w.Header().Get("Location"))
	mockGameService.AssertExpectations(t)
	mockAuditService.AssertExpectations(t)
}

func TestGameHandler_Create_BadRequest(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	router.POST("/games", handler.Create)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/games", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockGameService.AssertNotCalled(t, "CreateGameWithCover")
}

func TestGameHandler_Delete_Success(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	router.POST("/games/:id/delete", handler.Delete)

	mockGameService.On("Delete", mock.Anything, uint(1), uint(1)).Return(nil)
	mockAuditService.On("Log", uint(1), "delete", "game", uint(1), "").Return()

	req := httptest.NewRequest("POST", "/games/1/delete", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/games", w.Header().Get("Location"))
	mockGameService.AssertExpectations(t)
	mockAuditService.AssertExpectations(t)
}

func TestGameHandler_Delete_Forbidden(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	// Переопределяем userID для этого теста
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(2))
		c.Next()
	})
	router.POST("/games/:id/delete", handler.Delete)

	mockGameService.On("Delete", mock.Anything, uint(1), uint(2)).Return(errors.New("только владелец может удалить игру"))

	req := httptest.NewRequest("POST", "/games/1/delete", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockGameService.AssertExpectations(t)
}

func TestGameHandler_Update_Success(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	router.POST("/games/:id/update", handler.Update)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("name", "Updated Game")
	_ = writer.WriteField("description", "Updated Description")
	_ = writer.WriteField("max_team_number", "10")
	_ = writer.WriteField("visibility", "private")
	_ = writer.Close()

	existingGame := createTestGame(1, "Old Game")
	mockGameService.On("GetByID", mock.Anything, uint(1), uint(1)).Return(existingGame, nil)
	mockGameService.On("UpdateGameWithCover", mock.Anything, uint(1), mock.Anything, uint(1)).Return(nil)
	mockAuditService.On("Log", uint(1), "update", "game", uint(1), "Updated Game").Return()

	req := httptest.NewRequest("POST", "/games/1/update", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/games/1", w.Header().Get("Location"))
	mockGameService.AssertExpectations(t)
	mockAuditService.AssertExpectations(t)
}

func TestGameHandler_Publish_Success(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	router.POST("/games/:id/publish", handler.Publish)

	mockGameService.On("Publish", mock.Anything, uint(1), uint(1)).Return(nil)
	mockAuditService.On("Log", uint(1), "publish", "game", uint(1), "").Return()

	req := httptest.NewRequest("POST", "/games/1/publish", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/games/1", w.Header().Get("Location"))
	mockGameService.AssertExpectations(t)
	mockAuditService.AssertExpectations(t)
}

func TestGameHandler_Publish_Forbidden(t *testing.T) {
	mockGameService := new(MockGameService)
	mockCoAuthorService := new(MockCoAuthorService)
	mockAuditService := new(MockAuditService)
	mockStorage := new(MockStorage)
	mockPassingService := new(MockGamePassingService)

	handler := &GameHandler{
		gameService:     mockGameService,
		passingService:  mockPassingService,
		coAuthorService: mockCoAuthorService,
		auditService:    mockAuditService,
		storage:         mockStorage,
	}

	router := setupTestRouter()
	router.Use(func(c *gin.Context) {
		c.Set("userID", uint(2))
		c.Next()
	})
	router.POST("/games/:id/publish", handler.Publish)

	mockGameService.On("Publish", mock.Anything, uint(1), uint(2)).Return(errors.New("только автор или контент-менеджер может опубликовать игру"))

	req := httptest.NewRequest("POST", "/games/1/publish", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockGameService.AssertExpectations(t)
}
