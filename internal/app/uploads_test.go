// internal/app/uploads_test.go
package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/testutil"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNormalizeUploadPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"обычный путь", "/avatars/1_123.jpg", "avatars/1_123.jpg"},
		{"без ведущего слэша", "avatars/1.jpg", "avatars/1.jpg"},
		{"вложенная директория", "/photos/sub/1.jpg", "photos/sub/1.jpg"},
		{"пустая строка", "", ""},
		{"только слэш", "/", ""},
		{"точка", ".", ""},
		{"parent traversal", "/../etc/passwd", ""},
		{"parent traversal с префиксом", "uploads/../etc/passwd", ""},
		{"абсолютный за пределами", "/etc/passwd", "etc/passwd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeUploadPath(tt.in))
		})
	}

	// Windows-absolute пути (C:/...) отклоняются, если ОС считает их абсолютными.
	if filepath.IsAbs("C:/Users/secret") {
		assert.Equal(t, "", normalizeUploadPath("C:/Users/secret"))
	}
}

// uploadsTestData создаёт данные для интеграционных тестов раздачи файлов.
type uploadsTestData struct {
	dir             string
	publicGID       uint
	privateGID      uint
	privateAuthorID uint
	publicAuthorID  uint
	memberID        uint
	strangerID      uint
}

func setupUploadsTestData(t *testing.T, db *gorm.DB) *uploadsTestData {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"avatars", "covers", "photos", "answers"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0755))
	}
	// Реальные файлы на диске (c.File проверяет существование).
	avatar := filepath.Join(dir, "avatars", "1_123.jpg")
	coverPub := filepath.Join(dir, "covers", "2_456.jpg")
	coverPriv := filepath.Join(dir, "covers", "3_789.jpg")
	answer := filepath.Join(dir, "answers", "4_111.txt")
	for _, f := range []string{avatar, coverPub, coverPriv, answer} {
		require.NoError(t, os.WriteFile(f, []byte("x"), 0644))
	}

	author := user.User{Email: "uploads-author@test.com", Password: "pass", Name: "A", Role: "user"}
	require.NoError(t, db.Create(&author).Error)
	owner := user.User{Email: "uploads-owner@test.com", Password: "pass", Name: "O", Role: "user"}
	require.NoError(t, db.Create(&owner).Error)
	member := user.User{Email: "uploads-member@test.com", Password: "pass", Name: "M", Role: "user"}
	require.NoError(t, db.Create(&member).Error)
	stranger := user.User{Email: "uploads-stranger@test.com", Password: "pass", Name: "S", Role: "user"}
	require.NoError(t, db.Create(&stranger).Error)

	publicG := game.Game{Name: "Public", AuthorID: author.ID, Visibility: "public", CoverPath: "/uploads/covers/2_456.jpg"}
	require.NoError(t, db.Create(&publicG).Error)
	privateG := game.Game{Name: "Private", AuthorID: owner.ID, Visibility: "private", CoverPath: "/uploads/covers/3_789.jpg"}
	require.NoError(t, db.Create(&privateG).Error)

	// Аватар привязываем к автору (user.AvatarPath).
	author.AvatarPath = "/uploads/avatars/1_123.jpg"
	require.NoError(t, db.Save(&author).Error)

	tm := team.Team{Name: "Uploads Team", CaptainID: member.ID}
	require.NoError(t, db.Create(&tm).Error)
	require.NoError(t, db.Exec("INSERT INTO team_members (team_id, user_id) VALUES (?, ?)", tm.ID, member.ID).Error)

	passing := game.GamePassing{GameID: publicG.ID, TeamID: tm.ID, Status: game.StatusStarted}
	require.NoError(t, db.Create(&passing).Error)
	lvl := level.Level{GameID: publicG.ID, Name: "L1", Position: 1}
	require.NoError(t, db.Create(&lvl).Error)
	progress := game.LevelProgress{GamePassingID: passing.ID, LevelID: lvl.ID}
	require.NoError(t, db.Create(&progress).Error)
	require.NoError(t, db.Create(&game.Attempt{
		LevelProgressID: progress.ID,
		IsFile:          true,
		FilePath:        "/uploads/answers/4_111.txt",
		Success:         false,
	}).Error)

	return &uploadsTestData{dir: dir, publicGID: publicG.ID, privateGID: privateG.ID, privateAuthorID: owner.ID, publicAuthorID: author.ID, memberID: member.ID, strangerID: stranger.ID}
}

func TestUploads_AccessControl(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&user.User{}, &team.Team{}, &game.Game{}, &game.GamePassing{},
		&game.LevelProgress{}, &game.Attempt{}, &level.Level{}, &game.CoAuthor{},
	)
	data := setupUploadsTestData(t, db)

	h := newUploadsHandler(db, data.dir)
	gin.SetMode(gin.TestMode)

	mkReq := func(path string, userID uint, role string) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/uploads/*filepath", func(c *gin.Context) {
			c.Set("userID", userID)
			c.Set("role", role)
			h.Serve(c)
		})
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Аватар — публично.
	assert.Equal(t, http.StatusOK, mkReq("/uploads/avatars/1_123.jpg", 0, "").Code)
	// Cover публичной игры — публично.
	assert.Equal(t, http.StatusOK, mkReq("/uploads/covers/2_456.jpg", 0, "").Code)
	// Cover private игры — анониму 404.
	assert.Equal(t, http.StatusNotFound, mkReq("/uploads/covers/3_789.jpg", 0, "").Code)
	// Cover private игры — автору 200.
	assert.Equal(t, http.StatusOK, mkReq("/uploads/covers/3_789.jpg", data.privateAuthorID, "").Code)
	// Answer — анониму 404.
	assert.Equal(t, http.StatusNotFound, mkReq("/uploads/answers/4_111.txt", 0, "").Code)
	// Answer — участнику команды 200.
	assert.Equal(t, http.StatusOK, mkReq("/uploads/answers/4_111.txt", data.memberID, "").Code)
	// Answer — постороннему пользователю 404.
	assert.Equal(t, http.StatusNotFound, mkReq("/uploads/answers/4_111.txt", data.strangerID, "").Code)
	// Answer — автору игры 200.
	assert.Equal(t, http.StatusOK, mkReq("/uploads/answers/4_111.txt", data.publicAuthorID, "").Code)
	// Path traversal — 404.
	assert.Equal(t, http.StatusNotFound, mkReq("/uploads/../etc/passwd", 0, "").Code)
	// Несуществующая категория — 404.
	assert.Equal(t, http.StatusNotFound, mkReq("/uploads/unknown/1.jpg", 0, "").Code)
}
