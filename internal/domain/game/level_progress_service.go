package game

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type LevelProgressService struct {
	DB *gorm.DB
}

func NewLevelProgressService(db *gorm.DB) *LevelProgressService {
	return &LevelProgressService{DB: db}
}

// InitFirstLevel инициализирует прогресс первого уровня при старте игры.
func (s *LevelProgressService) InitFirstLevel(gamePassingID uint) error {
	var count int64
	s.DB.Model(&LevelProgress{}).Where("game_passing_id = ?", gamePassingID).Count(&count)
	if count > 0 {
		return nil
	}

	var passing GamePassing
	if err := s.DB.Preload("Game.Levels", func(db *gorm.DB) *gorm.DB {
		return db.Order("position ASC")
	}).First(&passing, gamePassingID).Error; err != nil {
		return err
	}

	levels := passing.Game.Levels
	if len(levels) == 0 {
		return errors.New("у игры нет уровней")
	}

	firstLevel := levels[0]
	progress := &LevelProgress{
		GamePassingID: gamePassingID,
		LevelID:       firstLevel.ID,
		StartedAt:     time.Now(),
	}
	return s.DB.Create(progress).Error
}

// GetCurrentProgress возвращает текущий незавершённый прогресс уровня.
func GetCurrentProgress(db *gorm.DB, gamePassingID uint) (*LevelProgress, error) {
	var progress LevelProgress
	err := db.
		Preload("Level.Questions.Answers").
		Where("game_passing_id = ? AND finished_at IS NULL", gamePassingID).
		First(&progress).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("нет активного уровня")
	}
	return &progress, err
}

// CompleteLevel завершает прогресс уровня и переходит к следующему.
func CompleteLevel(db *gorm.DB, progress *LevelProgress) error {
	now := time.Now()
	progress.FinishedAt = &now
	if err := db.Save(progress).Error; err != nil {
		return err
	}
	return AdvanceToNextLevel(db, progress.GamePassingID, progress.LevelID)
}

// AdvanceToNextLevel находит следующий уровень и создаёт для него прогресс.
func AdvanceToNextLevel(db *gorm.DB, gamePassingID, completedLevelID uint) error {
	var passing GamePassing
	if err := db.Preload("Game.Levels", func(db *gorm.DB) *gorm.DB {
		return db.Order("position ASC")
	}).First(&passing, gamePassingID).Error; err != nil {
		return err
	}

	levels := passing.Game.Levels
	foundCurrent := false
	for _, lvl := range levels {
		if lvl.DeletedAt.Valid {
			continue
		}
		if foundCurrent {
			newProgress := &LevelProgress{
				GamePassingID: gamePassingID,
				LevelID:       lvl.ID,
				StartedAt:     time.Now(),
			}
			return db.Create(newProgress).Error
		}
		if lvl.ID == completedLevelID {
			foundCurrent = true
		}
	}

	if passing.Status != StatusTesting {
		passing.Status = StatusFinished
		return db.Save(&passing).Error
	}
	return nil
}

// CheckTimeouts проверяет все незавершённые прогрессы и завершает просроченные.
func CheckTimeouts(db *gorm.DB, ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var activeProgresses []LevelProgress
			db.Where("finished_at IS NULL").
				Preload("GamePassing.Game.GameSetting").
				Find(&activeProgresses)

			now := time.Now()
			for _, p := range activeProgresses {
				setting := p.GamePassing.Game.GameSetting
				if setting.PerLevelTimeLimit > 0 {
					elapsed := now.Sub(p.StartedAt)
					limit := time.Duration(setting.PerLevelTimeLimit) * time.Minute
					if elapsed >= limit {
						p.FinishedAt = &now
						if err := db.Save(&p).Error; err != nil {
							log.Error().Err(err).Uint("progress_id", p.ID).Msg("CheckTimeouts: failed to save progress")
							continue
						}
						if err := AdvanceToNextLevel(db, p.GamePassingID, p.LevelID); err != nil {
							log.Error().Err(err).Uint("passing_id", p.GamePassingID).Msg("CheckTimeouts: failed to advance level")
						}
					}
				}
			}
		}
	}
}

// CheckAutoStartGames автоматически запускает игры, у которых наступило время старта.
func CheckAutoStartGames(db *gorm.DB, ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var games []Game
			now := time.Now()
			db.Where("is_draft = false AND starts_at IS NOT NULL AND starts_at <= ?", now).
				Preload("GameSetting").
				Find(&games)

			for _, g := range games {
				if g.GameSetting.ID == 0 || !g.GameSetting.AutoStart {
					continue
				}
				var startedCount int64
				db.Model(&GamePassing{}).Where("game_id = ? AND status = ?", g.ID, StatusStarted).Count(&startedCount)
				if startedCount > 0 {
					continue
				}

				var passings []GamePassing
				db.Where("game_id = ? AND status = ?", g.ID, StatusAccepted).Find(&passings)
				for _, p := range passings {
					p.Status = StatusStarted
					db.Save(&p)
					_ = NewLevelProgressService(db).InitFirstLevel(p.ID)
				}
			}
		}
	}
}