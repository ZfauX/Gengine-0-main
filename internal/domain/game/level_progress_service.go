// internal/domain/game/level_progress_service.go
package game

import (
	"context"
	"errors"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/pkg/metrics"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const levelProgressBatchSize = 50

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
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.dbTransaction(ctx, tx, gamePassingID)
	})
}

// InitFirstLevelWithTx инициализирует прогресс первого уровня с переданной транзакцией.
func (s *LevelProgressService) InitFirstLevelWithTx(ctx context.Context, tx *gorm.DB, gamePassingID uint) error {
	return s.dbTransaction(ctx, tx, gamePassingID)
}

// dbTransaction — общий метод инициализации первого уровня с переданным *gorm.DB.
func (s *LevelProgressService) dbTransaction(ctx context.Context, db *gorm.DB, gamePassingID uint) error {
	var count int64
	if err := db.WithContext(ctx).Model(&LevelProgress{}).Where("game_passing_id = ?", gamePassingID).Count(&count).Error; err != nil {
		return err
	}

	var passing GamePassing
	if err := db.WithContext(ctx).Preload("Game.Levels", func(db *gorm.DB) *gorm.DB {
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
	return db.WithContext(ctx).Create(progress).Error
}

// GetCurrentProgress возвращает текущий незавершённый прогресс уровня.
// БЕЗ БЛОКИРОВКИ — для чтения.
func (s *LevelProgressService) GetCurrentProgress(ctx context.Context, gamePassingID uint) (*LevelProgress, error) {
	var progress LevelProgress
	err := s.DB.WithContext(ctx).
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
// Возвращает onCommit — callback, который нужно вызвать ПОСЛЕ коммита транзакции.
func CompleteLevel(db *gorm.DB, progress *LevelProgress, onGameFinished func()) (onCommit func(), err error) {
	now := time.Now()
	progress.FinishedAt = &now
	if err = db.Save(progress).Error; err != nil {
		return nil, err
	}
	metrics.IncLevelProgress()
	return AdvanceToNextLevel(db, progress.GamePassingID, progress.LevelID, onGameFinished)
}

// AdvanceToNextLevel находит следующий уровень и создаёт для него прогресс.
// Работает с переданным *gorm.DB (может быть транзакцией).
// onGameFinished — необязательный callback, вызывается когда завершается последний уровень (игра окончена).
// Возвращает onCommit — callback, который нужно вызвать ПОСЛЕ коммита транзакции.
func AdvanceToNextLevel(db *gorm.DB, gamePassingID, completedLevelID uint, onGameFinished func()) (onCommit func(), err error) {
	// Загружаем прохождение только для получения GameID и Status
	var passing GamePassing
	if err = db.First(&passing, gamePassingID).Error; err != nil {
		return nil, err
	}

	// Загружаем все неудалённые уровни игры напрямую, без зависимости от Preload
	var levels []level.Level
	if err = db.Where("game_id = ? AND deleted_at IS NULL", passing.GameID).
		Order("position ASC").Find(&levels).Error; err != nil {
		return nil, err
	}

	foundCurrent := false
	for _, lvl := range levels {
		if foundCurrent {
			newProgress := &LevelProgress{
				GamePassingID: gamePassingID,
				LevelID:       lvl.ID,
				StartedAt:     time.Now(),
			}
			return nil, db.Create(newProgress).Error
		}
		if lvl.ID == completedLevelID {
			foundCurrent = true
		}
	}

	// Если нет следующего уровня, завершаем игру (кроме тестирования)
	if passing.Status != StatusTesting {
		passing.Status = StatusFinished
		if err = db.Save(&passing).Error; err != nil {
			return nil, err
		}
		// Возвращаем callback для вызова после коммита транзакции
		if onGameFinished != nil {
			return onGameFinished, nil
		}
	}
	return nil, nil
}

// periodicRunner запускает периодическую функцию с контекстом и ticker.
type periodicRunner struct {
	interval time.Duration
	fn       func(db *gorm.DB, ctx context.Context)
}

// runPeriodic запускает periodicRunner в горутине.
func runPeriodic(db *gorm.DB, ctx context.Context, runner periodicRunner) {
	ticker := time.NewTicker(runner.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msgf("periodicRunner: context cancelled, stopping")
			return
		case <-ticker.C:
			runner.fn(db, ctx)
		}
	}
}

// GameCompletionCallback — функция, вызываемая при завершении игры.
type GameCompletionCallback func(ctx context.Context, gameID uint)

// CheckTimeouts проверяет все незавершённые прогрессы и завершает просроченные.
// onGameFinished — необязательный callback для расчёта результатов при завершении игры.
func CheckTimeouts(db *gorm.DB, ctx context.Context, onGameFinished GameCompletionCallback) {
	runPeriodic(db, ctx, periodicRunner{
		interval: 30 * time.Second,
		fn:       func(db *gorm.DB, ctx context.Context) { checkTimeoutsImpl(db, ctx, onGameFinished) },
	})
}

func checkTimeoutsImpl(db *gorm.DB, ctx context.Context, onGameFinished GameCompletionCallback) {
	const batchSize = levelProgressBatchSize

	// Batch-загружаем active progresses с game_passings и settings в одном запросе
	type progressWithSetting struct {
		ID                uint
		GamePassingID     uint
		LevelID           uint
		StartedAt         time.Time
		FinishedAt        *time.Time
		PerLevelTimeLimit int
	}

	var progressesWithSettings []progressWithSetting
	if err := db.WithContext(ctx).
		Table("level_progresses").
		Select(`level_progresses.id, level_progresses.game_passing_id, level_progresses.level_id, 
		        level_progresses.started_at, level_progresses.finished_at, 
		        COALESCE(game_settings.per_level_time_limit, 0) as per_level_time_limit`).
		Joins("LEFT JOIN game_passings ON game_passings.id = level_progresses.game_passing_id").
		Joins("LEFT JOIN game_settings ON game_settings.game_id = game_passings.game_id").
		Where("level_progresses.finished_at IS NULL").
		Limit(batchSize).
		Find(&progressesWithSettings).Error; err != nil {
		log.Error().Err(err).Msg("CheckTimeouts: failed to fetch progresses with settings")
		return
	}

	if len(progressesWithSettings) == 0 {
		return
	}

	now := time.Now()
	var timedOutIDs []uint
	var timedOutProgresses []struct {
		ID            uint
		GamePassingID uint
		LevelID       uint
	}

	// Определяем просроченные прогрессы
	for _, p := range progressesWithSettings {
		if p.FinishedAt != nil {
			continue
		}
		if p.PerLevelTimeLimit <= 0 {
			continue
		}
		elapsed := now.Sub(p.StartedAt)
		limit := time.Duration(p.PerLevelTimeLimit) * time.Minute
		if elapsed >= limit {
			timedOutIDs = append(timedOutIDs, p.ID)
			timedOutProgresses = append(timedOutProgresses, struct {
				ID            uint
				GamePassingID uint
				LevelID       uint
			}{ID: p.ID, GamePassingID: p.GamePassingID, LevelID: p.LevelID})
		}
	}

	if len(timedOutIDs) == 0 {
		return
	}

	// Batch UPDATE всех просроченных прогрессов одной транзакцией
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Batch update finished_at для всех просроченных
		if err := tx.Model(&LevelProgress{}).
			Where("id IN ?", timedOutIDs).
			Where("finished_at IS NULL").
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Update("finished_at", now).Error; err != nil {
			return err
		}

		// Для каждого просроченного прогресса advance to next level
		for _, p := range timedOutProgresses {
			var passing GamePassing
			if err := tx.First(&passing, p.GamePassingID).Error; err != nil {
				log.Error().Err(err).Uint("passing_id", p.GamePassingID).Msg("CheckTimeouts: failed to fetch passing")
				continue
			}
			onCommit, err := AdvanceToNextLevel(tx, p.GamePassingID, p.LevelID, func() {
				if onGameFinished != nil {
					onGameFinished(ctx, passing.GameID)
				}
			})
			if err != nil {
				log.Error().Err(err).Uint("progress_id", p.ID).Msg("CheckTimeouts: AdvanceToNextLevel failed")
			}
			if onCommit != nil {
				// callback будет вызван после коммита транзакции
				defer onCommit()
			}
		}
		return nil
	})
	if err != nil {
		log.Error().Err(err).Msg("CheckTimeouts: batch transaction failed")
	}
}

