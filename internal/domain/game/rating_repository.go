// internal/domain/game/rating_repository.go
// A-2 (pass 31): репозиторий рейтинга — RatingService не обращается к *gorm.DB
// для read-запросов. Транзакционные начисления очков остаются в сервисе
// (они работают с tx внутри транзакций — стандартный GORM-паттерн).
package game

import (
	"context"

	"gorm.io/gorm"
)

// RatingRepository — контракт для чтения рейтинга/лидерборда.
type RatingRepository interface {
	GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error)
	GetLeaderboard(ctx context.Context, limit int) ([]LeaderboardEntry, error)
}

type gormRatingRepo struct{ db *gorm.DB }

func NewGormRatingRepo(db *gorm.DB) RatingRepository {
	return &gormRatingRepo{db: db}
}

func (r *gormRatingRepo) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	var result struct {
		AvgRating float64
		Count     int64
	}
	err := r.db.WithContext(ctx).Table("reviews").
		Select("COALESCE(AVG(rating), 0) as avg_rating, COUNT(*) as count").
		Where("game_id = ?", gameID).
		Scan(&result).Error
	if err != nil {
		return 0, 0, err
	}
	return result.AvgRating, result.Count, nil
}

func (r *gormRatingRepo) GetLeaderboard(ctx context.Context, limit int) ([]LeaderboardEntry, error) {
	var entries []LeaderboardEntry
	err := r.db.WithContext(ctx).
		Table("player_ratings").
		Select("player_ratings.user_id, player_ratings.score, users.name, users.avatar_path").
		Joins("JOIN users ON users.id = player_ratings.user_id").
		Order("score DESC").
		Limit(limit).
		Scan(&entries).Error
	return entries, err
}

var _ RatingRepository = (*gormRatingRepo)(nil)
