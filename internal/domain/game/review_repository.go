// internal/domain/game/review_repository.go
// A-2 (pass 31): репозиторий отзывов — ReviewService не обращается к *gorm.DB.
package game

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReviewRepository — контракт для отзывов.
type ReviewRepository interface {
	CanReview(ctx context.Context, gameID, userID uint) (bool, error)
	CreateIfNotExists(ctx context.Context, review *Review) (bool, error)
	ListByGame(ctx context.Context, gameID uint) ([]Review, error)
	GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error)
}

type gormReviewRepo struct{ db *gorm.DB }

func NewGormReviewRepo(db *gorm.DB) ReviewRepository {
	return &gormReviewRepo{db: db}
}

func (r *gormReviewRepo) CanReview(ctx context.Context, gameID, userID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&GamePassing{}).
		Joins("JOIN teams ON teams.id = game_passings.team_id").
		Where("game_passings.game_id = ? AND game_passings.status = ?", gameID, StatusFinished).
		Where("(teams.captain_id = ? OR EXISTS (SELECT 1 FROM team_members WHERE team_members.team_id = game_passings.team_id AND team_members.user_id = ?))", userID, userID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	var reviewCount int64
	if err := r.db.WithContext(ctx).Model(&Review{}).Where("game_id = ? AND user_id = ?", gameID, userID).Count(&reviewCount).Error; err != nil {
		return false, err
	}
	return reviewCount == 0, nil
}

// CreateIfNotExists создаёт отзыв с ON CONFLICT DO NOTHING.
// Возвращает true, если строка создана (RowsAffected == 1).
func (r *gormReviewRepo) CreateIfNotExists(ctx context.Context, review *Review) (bool, error) {
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "game_id"}, {Name: "user_id"}},
		DoNothing: true,
	}).Create(review)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *gormReviewRepo) ListByGame(ctx context.Context, gameID uint) ([]Review, error) {
	var reviews []Review
	// P-43-1 (pass 43): защитный LIMIT — раньше грузили ВСЕ отзывы игры в память
	// (и в 5-мин кэш). UI показывает скролл-блок; 100 — практический потолок.
	err := r.db.WithContext(ctx).Preload("User", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, name, avatar_path")
	}).Where("game_id = ?", gameID).Order("created_at DESC").Limit(100).Find(&reviews).Error
	return reviews, err
}

func (r *gormReviewRepo) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	var result struct {
		Avg   float64
		Count int64
	}
	err := r.db.WithContext(ctx).Model(&Review{}).
		Where("game_id = ?", gameID).
		Select("COALESCE(AVG(rating), 0) as avg, COUNT(*) as count").
		Scan(&result).Error
	return result.Avg, result.Count, err
}

var _ ReviewRepository = (*gormReviewRepo)(nil)
