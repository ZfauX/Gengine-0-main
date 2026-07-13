// internal/domain/user/profile_service.go
package user

import (
	"context"

	"gorm.io/gorm"
)

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
type ProfileService struct {
	db *gorm.DB
}

// NewProfileService создаёт новый ProfileService.
func NewProfileService(db *gorm.DB) *ProfileService {
	return &ProfileService{db: db}
}

// GetPublicProfileStats загружает статистику пользователя.
func (s *ProfileService) GetPublicProfileStats(ctx context.Context, userID uint) (*UserStats, error) {
	var stats UserStats

	// Игры, созданные пользователем
	if err := s.db.WithContext(ctx).Table("games").
		Where("author_id = ? AND deleted_at IS NULL", userID).
		Count(&stats.GamesCreated).Error; err != nil {
		return nil, err
	}

	// Прохождения (через team_members)
	if err := s.db.WithContext(ctx).Table("game_passings").
		Joins("JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.status = ? AND team_members.user_id = ?", "finished", userID).
		Count(&stats.GamesPlayed).Error; err != nil {
		return nil, err
	}

	// Победы
	if err := s.db.WithContext(ctx).Table("game_passings").
		Joins("JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.status = ? AND game_passings.place = 1 AND team_members.user_id = ?", "finished", userID).
		Count(&stats.Wins).Error; err != nil {
		return nil, err
	}

	// Рейтинг
	if err := s.db.WithContext(ctx).Table("player_ratings").
		Where("user_id = ?", userID).
		Select("score").
		Scan(&stats.Rating).Error; err != nil {
		return nil, err
	}

	return &stats, nil
}

// IsFollowing проверяет, подписан ли пользователь на другого.
func (s *ProfileService) IsFollowing(ctx context.Context, followerID, authorID uint) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Table("follows").
		Where("follower_id = ? AND author_id = ?", followerID, authorID).
		Count(&count).Error
	return count > 0, err
}

// GetRecentGames загружает последние игры автора.
func (s *ProfileService) GetRecentGames(ctx context.Context, authorID uint) ([]RecentGame, error) {
	var games []RecentGame
	err := s.db.WithContext(ctx).Table("games").
		Select("id, name, is_draft, cover_path, created_at").
		Where("author_id = ? AND is_draft = false AND deleted_at IS NULL", authorID).
		Order("created_at DESC").
		Limit(6).
		Find(&games).Error
	return games, err
}

// UpdateProfile обновляет имя и email пользователя.
func (s *ProfileService) UpdateProfile(ctx context.Context, userID uint, name, email string) error {
	return s.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
		"name":  name,
		"email": email,
	}).Error
}
