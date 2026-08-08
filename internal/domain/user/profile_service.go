// internal/domain/user/profile_service.go
package user

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ErrEmailTaken — email уже занят другим пользователем.
var ErrEmailTaken = errors.New("email уже используется другим пользователем")

// UserStats содержит статистику для публичного профиля.
type UserStats struct {
	GamesCreated int64
	GamesPlayed  int64
	Wins         int64
	Rating       int
}

// RecentGame содержит данные о последней игре.
type RecentGame struct {
	ID        uint
	Name      string
	IsDraft   bool
	CoverPath string
	CreatedAt string
}

// ProfileService отвечает за статистику и данные публичного профиля.
// A-2 (pass 35): данные через ProfileRepository, а не raw *gorm.DB.
type ProfileService struct {
	repo ProfileRepository
}

// NewProfileService создаёт новый ProfileService.
func NewProfileService(repo ProfileRepository) *ProfileService {
	return &ProfileService{repo: repo}
}

// GetPublicProfileStats загружает статистику пользователя.
// PF-3 (pass 29): 3 COUNT + rating ранее были 4 round-trip; теперь один
// запрос с агрегатами через подзапросы.
func (s *ProfileService) GetPublicProfileStats(ctx context.Context, userID uint) (*UserStats, error) {
	return s.repo.GetPublicProfileStats(ctx, userID)
}

// IsFollowing проверяет, подписан ли пользователь на другого.
func (s *ProfileService) IsFollowing(ctx context.Context, followerID, authorID uint) (bool, error) {
	return s.repo.IsFollowing(ctx, followerID, authorID)
}

// GetRecentGames загружает последние игры автора.
func (s *ProfileService) GetRecentGames(ctx context.Context, authorID uint) ([]RecentGame, error) {
	return s.repo.GetRecentGames(ctx, authorID)
}

// UpdateProfile обновляет имя и email пользователя.
func (s *ProfileService) UpdateProfile(ctx context.Context, userID uint, name, email string) error {
	return s.repo.UpdateProfile(ctx, userID, name, email)
}

// GetThemeSettings возвращает настройки темы пользователя (с дефолтами при пустой записи).
func (s *ProfileService) GetThemeSettings(ctx context.Context, userID uint) (ThemeSettings, error) {
	return s.repo.GetThemeSettings(ctx, userID)
}

// SaveThemeSettings сохраняет настройки темы пользователя.
func (s *ProfileService) SaveThemeSettings(ctx context.Context, userID uint, ts ThemeSettings) error {
	return s.repo.SaveThemeSettings(ctx, userID, ts)
}

// GetUserThemeSettings загружает настройки темы пользователя из БД.
// Удобная обёртка для middleware (не требует создания сервиса на вызывающей стороне).
func GetUserThemeSettings(ctx context.Context, db *gorm.DB, userID uint) (ThemeSettings, error) {
	return NewProfileService(NewGormProfileRepo(db)).GetThemeSettings(ctx, userID)
}

// GetGamesView возвращает сохранённое предпочтение вида списка игр.
func (s *ProfileService) GetGamesView(ctx context.Context, userID uint) (string, error) {
	return s.repo.GetGamesView(ctx, userID)
}

// SaveGamesView сохраняет предпочтение вида списка игр.
func (s *ProfileService) SaveGamesView(ctx context.Context, userID uint, view string) error {
	return s.repo.SaveGamesView(ctx, userID, view)
}
