// internal/app/uploads.go
package app

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"gengine-0/internal/domain/game"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// uploadsHandler раздаёт файлы из /uploads с проверкой прав доступа.
// SEC2: заменяет r.Static("/uploads"), который отдавал все файлы анониму.
//
// Категории:
//   - avatars/* — публично (аватар пользователя);
//   - covers/*  — проверка видимости игры (private/draft — только менеджер);
//   - photos/*  — проверка видимости игры;
//   - answers/* — файлы ответов команд: только участник команды или менеджер игры.
type uploadsHandler struct {
	db         *gorm.DB
	uploadsDir string
}

func newUploadsHandler(db *gorm.DB, uploadsDir string) *uploadsHandler {
	return &uploadsHandler{db: db, uploadsDir: uploadsDir}
}

// Serve обрабатывает GET /uploads/*filepath.
func (h *uploadsHandler) Serve(c *gin.Context) {
	webPath := c.Param("filepath") // например "/avatars/1_123.jpg"
	if webPath == "" {
		c.Status(http.StatusNotFound)
		return
	}

	// Нормализация + защита от path traversal.
	webPath = normalizeUploadPath(webPath)
	if webPath == "" {
		c.Status(http.StatusNotFound)
		return
	}

	category, rest, found := strings.Cut(webPath, "/")
	if !found || rest == "" {
		c.Status(http.StatusNotFound)
		return
	}

	userID := uint(0)
	if id, ok := c.Get("userID"); ok {
		userID, _ = id.(uint)
	}
	role, _ := c.Get("role")
	userRole, _ := role.(string)

	allowed := true
	switch category {
	case "avatars":
		// публично
	case "covers", "photos":
		canView, err := h.canViewGameFile(c.Request.Context(), category, webPath, userID, userRole)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		allowed = canView
	case "answers":
		canView, err := h.canViewAnswer(c.Request.Context(), webPath, userID, userRole)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		allowed = canView
	default:
		allowed = false
	}

	if !allowed {
		c.Status(http.StatusNotFound)
		return
	}

	fullPath := filepath.Join(h.uploadsDir, filepath.FromSlash(webPath))
	c.File(fullPath)
}

// normalizeUploadPath приводит путь к относительному виду внутри uploads
// ("avatars/1.jpg") и возвращает пустую строку при попытке выйти за пределы.
func normalizeUploadPath(webPath string) string {
	// Запрещаем любой компонент ".." во входящем пути (parent traversal) —
	// проверка ДО filepath.Clean, который бы его сжал и «спрятал».
	if strings.Contains(webPath, "..") {
		return ""
	}
	rel := strings.TrimPrefix(webPath, "/")
	if rel == "" {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	// filepath.IsAbs защищает от абсолютных путей (например, C:/... на Windows),
	// которые иначе оказались бы за пределами uploads после Join.
	if clean == "." || clean == "" || filepath.IsAbs(clean) {
		return ""
	}
	return clean
}

// canViewGameFile определяет, можно ли показывать cover/photo этой игры.
// Публичная не-draft игра видна всем; private/draft — только менеджеру или админу.
func (h *uploadsHandler) canViewGameFile(ctx context.Context, category, webPath string, userID uint, userRole string) (bool, error) {
	var gameID uint
	var err error

	switch category {
	case "covers":
		var g game.Game
		err = h.db.WithContext(ctx).Select("id").Where("cover_path = ?", "/uploads/"+webPath).First(&g).Error
		gameID = g.ID
	case "photos":
		var p game.Photo
		err = h.db.WithContext(ctx).Select("game_id").Where("path = ?", "/uploads/"+webPath).First(&p).Error
		gameID = p.GameID
	}
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if gameID == 0 {
		return false, nil
	}
	return h.canViewGame(ctx, gameID, userID, userRole)
}

// canViewAnswer проверяет доступ к файлу-ответу команды.
// Разрешено: участнику команды (капитан или team_members) или менеджеру игры.
func (h *uploadsHandler) canViewAnswer(ctx context.Context, webPath string, userID uint, userRole string) (bool, error) {
	if userID == 0 {
		return false, nil
	}

	// Файл ответа → Attempt.FilePath → LevelProgress → GamePassing → Game + Team.
	var result struct {
		GameID uint
		TeamID uint
	}
	err := h.db.WithContext(ctx).
		Table("attempts").
		Select("gp.game_id, gp.team_id").
		Joins("JOIN level_progresses lp ON lp.id = attempts.level_progress_id").
		Joins("JOIN game_passings gp ON gp.id = lp.game_passing_id").
		Where("attempts.file_path = ?", "/uploads/"+webPath).
		Scan(&result).Error
	if err != nil {
		return false, err
	}
	if result.GameID == 0 {
		return false, nil
	}

	// Менеджер игры (автор/соавтор) или админ — доступен.
	// ВАЖНО: для answers не используем canViewGame (публичная игра видна всем) —
	// данные решений команды доступны только менеджерам и участникам команды.
	isManager, err := h.isGameManager(ctx, result.GameID, userID, userRole)
	if err != nil {
		return false, err
	}
	if isManager {
		return true, nil
	}

	// Участник команды (капитан или team_members).
	if result.TeamID == 0 {
		return false, nil
	}
	var teamCount int64
	if err := h.db.WithContext(ctx).
		Table("teams").
		Where("id = ? AND captain_id = ?", result.TeamID, userID).
		Count(&teamCount).Error; err != nil {
		return false, err
	}
	if teamCount > 0 {
		return true, nil
	}
	var memberCount int64
	if err := h.db.WithContext(ctx).
		Table("team_members").
		Where("team_id = ? AND user_id = ?", result.TeamID, userID).
		Count(&memberCount).Error; err != nil {
		return false, err
	}
	return memberCount > 0, nil
}

// canViewGame применяет ту же логику, что GameCRUDService.CanViewGame:
// публичная не-draft игра — всем; private/draft — только менеджер или админ.
func (h *uploadsHandler) canViewGame(ctx context.Context, gameID, userID uint, userRole string) (bool, error) {
	var g game.Game
	err := h.db.WithContext(ctx).Select("id, is_draft, visibility, author_id").First(&g, gameID).Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if !g.IsDraft && g.Visibility != "private" {
		return true, nil
	}
	return h.isGameManager(ctx, gameID, userID, userRole)
}

// isGameManager определяет, является ли пользователь автором/соавтором игры
// или админом. Используется для доступа к приватным файлам и данным решений.
func (h *uploadsHandler) isGameManager(ctx context.Context, gameID, userID uint, userRole string) (bool, error) {
	var g game.Game
	err := h.db.WithContext(ctx).Select("author_id").First(&g, gameID).Error
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if g.AuthorID == userID {
		return true, nil
	}
	if userRole == "admin" {
		return true, nil
	}
	var coCount int64
	if err := h.db.WithContext(ctx).
		Table("co_authors").
		Where("game_id = ? AND user_id = ?", gameID, userID).
		Count(&coCount).Error; err != nil {
		return false, err
	}
	return coCount > 0, nil
}
