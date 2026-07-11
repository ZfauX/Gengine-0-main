// internal/domain/game/level_progress_service.go
package game

import (
	"context"
	"errors"
	"time"

	"gengine-0/internal/domain/level"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Typed errors для level progress
var (
	ErrNoActiveLevel = errors.New("нет активного уровня")
	ErrNoLevels      = errors.New("у игры нет уровней")
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
		return ErrNoLevels
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
// БЕЗ БЛОКИРОВКИ — для чтения.
func GetCurrentProgress(db *gorm.DB, gamePassingID uint) (*LevelProgress, error) {
	var progress LevelProgress
	err := db.
		Preload("Level.Questions.Answers").
		Where("game_passing_id = ? AND finished_at IS NULL", gamePassingID).
		First(&progress).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoActiveLevel
	}
	return &progress, err
}

// GetCurrentProgressForUpdate возвращает текущий незавершённый прогресс с блокировкой FOR UPDATE.
// Используется внутри транзакций для предотвращения гонок.
func GetCurrentProgressForUpdate(tx *gorm.DB, gamePassingID uint) (*LevelProgress, error) {
	var progress LevelProgress
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Preload("Level.Questions.Answers").
		Where("game_passing_id = ? AND finished_at IS NULL", gamePassingID).
		First(&progress).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoActiveLevel
	}
	return &progress, err
}

// CompleteLevel завершает прогресс уровня и переходит к следующему.
// Работает с переданным *gorm.DB (может быть транзакцией).
func CompleteLevel(db *gorm.DB, progress *LevelProgress) error {
	now := time.Now()
	progress.FinishedAt = &now
	if err := db.Save(progress).Error; err != nil {
		return err
	}
	return AdvanceToNextLevel(db, progress.GamePassingID, progress.LevelID)
}

// AdvanceToNextLevel находит следующий уровень и создаёт для него прогресс.
// Работает с переданным *gorm.DB (может быть транзакцией).
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
// Запущена как горутина, останавливается через ctx.
func CheckTimeouts(db *gorm.DB, ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("CheckTimeouts: context cancelled, stopping")
			return
		case <-ticker.C:
			// Логируем ошибки, но не перезапускаем горутину — контекст управляет жизненным циклом
			var activeProgresses []LevelProgress
			if err := db.WithContext(ctx).Clauses(clause.Locking{
				Strength: "UPDATE",
				Options:  "SKIP LOCKED",
			}).Where("finished_at IS NULL").
				Preload("GamePassing.Game.GameSetting").
				Find(&activeProgresses).Error; err != nil {
				log.Error().Err(err).Msg("CheckTimeouts: failed to fetch active progresses")
				continue
			}

			now := time.Now()
			for _, p := range activeProgresses {
				var currentProgress LevelProgress
				if err := db.WithContext(ctx).Clauses(clause.Locking{
					Strength: "UPDATE",
					Options:  "SKIP LOCKED",
				}).First(&currentProgress, p.ID).Error; err != nil {
					continue
				}
				if currentProgress.FinishedAt != nil {
					continue
				}

				var passing GamePassing
				if err := db.WithContext(ctx).Preload("Game.GameSetting").First(&passing, currentProgress.GamePassingID).Error; err != nil {
					log.Error().Err(err).Uint("progress_id", currentProgress.ID).Msg("CheckTimeouts: failed to load passing")
					continue
				}
				if passing.Game.GameSetting.ID == 0 {
					log.Warn().Uint("progress_id", currentProgress.ID).Msg("CheckTimeouts: game setting not found for progress, skipping")
					continue
				}
				setting := passing.Game.GameSetting
				if setting.PerLevelTimeLimit > 0 {
					elapsed := now.Sub(currentProgress.StartedAt)
					limit := time.Duration(setting.PerLevelTimeLimit) * time.Minute
					if elapsed >= limit {
						currentProgress.FinishedAt = &now
						if err := db.WithContext(ctx).Save(&currentProgress).Error; err != nil {
							log.Error().Err(err).Uint("progress_id", currentProgress.ID).Msg("CheckTimeouts: failed to save progress")
							continue
						}
						if err := AdvanceToNextLevel(db, currentProgress.GamePassingID, currentProgress.LevelID); err != nil {
							log.Error().Err(err).Uint("passing_id", currentProgress.GamePassingID).Msg("CheckTimeouts: failed to advance level")
						}
					}
				}
			}
		}
	}
}

// CheckAutoStartGames автоматически запускает игры, у которых наступило время старта.
// Запущена как горутина, останавливается через ctx.
func CheckAutoStartGames(db *gorm.DB, ctx context.Context) {
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
