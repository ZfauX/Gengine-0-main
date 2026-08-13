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
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/testutil"

	csrf "gengine-0/internal/pkg/csrf"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// =============================================================================
// Р вЂ™РЎРѓР С—Р С•Р СР С•Р С–Р В°РЎвЂљР ВµР В»РЎРЉР Р…РЎвЂ№Р Вµ РЎвЂћРЎС“Р Р…Р С”РЎвЂ Р С‘Р С‘ Р Т‘Р В»РЎРЏ Р Р…Р В°РЎРѓРЎвЂљРЎР‚Р С•Р в„–Р С”Р С‘ РЎвЂљР ВµРЎРѓРЎвЂљР С•Р Р†
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
// Р СћР ВµРЎРѓРЎвЂљРЎвЂ№ Р Т‘Р В»РЎРЏ BackupService
// =============================================================================

func TestBackupService_CreateNow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (requires pg_dump)")
	}
	// Р СџРЎР‚Р С•Р Р†Р ВµРЎР‚РЎРЏР ВµР С Р Р…Р В°Р В»Р С‘РЎвЂЎР С‘Р Вµ pg_dump Р Р† РЎРѓР С‘РЎРѓРЎвЂљР ВµР СР Вµ
	_, err := exec.LookPath("pg_dump")
	if err != nil {
		t.Skip("pg_dump not found in PATH, skipping test")
	}

	// Р РЋР С•Р В·Р Т‘Р В°РЎвЂР С Р Р†РЎР‚Р ВµР СР ВµР Р…Р Р…РЎС“РЎР‹ Р Т‘Р С‘РЎР‚Р ВµР С”РЎвЂљР С•РЎР‚Р С‘РЎР‹ Р Т‘Р В»РЎРЏ Р В±РЎРЊР С”Р В°Р С—Р С•Р Р†
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
	svc, svcErr := admin.NewBackupService(backupRepo, tmpDir, 10, dbCfg, "")
	require.NoError(t, svcErr)

	err = svc.CreateNow(context.Background())
	require.NoError(t, err)

	// Р СџРЎР‚Р С•Р Р†Р ВµРЎР‚РЎРЏР ВµР С, РЎвЂЎРЎвЂљР С• РЎвЂћР В°Р в„–Р В» РЎРѓР С•Р В·Р Т‘Р В°Р Р…
	files, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	assert.Len(t, files, 1)

	// Р СџРЎР‚Р С•Р Р†Р ВµРЎР‚РЎРЏР ВµР С, РЎвЂЎРЎвЂљР С• Р В·Р В°Р С—Р С‘РЎРѓРЎРЉ Р Р† Р вЂР вЂќ РЎРѓР С•Р В·Р Т‘Р В°Р Р…Р В°
	var count int64
	db.Model(&admin.Backup{}).Count(&count)
	assert.Equal(t, int64(1), count)

	// Р СџРЎР‚Р С•Р Р†Р ВµРЎР‚РЎРЏР ВµР С, РЎвЂЎРЎвЂљР С• РЎвЂћР В°Р в„–Р В» Р Р…Р Вµ Р С—РЎС“РЎРѓРЎвЂљР С•Р в„–
	info, err := os.Stat(filepath.Join(tmpDir, files[0].Name()))
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

