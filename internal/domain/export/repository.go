// internal/domain/export/repository.go
package export

import (
	"context"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"

	"gorm.io/gorm"
)

// ExportRepository определяет контракт для получения данных для экспорта.
type ExportRepository interface {
	// GetGameWithLevels загружает игру со всеми уровнями, вопросами и ответами.
	GetGameWithLevels(ctx context.Context, gameID uint) (*game.Game, []level.Level, error)
	// GetFinishedPassingsWithDetails загружает завершённые прохождения с командами, прогрессом, попытками и уровнями.
	GetFinishedPassingsWithDetails(ctx context.Context, gameID uint) ([]game.GamePassing, error)
}

type gormExportRepo struct {
	db *gorm.DB
}

func NewGormExportRepo(db *gorm.DB) ExportRepository {
	return &gormExportRepo{db: db}
}

func (r *gormExportRepo) GetGameWithLevels(ctx context.Context, gameID uint) (*game.Game, []level.Level, error) {
	var g game.Game
	if err := r.db.WithContext(ctx).Preload("Author").First(&g, gameID).Error; err != nil {
		return nil, nil, err
	}
	var levels []level.Level
	err := r.db.WithContext(ctx).
		Preload("Questions.Answers").
		Where("game_id = ?", gameID).
		Order("position ASC").
		Find(&levels).Error
	return &g, levels, err
}

func (r *gormExportRepo) GetFinishedPassingsWithDetails(ctx context.Context, gameID uint) ([]game.GamePassing, error) {
	var passings []game.GamePassing
	err := r.db.WithContext(ctx).
		Preload("Team").
		Preload("Progresses").
		Preload("Progresses.Attempts").
		Preload("Progresses.Level").
		Where("game_id = ? AND status = ?", gameID, game.StatusFinished).
		Order("place ASC").
		Find(&passings).Error
	return passings, err
}
