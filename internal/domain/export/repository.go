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
	// A-3 (pass 35): типизированные read-методы результатов команды — вместо
	// DB(ctx), который отдавал *gorm.DB наружу и позволял сервису писать SQL.
	GetPassingByGameAndTeam(ctx context.Context, gameID, teamID uint) (*game.GamePassing, error)
	GetProgressesByPassing(ctx context.Context, passingID uint) ([]game.LevelProgress, error)
	GetLevelsByGame(ctx context.Context, gameID uint) ([]level.Level, error)
	GetAttemptsByProgressIDs(ctx context.Context, progressIDs []uint) ([]game.Attempt, error)
}

type gormExportRepo struct {
	db *gorm.DB
}

func NewGormExportRepo(db *gorm.DB) ExportRepository {
	return &gormExportRepo{db: db}
}

func (r *gormExportRepo) GetGameWithLevels(ctx context.Context, gameID uint) (*game.Game, []level.Level, error) {
	var g game.Game
	// LOW #11 (PASS-13): Preload("Author") с ограничением полей — раньше
	// грузилась полная строка users (password_hash/email в память зря).
	if err := r.db.WithContext(ctx).
		Preload("Author", func(db *gorm.DB) *gorm.DB {
			return db.Select("id", "name", "avatar_path")
		}).
		First(&g, gameID).Error; err != nil {
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
		// #9: загружаем только id попыток (для len) — без огромных кодов ответов.
		Preload("Progresses.Attempts", func(db *gorm.DB) *gorm.DB {
			return db.Select("id")
		}).
		// P-44-9 (pass 44): экспорт использует только Level.Name — без description/hint.
		Preload("Progresses.Level", func(db *gorm.DB) *gorm.DB {
			return db.Select("id, name")
		}).
		Where("game_id = ? AND status = ?", gameID, game.StatusFinished).
		Order("place ASC").
		Find(&passings).Error
	return passings, err
}

// GetPassingByGameAndTeam возвращает прохождение команды в игре (A-3, pass 35).
func (r *gormExportRepo) GetPassingByGameAndTeam(ctx context.Context, gameID, teamID uint) (*game.GamePassing, error) {
	var passing game.GamePassing
	err := r.db.WithContext(ctx).
		Where("game_id = ? AND team_id = ?", gameID, teamID).
		First(&passing).Error
	if err != nil {
		return nil, err
	}
	return &passing, nil
}

// GetProgressesByPassing возвращает прогрессы прохождения в хронологическом порядке.
func (r *gormExportRepo) GetProgressesByPassing(ctx context.Context, passingID uint) ([]game.LevelProgress, error) {
	var progress []game.LevelProgress
	err := r.db.WithContext(ctx).
		Where("game_passing_id = ?", passingID).
		Order("created_at ASC").
		Find(&progress).Error
	return progress, err
}

// GetLevelsByGame возвращает уровни игры в порядке позиций.
func (r *gormExportRepo) GetLevelsByGame(ctx context.Context, gameID uint) ([]level.Level, error) {
	var levels []level.Level
	err := r.db.WithContext(ctx).
		Where("game_id = ?", gameID).
		Order("position ASC").
		Find(&levels).Error
	return levels, err
}

// GetAttemptsByProgressIDs возвращает все попытки по списку прогрессов.
// M7 (PASS-17): Select только нужных колонок — раньше тянулись полные Attempt
// (Code, FilePath, IsFile, UserID, ссылки) только ради подсчёта по прогрессу.
func (r *gormExportRepo) GetAttemptsByProgressIDs(ctx context.Context, progressIDs []uint) ([]game.Attempt, error) {
	var attempts []game.Attempt
	if len(progressIDs) == 0 {
		return attempts, nil
	}
	err := r.db.WithContext(ctx).
		Select("id", "level_progress_id").
		Where("level_progress_id IN ?", progressIDs).
		Find(&attempts).Error
	return attempts, err
}
