// internal/domain/admin/service_test.go
package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/admin"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/testutil"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// =============================================================================
// Вспомогательные функции для настройки тестов
// =============================================================================

func setupAdminTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t,
		&user.User{},
		&game.Game{},
		&audit.Entry{},
		&admin.Backup{},
	)
}

func createTestUser(t *testing.T, db *gorm.DB, email, password, name, role string) *user.User {
	t.Helper()
	u := &user.User{
		Email:    email,
		Password: password,
		Name:     name,
		Role:     role,
	}
	require.NoError(t, db.Create(u).Error)
	return u
}

func createTestGame(t *testing.T, db *gorm.DB, authorID uint, name string, isDraft bool) *game.Game {
	t.Helper()
	g := &game.Game{Name: name, AuthorID: authorID, IsDraft: isDraft}
	require.NoError(t, db.Create(g).Error)
	return g
}

// =============================================================================
// Тесты для BackupService
// =============================================================================

func TestBackupService_CreateNow(t *testing.T) {
	// Проверяем наличие pg_dump в системе
	_, err := exec.LookPath("pg_dump")
	if err != nil {
		t.Skip("pg_dump not found in PATH, skipping test")
	}

	// Создаём временную директорию для бэкапов
	tmpDir, err := os.MkdirTemp("", "backup_create")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	db := setupAdminTestDB(t)
	backupRepo := admin.NewGormBackupRepo(db)

	dbCfg := config.DatabaseConfig{
		Host:     "localhost",
		Port:     "5432",
		User:     "test",
		Password: "test",
		Name:     "testdb",
	}
	svc := admin.NewBackupService(backupRepo, tmpDir, 10, dbCfg)

	err = svc.CreateNow(context.Background())
	require.NoError(t, err)

	// Проверяем, что файл создан
	files, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	assert.Len(t, files, 1)

	// Проверяем, что запись в БД создана
	var count int64
	db.Model(&admin.Backup{}).Count(&count)
	assert.Equal(t, int64(1), count)

	// Проверяем, что файл не пустой
	info, err := os.Stat(filepath.Join(tmpDir, files[0].Name()))
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

func TestBackupService_RotateBackups(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "backup_rotate")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	for i := 1; i <= 5; i++ {
		fname := filepath.Join(tmpDir, "backup"+strconv.Itoa(i)+".sql")
		require.NoError(t, os.WriteFile(fname, []byte("dummy"), 0644))
	}

	db := setupAdminTestDB(t)
	backupRepo := admin.NewGormBackupRepo(db)

	now := time.Now()
	for i := 1; i <= 5; i++ {
		b := &admin.Backup{
			FilePath:  filepath.Join(tmpDir, "backup"+strconv.Itoa(i)+".sql"),
			CreatedAt: now.Add(time.Duration(i) * time.Hour),
		}
		require.NoError(t, db.Create(b).Error)
	}

	dbCfg := config.DatabaseConfig{
		Host:     "localhost",
		Port:     "5432",
		User:     "test",
		Password: "test",
		Name:     "testdb",
	}
	svc := admin.NewBackupService(backupRepo, tmpDir, 3, dbCfg)

	err = svc.RotateBackups(context.Background())
	require.NoError(t, err)

	var count int64
	db.Model(&admin.Backup{}).Count(&count)
	assert.Equal(t, int64(3), count)

	files, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	assert.Len(t, files, 3)
}

func TestBackupService_GetMaxBackups(t *testing.T) {
	db := setupAdminTestDB(t)
	backupRepo := admin.NewGormBackupRepo(db)
	dbCfg := config.DatabaseConfig{}

	svc := admin.NewBackupService(backupRepo, "/tmp", 7, dbCfg)
	assert.Equal(t, 7, svc.GetMaxBackups())

	svc2 := admin.NewBackupService(backupRepo, "/tmp", 0, dbCfg)
	assert.Equal(t, 10, svc2.GetMaxBackups())
}

// =============================================================================
// Тесты для AdminHandler (только редиректы, без HTML-рендеринга)
// =============================================================================

// setupAdminHandlerForRedirect создаёт роутер с минимальными настройками,
// достаточными для тестирования действий, возвращающих редирект.
func setupAdminHandlerForRedirect(t *testing.T) (*gin.Engine, *gorm.DB, *admin.AdminHandler) {
	gin.SetMode(gin.TestMode)
	db := setupAdminTestDB(t)

	adminUser := createTestUser(t, db, "admin@test.com", "adminpass", "Admin", "admin")
	_ = createTestUser(t, db, "user@test.com", "userpass", "User", "user")

	createTestGame(t, db, adminUser.ID, "Game Draft", true)
	createTestGame(t, db, adminUser.ID, "Game Published", false)

	backupRepo := admin.NewGormBackupRepo(db)
	dbCfg := config.DatabaseConfig{Host: "localhost", Port: "5432", User: "test", Password: "test", Name: "testdb"}
	backupService := admin.NewBackupService(backupRepo, "/tmp", 10, dbCfg)

	auditService := audit.NewService(db)

	userRepo := user.NewGormUserRepo(db)
	gameRepo := game.NewGormGameRepo(db)

	handler := admin.NewAdminHandler(userRepo, gameRepo, backupService, auditService)

	r := gin.New()

	// Сессия и CSRF (необходимы для csrf.GetToken, даже если ответ — редирект)
	sessionSecret := "test-admin-secret-key-32chr!!"
	store := cookie.NewStore([]byte(sessionSecret))
	r.Use(sessions.Sessions("gengine_test_session", store))
	r.Use(csrf.Middleware(csrf.Options{
		Secret: sessionSecret,
		ErrorFunc: func(c *gin.Context) {
			c.String(403, "CSRF token mismatch")
			c.Abort()
		},
	}))

	// Эмулируем авторизацию
	r.Use(func(c *gin.Context) {
		c.Set("userID", adminUser.ID)
		c.Set("IsAdmin", true)
		c.Next()
	})

	adminGroup := r.Group("/admin")
	{
		// Регистрируем только те маршруты, которые нужны для редиректов
		adminGroup.GET("/users/:id/toggle-admin", handler.ToggleAdmin)
		adminGroup.GET("/users/:id/delete", handler.DeleteUser)
		adminGroup.GET("/games/:id/delete", handler.DeleteGame)
		adminGroup.POST("/backups/rotate", handler.RotateBackups)
	}
	return r, db, handler
}

