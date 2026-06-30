// internal/domain/game/level_progress_service.go
package game

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"gengine-0/internal/domain/level"
)

type LevelProgressService struct {
	DB *gorm.DB
}

func NewLevelProgressService(db *gorm.DB) *LevelProgressService {
	return &LevelProgressService{DB: db}
}

// InitFirstLevel инициализирует прогресс первого уровня при старте игры.
func (s *LevelProgressService) InitFirstLevel(ctx context.Context, gamePassingID uint) error {
	var count int64
	s.DB.WithContext(ctx).Model(&LevelProgress{}).Where("game_passing_id = ?", gamePassingID).Count(&count)
	if count > 0 {
		return nil
	}

	var passing GamePassing
	if err := s.DB.WithContext(ctx).Preload("Game.Levels", func(db *gorm.DB) *gorm.DB {
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
	return s.DB.WithContext(ctx).Create(progress).Error
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
	// Загружаем прохождение только для получения GameID и Status
	var passing GamePassing
	if err := db.First(&passing, gamePassingID).Error; err != nil {
		return err
	}

	// Загружаем все неудалённые уровни игры напрямую, без зависимости от Preload
	var levels []level.Level
	if err := db.Where("game_id = ? AND deleted_at IS NULL", passing.GameID).
		Order("position ASC").Find(&levels).Error; err != nil {
		return err
	}

	foundCurrent := false
	for _, lvl := range levels {
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

	// Если нет следующего уровня, завершаем игру (кроме тестирования)
	if passing.Status != StatusTesting {
		passing.Status = StatusFinished
		return db.Save(&passing).Error
	}
	return nil
}

// CheckTimeouts проверяет все незавершённые прогрессы и завершает просроченные.
// Обёрнута в recover для защиты от паник.
func CheckTimeouts(db *gorm.DB, ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("CheckTimeouts: panic recovered, restarting goroutine")
			// Перезапускаем горутину через 5 секунд, чтобы не зациклиться при постоянных паниках
			time.Sleep(5 * time.Second)
			go CheckTimeouts(db, ctx)
		}
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("CheckTimeouts: context cancelled, stopping")
			return
		case <-ticker.C:
			var activeProgresses []LevelProgress
			if err := db.WithContext(ctx).Where("finished_at IS NULL").
				Preload("GamePassing.Game.GameSetting").
				Find(&activeProgresses).Error; err != nil {
				log.Error().Err(err).Msg("CheckTimeouts: failed to fetch active progresses")
				continue
			}

			now := time.Now()
			for _, p := range activeProgresses {
				// Защита от nil-указателей
				if p.GamePassing.Game.GameSetting.ID == 0 {
					log.Warn().Uint("progress_id", p.ID).Msg("CheckTimeouts: game setting not found for progress, skipping")
					continue
				}
				setting := p.GamePassing.Game.GameSetting
				if setting.PerLevelTimeLimit > 0 {
					elapsed := now.Sub(p.StartedAt)
					limit := time.Duration(setting.PerLevelTimeLimit) * time.Minute
					if elapsed >= limit {
						p.FinishedAt = &now
						if err := db.WithContext(ctx).Save(&p).Error; err != nil {
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
// Обёрнута в recover для защиты от паник.
func CheckAutoStartGames(db *gorm.DB, ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("CheckAutoStartGames: panic recovered, restarting goroutine")
			time.Sleep(5 * time.Second)
			go CheckAutoStartGames(db, ctx)
		}
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("CheckAutoStartGames: context cancelled, stopping")
			return
		case <-ticker.C:
			var games []Game
			now := time.Now()
			if err := db.WithContext(ctx).Where("is_draft = false AND starts_at IS NOT NULL AND starts_at <= ?", now).
				Preload("GameSetting").
				Find(&games).Error; err != nil {
				log.Error().Err(err).Msg("CheckAutoStartGames: failed to fetch games")
				continue
			}

			for _, g := range games {
				// Защита от nil-указателей
				if g.GameSetting.ID == 0 {
					log.Warn().Uint("game_id", g.ID).Msg("CheckAutoStartGames: game setting not found, skipping")
					continue
				}
				if !g.GameSetting.AutoStart {
					continue
				}
				var startedCount int64
				if err := db.WithContext(ctx).Model(&GamePassing{}).Where("game_id = ? AND status = ?", g.ID, StatusStarted).Count(&startedCount).Error; err != nil {
					log.Error().Err(err).Uint("game_id", g.ID).Msg("CheckAutoStartGames: failed to count started passings")
					continue
				}
				if startedCount > 0 {
					continue
				}

				var passings []GamePassing
				if err := db.WithContext(ctx).Where("game_id = ? AND status = ?", g.ID, StatusAccepted).Find(&passings).Error; err != nil {
					log.Error().Err(err).Uint("game_id", g.ID).Msg("CheckAutoStartGames: failed to fetch passings")
					continue
				}
				for _, p := range passings {
					p.Status = StatusStarted
					if err := db.WithContext(ctx).Save(&p).Error; err != nil {
						log.Error().Err(err).Uint("passing_id", p.ID).Msg("CheckAutoStartGames: failed to update passing status")
						continue
					}
					if err := NewLevelProgressService(db).InitFirstLevel(ctx, p.ID); err != nil {
						log.Error().Err(err).Uint("passing_id", p.ID).Msg("CheckAutoStartGames: failed to init first level")
					}
				}
			}
		}
	}
}