// TestBackupService_Download: РЎРѓР С”Р В°РЎвЂЎР С‘Р Р†Р В°Р Р…Р С‘Р Вµ РЎРѓРЎС“РЎвЂ°Р ВµРЎРѓРЎвЂљР Р†РЎС“РЎР‹РЎвЂ°Р ВµР С–Р С• Р В±РЎРЊР С”Р В°Р С—Р В° Р Р†Р С•Р В·Р Р†РЎР‚Р В°РЎвЂ°Р В°Р ВµРЎвЂљ Р С—РЎС“РЎвЂљРЎРЉ,
// Р Р…Р ВµРЎРѓРЎС“РЎвЂ°Р ВµРЎРѓРЎвЂљР Р†РЎС“РЎР‹РЎвЂ°Р ВµР С–Р С• РІР‚вЂќ Р С•РЎв‚¬Р С‘Р В±Р С”РЎС“ (T-M1).
func TestBackupService_Download(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (requires pg_dump)")
	}
	_, err := exec.LookPath("pg_dump")
	if err != nil {
		t.Skip("pg_dump not found in PATH, skipping test")
	}

	tmpDir, err := os.MkdirTemp("", "backup_download")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	db := setupAdminTestDB(t)
	backupRepo := admin.NewGormBackupRepo(db)
	dbCfg := config.DatabaseConfig{Host: "localhost", Port: "5432", User: "test", Password: "test", Name: "testdb"}
	svc, svcErr := admin.NewBackupService(backupRepo, tmpDir, 10, dbCfg, "")
	require.NoError(t, svcErr)

	require.NoError(t, svc.CreateNow(context.Background()))

	var backup admin.Backup
	require.NoError(t, db.First(&backup).Error)

	path, cleanup, err := svc.Download(context.Background(), backup.ID)
	require.NoError(t, err)
	if cleanup != nil {
		defer cleanup()
	}
	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	assert.Greater(t, info.Size(), int64(0))

	_, _, err = svc.Download(context.Background(), 999999)
	assert.Error(t, err, "Р Р…Р ВµРЎРѓРЎС“РЎвЂ°Р ВµРЎРѓРЎвЂљР Р†РЎС“РЎР‹РЎвЂ°Р С‘Р в„– Р В±РЎРЊР С”Р В°Р С— Р Т‘Р С•Р В»Р В¶Р ВµР Р… Р Т‘Р В°Р Р†Р В°РЎвЂљРЎРЉ Р С•РЎв‚¬Р С‘Р В±Р С”РЎС“")
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
	svc, svcErr := admin.NewBackupService(backupRepo, tmpDir, 3, dbCfg, "")
	require.NoError(t, svcErr)

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

	svc, svcErr := admin.NewBackupService(backupRepo, "/tmp", 7, dbCfg, "")
	require.NoError(t, svcErr)
	assert.Equal(t, 7, svc.GetMaxBackups())

	svc2, svcErr2 := admin.NewBackupService(backupRepo, "/tmp", 0, dbCfg, "")
	require.NoError(t, svcErr2)
	assert.Equal(t, 10, svc2.GetMaxBackups())
}

// =============================================================================
// Р СћР ВµРЎРѓРЎвЂљРЎвЂ№ Р Т‘Р В»РЎРЏ AdminHandler (РЎвЂљР С•Р В»РЎРЉР С”Р С• РЎР‚Р ВµР Т‘Р С‘РЎР‚Р ВµР С”РЎвЂљРЎвЂ№, Р В±Р ВµР В· HTML-РЎР‚Р ВµР Р…Р Т‘Р ВµРЎР‚Р С‘Р Р…Р С–Р В°)
// =============================================================================