// CheckAutoStartGames автоматически запускает игры, у которых наступило время старта.
// Запущена как горутина, останавливается через ctx.
func CheckAutoStartGames(db *gorm.DB, ctx context.Context) {
	runPeriodic(db, ctx, periodicRunner{
		interval: 30 * time.Second,
		fn:       func(db *gorm.DB, ctx context.Context) { checkAutoStartGamesImpl(db, ctx) },
	})
}

func checkAutoStartGamesImpl(db *gorm.DB, ctx context.Context) {
	const batchSize = levelProgressBatchSize

	var games []Game
	now := time.Now()
	if err := db.WithContext(ctx).
		Preload("GameSetting").
		Joins("JOIN game_settings ON game_settings.game_id = games.id").
		Where("games.is_draft = false AND games.starts_at IS NOT NULL AND games.starts_at <= ? AND game_settings.auto_start = true", now).
		Limit(batchSize).
		Find(&games).Error; err != nil {
		log.Error().Err(err).Msg("CheckAutoStartGames: failed to fetch games")
		return
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

		// Транзакция на всю партию passings для одной игры
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var passings []GamePassing
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("game_id = ? AND status = ?", g.ID, StatusAccepted).Find(&passings).Error; err != nil {
				return err
			}
			for _, p := range passings {
				p.Status = StatusStarted
				if err := tx.Save(&p).Error; err != nil {
					return err
				}
				txProgressSvc := NewLevelProgressService(tx)
				if err := txProgressSvc.InitFirstLevelWithTx(ctx, tx, p.ID); err != nil {
					log.Error().Err(err).Uint("passing_id", p.ID).Msg("CheckAutoStartGames: failed to init first level")
				}
			}
			return nil
		}); err != nil {
			log.Error().Err(err).Uint("game_id", g.ID).Msg("CheckAutoStartGames: transaction failed")
		}
	}
}