func TestAdminHandler_ToggleAdmin(t *testing.T) {
	r, db, _ := setupAdminHandlerForRedirect(t)

	var u user.User
	db.Where("email = ?", "user@test.com").First(&u)

	req := httptest.NewRequest("GET", "/admin/users/"+strconv.FormatUint(uint64(u.ID), 10)+"/toggle-admin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/admin/users", w.Header().Get("Location"))

	var updated user.User
	db.First(&updated, u.ID)
	assert.Equal(t, "admin", updated.Role)
}

func TestAdminHandler_DeleteUser(t *testing.T) {
	r, db, _ := setupAdminHandlerForRedirect(t)

	tmpUser := createTestUser(t, db, "temp@test.com", "pass", "Temp", "user")

	req := httptest.NewRequest("GET", "/admin/users/"+strconv.FormatUint(uint64(tmpUser.ID), 10)+"/delete", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/admin/users", w.Header().Get("Location"))

	var count int64
	db.Model(&user.User{}).Where("id = ?", tmpUser.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestAdminHandler_DeleteGame(t *testing.T) {
	r, db, _ := setupAdminHandlerForRedirect(t)

	adminUser := createTestUser(t, db, "admin2@test.com", "pass", "Admin2", "admin")
	g := createTestGame(t, db, adminUser.ID, "ToDelete", false)

	req := httptest.NewRequest("GET", "/admin/games/"+strconv.FormatUint(uint64(g.ID), 10)+"/delete", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/admin/games", w.Header().Get("Location"))

	var count int64
	db.Model(&game.Game{}).Where("id = ?", g.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestAdminHandler_RotateBackups(t *testing.T) {
	_, db, _ := setupAdminHandlerForRedirect(t)

	tmpDir, err := os.MkdirTemp("", "rotate_test")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	backupRepo := admin.NewGormBackupRepo(db)
	for i := 1; i <= 5; i++ {
		fname := filepath.Join(tmpDir, "backup"+strconv.Itoa(i)+".sql")
		require.NoError(t, os.WriteFile(fname, []byte("dummy"), 0644))
		b := &admin.Backup{FilePath: fname, CreatedAt: time.Now().Add(time.Duration(i) * time.Hour)}
		_ = backupRepo.Create(context.Background(), b)
	}

	// Создаём новый сервис с ограничением в 3 бекапа
	dbCfg := config.DatabaseConfig{Host: "localhost", Port: "5432", User: "test", Password: "test", Name: "testdb"}
	backupService := admin.NewBackupService(backupRepo, tmpDir, 3, dbCfg)

	// Обработчик с этим сервисом
	userRepo := user.NewGormUserRepo(db)
	gameRepo := game.NewGormGameRepo(db)
	auditSvc := audit.NewService(db)
	handler := admin.NewAdminHandler(userRepo, gameRepo, backupService, auditSvc)

	// Новый роутер для теста, без CSRF (RotateBackups не использует csrf.GetToken)
	r2 := gin.New()
	sessionSecret := "rotate-test-secret-key-32chr!"
	store := cookie.NewStore([]byte(sessionSecret))
	r2.Use(sessions.Sessions("gengine_rotate", store))
	r2.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Set("IsAdmin", true)
		c.Next()
	})
	r2.POST("/admin/backups/rotate", handler.RotateBackups)

	req := httptest.NewRequest("POST", "/admin/backups/rotate", nil)
	w := httptest.NewRecorder()
	r2.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/admin/backups", w.Header().Get("Location"))

	var count int64
	db.Model(&admin.Backup{}).Count(&count)
	assert.Equal(t, int64(3), count)
}

// =============================================================================
// Тесты для audit.Service
// =============================================================================

func TestAuditService_LogAndList(t *testing.T) {
	db := setupAdminTestDB(t)
	svc := audit.NewService(db)
	ctx := context.Background()

	svc.Log(1, "test_action", "test", 1, "test details")

	logs, total, err := svc.List(ctx, "", "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, logs, 1)
	assert.Equal(t, uint(1), logs[0].UserID)
	assert.Equal(t, "test_action", logs[0].Action)
	assert.Equal(t, "test details", logs[0].Details)
}

func TestAuditService_FilterByUser(t *testing.T) {
	db := setupAdminTestDB(t)
	svc := audit.NewService(db)
	ctx := context.Background()

	svc.Log(1, "a1", "", 0, "")
	svc.Log(2, "a2", "", 0, "")

	logs, total, err := svc.List(ctx, strconv.FormatUint(uint64(1), 10), "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, logs, 1)
	assert.Equal(t, uint(1), logs[0].UserID)
}

func TestAuditService_FilterByAction(t *testing.T) {
	db := setupAdminTestDB(t)
	svc := audit.NewService(db)
	ctx := context.Background()

	svc.Log(1, "login", "", 0, "")
	svc.Log(1, "logout", "", 0, "")

	logs, total, err := svc.List(ctx, "", "login", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, logs, 1)
	assert.Equal(t, "login", logs[0].Action)
}
