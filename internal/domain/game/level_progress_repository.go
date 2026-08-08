// internal/domain/game/level_progress_repository.go
// A-2 (pass 31): репозиторий прогрессов уровней — сервис LevelProgressService
// больше не обращается к *gorm.DB напрямую для read-путей.
package game

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// LevelProgressRepository — контракт для прогрессов уровней.
type LevelProgressRepository interface {
	CountByPassing(ctx context.Context, gamePassingID uint) (int64, error)
	GetCurrent(ctx context.Context, gamePassingID uint) (*LevelProgress, error)
	GetByID(ctx context.Context, id uint) (*LevelProgress, error)
	Create(ctx context.Context, progress *LevelProgress) error
	Save(ctx context.Context, progress *LevelProgress) error
}

type gormLevelProgressRepo struct{ db *gorm.DB }

func NewGormLevelProgressRepo(db *gorm.DB) LevelProgressRepository {
	return &gormLevelProgressRepo{db: db}
}

func (r *gormLevelProgressRepo) CountByPassing(ctx context.Context, gamePassingID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&LevelProgress{}).
		Where("game_passing_id = ?", gamePassingID).Count(&count).Error
	return count, err
}

func (r *gormLevelProgressRepo) GetCurrent(ctx context.Context, gamePassingID uint) (*LevelProgress, error) {
	var progress LevelProgress
	err := r.db.WithContext(ctx).
		Preload("Level.Questions.Answers").
		Where("game_passing_id = ? AND finished_at IS NULL", gamePassingID).
		First(&progress).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNoActiveLevel
		}
		return nil, err
	}
	return &progress, nil
}

func (r *gormLevelProgressRepo) GetByID(ctx context.Context, id uint) (*LevelProgress, error) {
	var progress LevelProgress
	err := r.db.WithContext(ctx).First(&progress, id).Error
	if err != nil {
		return nil, err
	}
	return &progress, nil
}

func (r *gormLevelProgressRepo) Create(ctx context.Context, progress *LevelProgress) error {
	return r.db.WithContext(ctx).Create(progress).Error
}

func (r *gormLevelProgressRepo) Save(ctx context.Context, progress *LevelProgress) error {
	return r.db.WithContext(ctx).Save(progress).Error
}