// setupAdminHandlerForRedirect РЎРѓР С•Р В·Р Т‘Р В°РЎвЂРЎвЂљ РЎР‚Р С•РЎС“РЎвЂљР ВµРЎР‚ РЎРѓ Р СР С‘Р Р…Р С‘Р СР В°Р В»РЎРЉР Р…РЎвЂ№Р СР С‘ Р Р…Р В°РЎРѓРЎвЂљРЎР‚Р С•Р в„–Р С”Р В°Р СР С‘,
// Р Т‘Р С•РЎРѓРЎвЂљР В°РЎвЂљР С•РЎвЂЎР Р…РЎвЂ№Р СР С‘ Р Т‘Р В»РЎРЏ РЎвЂљР ВµРЎРѓРЎвЂљР С‘РЎР‚Р С•Р Р†Р В°Р Р…Р С‘РЎРЏ Р Т‘Р ВµР в„–РЎРѓРЎвЂљР Р†Р С‘Р в„–, Р Р†Р С•Р В·Р Р†РЎР‚Р В°РЎвЂ°Р В°РЎР‹РЎвЂ°Р С‘РЎвЂ¦ РЎР‚Р ВµР Т‘Р С‘РЎР‚Р ВµР С”РЎвЂљ.
func setupAdminHandlerForRedirect(t *testing.T) (*gin.Engine, *gorm.DB, *admin.AdminHandler) {
	gin.SetMode(gin.TestMode)
	db := setupAdminTestDB(t)

	adminUser := createTestUser(t, db, "admin@test.com", "adminpass", "Admin", "admin")
	_ = createTestUser(t, db, "user@test.com", "userpass", "User", "user")

	createTestGame(t, db, adminUser.ID, "Game Draft", true)
	createTestGame(t, db, adminUser.ID, "Game Published", false)

	backupRepo := admin.NewGormBackupRepo(db)
	dbCfg := config.DatabaseConfig{Host: "localhost", Port: "5432", User: "test", Password: "test", Name: "testdb"}
	backupService, bsErr := admin.NewBackupService(backupRepo, "/tmp", 10, dbCfg, "")
	require.NoError(t, bsErr)

	auditService := audit.NewService(db)

	userRepo := user.NewGormUserRepo(db)
	gameRepo := game.NewGormGameRepo(db)
	refreshTokenRepo := user.NewGormRefreshTokenRepo(db)

	handler := admin.NewAdminHandler(userRepo, gameRepo, nil, team.NewGormTeamRepo(db), backupService, auditService, refreshTokenRepo, nil)

	r := gin.New()

	// Р РЋР ВµРЎРѓРЎРѓР С‘РЎРЏ Р С‘ CSRF (Р Р…Р ВµР С•Р В±РЎвЂ¦Р С•Р Т‘Р С‘Р СРЎвЂ№ Р Т‘Р В»РЎРЏ csrf.GetToken, Р Т‘Р В°Р В¶Р Вµ Р ВµРЎРѓР В»Р С‘ Р С•РЎвЂљР Р†Р ВµРЎвЂљ РІР‚вЂќ РЎР‚Р ВµР Т‘Р С‘РЎР‚Р ВµР С”РЎвЂљ)
	sessionSecret := "test-admin-secret-key-32chr!!"
	store := cookie.NewStore([]byte(sessionSecret))
	r.Use(sessions.Sessions("gengine_test_session", store))
	r.Use(csrf.Middleware(sessionSecret, false, nil))

	// Р В­Р СРЎС“Р В»Р С‘РЎР‚РЎС“Р ВµР С Р В°Р Р†РЎвЂљР С•РЎР‚Р С‘Р В·Р В°РЎвЂ Р С‘РЎР‹
	r.Use(func(c *gin.Context) {
		c.Set("userID", adminUser.ID)
		c.Set("IsAdmin", true)
		c.Next()
	})

	adminGroup := r.Group("/admin")
	{
		// Р В Р ВµР С–Р С‘РЎРѓРЎвЂљРЎР‚Р С‘РЎР‚РЎС“Р ВµР С РЎвЂљР С•Р В»РЎРЉР С”Р С• РЎвЂљР Вµ Р СР В°РЎР‚РЎв‚¬РЎР‚РЎС“РЎвЂљРЎвЂ№, Р С”Р С•РЎвЂљР С•РЎР‚РЎвЂ№Р Вµ Р Р…РЎС“Р В¶Р Р…РЎвЂ№ Р Т‘Р В»РЎРЏ РЎР‚Р ВµР Т‘Р С‘РЎР‚Р ВµР С”РЎвЂљР С•Р Р†
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
		require.NoError(t, backupRepo.Create(context.Background(), b))
	}

	// Р РЋР С•Р В·Р Т‘Р В°РЎвЂР С Р Р…Р С•Р Р†РЎвЂ№Р в„– РЎРѓР ВµРЎР‚Р Р†Р С‘РЎРѓ РЎРѓ Р С•Р С–РЎР‚Р В°Р Р…Р С‘РЎвЂЎР ВµР Р…Р С‘Р ВµР С Р Р† 3 Р В±Р ВµР С”Р В°Р С—Р В°
	dbCfg := config.DatabaseConfig{Host: "localhost", Port: "5432", User: "test", Password: "test", Name: "testdb"}
	backupService, bsErr := admin.NewBackupService(backupRepo, tmpDir, 3, dbCfg, "")
	require.NoError(t, bsErr)

	// Р С›Р В±РЎР‚Р В°Р В±Р С•РЎвЂљРЎвЂЎР С‘Р С” РЎРѓ РЎРЊРЎвЂљР С‘Р С РЎРѓР ВµРЎР‚Р Р†Р С‘РЎРѓР С•Р С
	userRepo := user.NewGormUserRepo(db)
	gameRepo := game.NewGormGameRepo(db)
	auditSvc := audit.NewService(db)
	refreshTokenRepo := user.NewGormRefreshTokenRepo(db)
	handler := admin.NewAdminHandler(userRepo, gameRepo, nil, team.NewGormTeamRepo(db), backupService, auditSvc, refreshTokenRepo, nil)

	// Р СњР С•Р Р†РЎвЂ№Р в„– РЎР‚Р С•РЎС“РЎвЂљР ВµРЎР‚ Р Т‘Р В»РЎРЏ РЎвЂљР ВµРЎРѓРЎвЂљР В°, Р В±Р ВµР В· CSRF (RotateBackups Р Р…Р Вµ Р С‘РЎРѓР С—Р С•Р В»РЎРЉР В·РЎС“Р ВµРЎвЂљ csrf.GetToken)
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
// Р СћР ВµРЎРѓРЎвЂљРЎвЂ№ Р Т‘Р В»РЎРЏ audit.Service
// =============================================================================

func TestAuditService_LogAndList(t *testing.T) {
	db := setupAdminTestDB(t)
	svc := audit.NewService(db)
	ctx := context.Background()

	svc.Log(1, "test_action", "test", 1, "test details")

	logs, total, err := svc.List(ctx, "", "", "", 1, 10)
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

	logs, total, err := svc.List(ctx, strconv.FormatUint(uint64(1), 10), "", "", 1, 10)
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

	logs, total, err := svc.List(ctx, "", "login", "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, logs, 1)
	assert.Equal(t, "login", logs[0].Action)
}
