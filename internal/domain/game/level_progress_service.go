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
	return s.dbTransaction(ctx, s.DB, gamePassingID)
}

// InitFirstLevelWithTx инициализирует прогресс первого уровня с переданной транзакцией.
func (s *LevelProgressService) InitFirstLevelWithTx(ctx context.Context, tx *gorm.DB, gamePassingID uint) error {
	return s.dbTransaction(ctx, tx, gamePassingID)
}

// dbTransaction — общий метод инициализации первого уровня с переданным *gorm.DB.
func (s *LevelProgressService) dbTransaction(ctx context.Context, db *gorm.DB, gamePassingID uint) error {
	var count int64
	db.Model(&LevelProgress{}).Where("game_passing_id = ?", gamePassingID).Count(&count)
	if count > 0 {
		return nil
	}

	var passing GamePassing
	if err := db.Preload("Game.Levels", func(db *gorm.DB) *gorm.DB {
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
	return db.Create(progress).Error
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
func CompleteLevel(db *gorm.DB, progress *LevelProgress, onGameFinished func()) error {
	now := time.Now()
	progress.FinishedAt = &now
	if err := db.Save(progress).Error; err != nil {
		return err
	}
	return AdvanceToNextLevel(db, progress.GamePassingID, progress.LevelID, onGameFinished)
}

// AdvanceToNextLevel находит следующий уровень и создаёт для него прогресс.
// Работает с переданным *gorm.DB (может быть транзакцией).
// onGameFinished — необязательный callback, вызывается когда завершается последний уровень (игра окончена).
func AdvanceToNextLevel(db *gorm.DB, gamePassingID, completedLevelID uint, onGameFinished func()) error {
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
		if err := db.Save(&passing).Error; err != nil {
			return err
		}
		// Вызываем callback после фиксации транзакции
		if onGameFinished != nil {
			onGameFinished()
		}
	}
	return nil
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

// CheckTimeouts проверяет все незавершённые прогрессы и завершает просроченные.
// Запущена как горутина, останавливается через ctx.
func CheckTimeouts(db *gorm.DB, ctx context.Context) {
	runPeriodic(db, ctx, periodicRunner{
		interval: 30 * time.Second,
		fn:       checkTimeoutsImpl,
	})
}

func checkTimeoutsImpl(db *gorm.DB, ctx context.Context) {
	const batchSize = 50

	var activeProgresses []LevelProgress
	if err := db.WithContext(ctx).Clauses(clause.Locking{
		Strength: "UPDATE",
		Options:  "SKIP LOCKED",
	}).Select("id, game_passing_id, level_id, started_at, finished_at, penalty_seconds, hints_used").
		Where("finished_at IS NULL").
		Limit(batchSize).
		Find(&activeProgresses).Error; err != nil {
		log.Error().Err(err).Msg("CheckTimeouts: failed to fetch active progresses")
		return
	}

	if len(activeProgresses) == 0 {
		return
	}

	// Собираем уникальные game_passing_id для batch-загрузки settings
	var passingIDs []uint
	for _, p := range activeProgresses {
		passingIDs = append(passingIDs, p.GamePassingID)
	}

	// Batch-загружаем game_passings с settings
	type passingWithSetting struct {
		ID                uint
		PerLevelTimeLimit int
	}
	var passingsWithSettings []passingWithSetting
	if err := db.WithContext(ctx).
		Model(&GamePassing{}).
		Select("game_passings.id, game_settings.per_level_time_limit").
		Joins("LEFT JOIN game_settings ON game_settings.game_id = game_passings.game_id").
		Where("game_passings.id IN ?", passingIDs).
		Find(&passingsWithSettings).Error; err != nil {
		log.Error().Err(err).Msg("CheckTimeouts: failed to fetch passings with settings")
	}

	settingMap := make(map[uint]*GameSetting)
	for _, ps := range passingsWithSettings {
		settingMap[ps.ID] = &GameSetting{PerLevelTimeLimit: ps.PerLevelTimeLimit}
	}

	now := time.Now()
	for _, p := range activeProgresses {
		if p.FinishedAt != nil {
			continue
		}

		setting, ok := settingMap[p.GamePassingID]
		if !ok || setting.PerLevelTimeLimit <= 0 {
			continue
		}

		elapsed := now.Sub(p.StartedAt)
		limit := time.Duration(setting.PerLevelTimeLimit) * time.Minute
		if elapsed >= limit {
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				// Блокируем прогресс
				var current LevelProgress
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
					Where("id = ? AND finished_at IS NULL", p.ID).
					First(&current).Error; err != nil {
					return err
				}
				current.FinishedAt = &now
				if err := tx.Save(&current).Error; err != nil {
					return err
				}
				return AdvanceToNextLevel(tx, current.GamePassingID, current.LevelID, nil)
			}); err != nil {
				log.Error().Err(err).Uint("progress_id", p.ID).Msg("CheckTimeouts: transaction failed")
			}
		}
	}
}

// CheckAutoStartGames автоматически запускает игры, у которых наступило время старта.
// Запущена как горутина, останавливается через ctx.
func CheckAutoStartGames(db *gorm.DB, ctx context.Context) {
	progressSvc := NewLevelProgressService(db)
	runPeriodic(db, ctx, periodicRunner{
		interval: 30 * time.Second,
		fn:       func(db *gorm.DB, ctx context.Context) { checkAutoStartGamesImpl(db, ctx, progressSvc) },
	})
}

func checkAutoStartGamesImpl(db *gorm.DB, ctx context.Context, progressSvc *LevelProgressService) {
	const batchSize = 20

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
