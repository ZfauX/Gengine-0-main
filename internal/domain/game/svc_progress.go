// internal/domain/game/level_progress_service.go
package game

import (
	"context"
	"errors"
	"fmt"
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
	// ErrCompletedLevelNotFound — завершённый уровень удалён из игры (A-4, pass 39).
	ErrCompletedLevelNotFound = errors.New("завершённый уровень не найден")
)

type LevelProgressService struct {
	db          *gorm.DB
	repo        LevelProgressRepository
	sseMgr      *SSEManager
	gameService *GameService
}

func NewLevelProgressService(db *gorm.DB) *LevelProgressService {
	return &LevelProgressService{db: db}
}

// WithRepository устанавливает репозиторий прогрессов (A-2, pass 31).
func (s *LevelProgressService) WithRepository(repo LevelProgressRepository) *LevelProgressService {
	s.repo = repo
	return s
}

// WithSSEManager устанавливает SSE-менеджер для broadcast-уведомлений.
func (s *LevelProgressService) WithSSEManager(sseMgr *SSEManager) *LevelProgressService {
	s.sseMgr = sseMgr
	return s
}

// WithGameService устанавливает сервис игр для получения ID игры.
func (s *LevelProgressService) WithGameService(gameService *GameService) *LevelProgressService {
	s.gameService = gameService
	return s
}

// InitFirstLevel инициализирует прогресс первого уровня при старте игры.
func (s *LevelProgressService) InitFirstLevel(ctx context.Context, gamePassingID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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

	var firstLevel level.Level
	if err := db.WithContext(ctx).Where("game_id = (SELECT game_id FROM game_passings WHERE id = ?)", gamePassingID).Order("position ASC").Limit(1).First(&firstLevel).Error; err != nil {
		return err
	}

	if firstLevel.ID == 0 {
		return ErrNoLevels
	}
	progress := &LevelProgress{
		GamePassingID: gamePassingID,
		LevelID:       firstLevel.ID,
		StartedAt:     time.Now(),
	}
	return db.WithContext(ctx).Create(progress).Error
}

// GetCurrentProgress возвращает текущий незавершённый прогресс уровня.
// БЕЗ БЛОКИРОВКИ — для чтения. Через репозиторий (A-2, pass 31).
func (s *LevelProgressService) GetCurrentProgress(ctx context.Context, gamePassingID uint) (*LevelProgress, error) {
	return s.repo.GetCurrent(ctx, gamePassingID)
}

// GetCurrentProgressForUpdate возвращает текущий незавершённый прогресс с блокировкой FOR UPDATE.
// Используется внутри транзакций для предотвращения гонок.
// NB: без Preload уровня (pass 25 / #7) — SubmitCodeWithTx сам догружает
// Level.Questions.Answers, а UseHint/SubmitFile/AcceptBlackboxAnswer граф не нужен.
func GetCurrentProgressForUpdate(tx *gorm.DB, gamePassingID uint) (*LevelProgress, error) {
	var progress LevelProgress
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("game_passing_id = ? AND finished_at IS NULL", gamePassingID).
		First(&progress).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoActiveLevel
	}
	return &progress, err
}

// CompleteLevel завершает прогресс уровня и переходит к следующему.
// Работает с переданным *gorm.DB (может быть транзакцией).
// onGameFinished — необязательный callback, вызывается когда завершается последний уровень.
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
	return AdvanceToNextLevelWithPassing(db, &passing, completedLevelID, onGameFinished)
}

// AdvanceToNextLevelWithPassing — вариант AdvanceToNextLevel, принимающий уже
// загруженное прохождение (P-1, pass 38): убирает повторный SELECT из
// checkTimeoutsImpl, где passings грузятся батчем.
func AdvanceToNextLevelWithPassing(db *gorm.DB, passing *GamePassing, completedLevelID uint, onGameFinished func()) (onCommit func(), err error) {
	gamePassingID := passing.ID

	// Perf (pass 24): вместо загрузки ВСЕХ уровней игры — один запрос
	// следующего уровня после завершённого.
	var nextLevel level.Level
	err = db.Where("game_id = ? AND deleted_at IS NULL AND position > (SELECT position FROM levels WHERE id = ?)",
		passing.GameID, completedLevelID).
		Order("position ASC").First(&nextLevel).Error
	switch {
	case err == nil:
		newProgress := &LevelProgress{
			GamePassingID: gamePassingID,
			LevelID:       nextLevel.ID,
			StartedAt:     time.Now(),
		}
		return nil, db.Create(newProgress).Error
	case errors.Is(err, gorm.ErrRecordNotFound):
		// Следующего нет: либо это последний уровень, либо завершённый удалён.
		// Проверяем существование завершённого уровня.
		var exists int64
		if countErr := db.Model(&level.Level{}).Where("id = ?", completedLevelID).Count(&exists).Error; countErr != nil {
			return nil, countErr
		}
		if exists == 0 {
			log.Warn().Uint("game_passing_id", gamePassingID).Uint("level_id", completedLevelID).Msg("AdvanceToNextLevel: completed level not found (possibly deleted)")
			return nil, ErrCompletedLevelNotFound
		}
		// Последний уровень — завершаем игру (кроме тестирования).
		if passing.Status != StatusTesting {
			passing.Status = StatusFinished
			if err = db.Save(passing).Error; err != nil {
				return nil, err
			}
			if onGameFinished != nil {
				return onGameFinished, nil
			}
		}
		return nil, nil
	default:
		return nil, err
	}
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
			log.Info().Msgf("periodicRunner: context canceled, stopping")
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
		// ORDER BY started_at ASC (P-H5): без сортировки partial-индекс
		// (game_passing_id, finished_at) отдавал одни и те же занятые прохождения
		// каждые 30с, а свежие голодали.
		Order("level_progresses.started_at ASC").
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

	// Batch UPDATE всех просроченных прогрессов одной транзакцией.
	// Колбэки завершения игры (турнирные очки) вызываем ПОСЛЕ коммита —
	// так же, как в svc_play.go (иначе они читают незакоммиченные данные).
	var onCommitCallbacks []func()
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Batch update finished_at для всех просроченных.
		// RowsAffected проверяем: если другой инстанс уже обработал те же ID
		// (вторая транзакция видит finished_at уже не NULL из-за row-блокировки
		// UPDATE), то advance-петлю пропускаем — иначе создадутся дублирующие
		// next-level прогрессы (B6).
		// NB: НЕ добавляем Clauses(Locking{...}) — GORM не билдит его для UPDATE
		// (no-op), а сам UPDATE уже берёт row-локи (C-H4).
		res := tx.Model(&LevelProgress{}).
			Where("id IN ?", timedOutIDs).
			Where("finished_at IS NULL").
			Update("finished_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}

		// S2 (pass 30): advance только для прогрессов, обновлённых ЭТОЙ
		// транзакцией. Batch UPDATE в multi-instance может вернуть частичный
		// RowsAffected (часть ID уже обработана конкурентным инстансом, для них
		// finished_at != наш now) — иначе AdvanceToNextLevel создаст дубли.
		var updatedIDs []uint
		if err := tx.Model(&LevelProgress{}).
			Where("id IN ? AND finished_at = ?", timedOutIDs, now).
			Pluck("id", &updatedIDs).Error; err != nil {
			return fmt.Errorf("не удалось получить обновлённые прогрессы: %w", err)
		}
		updated := make(map[uint]bool, len(updatedIDs))
		for _, id := range updatedIDs {
			updated[id] = true
		}
		// Отбираем только те просроченные, что обновила наша транзакция.
		progressesToAdvance := timedOutProgresses[:0]
		for _, p := range timedOutProgresses {
			if updated[p.ID] {
				progressesToAdvance = append(progressesToAdvance, p)
			}
		}
		if len(progressesToAdvance) == 0 {
			return nil
		}

		// Для каждого просроченного прогресса advance to next level.
		// C-5: при сбое advance возвращаем ошибку из транзакции — иначе
		// прохождение остаётся finished_at без next-progress и retry невозможен.
		// Откат всей партии безопасен: в следующем цикле таймаут повторится.
		// M15 (pass 30): грузим все прохождения одним запросом (вместо N+1).
		passingIDs := make([]uint, 0, len(progressesToAdvance))
		for _, p := range progressesToAdvance {
			passingIDs = append(passingIDs, p.GamePassingID)
		}
		var passings []GamePassing
		if err := tx.Where("id IN ?", passingIDs).Find(&passings).Error; err != nil {
			return fmt.Errorf("не удалось загрузить прохождения: %w", err)
		}
		passingByID := make(map[uint]GamePassing, len(passings))
		for i := range passings {
			passingByID[passings[i].ID] = passings[i]
		}

		// P-1 (pass 39): вместо N запросов next-level на каждый просроченный
		// прогресс — prefetch уровней всех затронутых игр одним запросом и
		// ручной поиск следующего уровня по позиции.
		gameIDs := make([]uint, 0, len(passings))
		seenGames := make(map[uint]bool, len(passings))
		for _, passing := range passings {
			if !seenGames[passing.GameID] {
				seenGames[passing.GameID] = true
				gameIDs = append(gameIDs, passing.GameID)
			}
		}
		var levelsByGame map[uint][]level.Level
		if len(gameIDs) > 0 {
			var allLevels []level.Level
			if err := tx.Where("game_id IN ? AND deleted_at IS NULL", gameIDs).
				Order("game_id ASC, position ASC").
				Find(&allLevels).Error; err != nil {
				return fmt.Errorf("не удалось загрузить уровни: %w", err)
			}
			levelsByGame = make(map[uint][]level.Level, len(gameIDs))
			for _, lvl := range allLevels {
				levelsByGame[lvl.GameID] = append(levelsByGame[lvl.GameID], lvl)
			}
		}

		// Находим следующий уровень после completedLevelID.
		// Возвращает next=nil, completedExists=false, если завершённый уровень
		// был удалён из игры (прежняя COUNT-проверка из AdvanceToNextLevel).
		nextLevelFor := func(gameID, completedLevelID uint) (next *level.Level, completedExists bool) {
			var completedPos int
			foundCompleted := false
			for _, lvl := range levelsByGame[gameID] {
				if lvl.ID == completedLevelID {
					completedPos = lvl.Position
					foundCompleted = true
					break
				}
			}
			if !foundCompleted {
				return nil, false
			}
			for _, lvl := range levelsByGame[gameID] {
				if lvl.Position > completedPos {
					return &lvl, true
				}
			}
			return nil, true
		}

		// Собираем batch новых прогрессов и прохождения, которые нужно завершить.
		type finishItem struct {
			passing  *GamePassing
			onFinish func()
		}
		var newProgresses []LevelProgress
		var finishItems []finishItem
		for _, p := range progressesToAdvance {
			passing, ok := passingByID[p.GamePassingID]
			if !ok {
				log.Error().Uint("passing_id", p.GamePassingID).Msg("CheckTimeouts: passing not found")
				return fmt.Errorf("не удалось загрузить прохождение %d", p.GamePassingID)
			}
			onFinish := func(gid uint) func() {
				return func() {
					if onGameFinished != nil {
						onGameFinished(ctx, gid)
					}
				}
			}(passing.GameID)

			next, completedExists := nextLevelFor(passing.GameID, p.LevelID)
			if !completedExists {
				// Завершённый уровень удалён — не можем перевести прохождение.
				log.Warn().Uint("passing_id", p.GamePassingID).Uint("level_id", p.LevelID).Msg("CheckTimeouts: completed level not found (possibly deleted)")
				return fmt.Errorf("завершённый уровень %d не найден: %w", p.LevelID, ErrCompletedLevelNotFound)
			}
			if next != nil {
				newProgresses = append(newProgresses, LevelProgress{
					GamePassingID: p.GamePassingID,
					LevelID:       next.ID,
					StartedAt:     time.Now(),
				})
				continue
			}
			// Следующего нет: последний уровень.
			passingCopy := passing
			finishItems = append(finishItems, finishItem{passing: &passingCopy, onFinish: onFinish})
		}

		// Batch-create новых прогрессов.
		if len(newProgresses) > 0 {
			if err := tx.CreateInBatches(&newProgresses, levelProgressBatchSize).Error; err != nil {
				return fmt.Errorf("не удалось создать прогрессы следующих уровней: %w", err)
			}
		}

		// Завершаем игры с последним пройденным уровнем (кроме тестирования).
		for _, fi := range finishItems {
			if fi.passing.Status == StatusTesting {
				continue
			}
			fi.passing.Status = StatusFinished
			if err := tx.Save(fi.passing).Error; err != nil {
				return fmt.Errorf("не удалось завершить прохождение %d: %w", fi.passing.ID, err)
			}
			onCommitCallbacks = append(onCommitCallbacks, fi.onFinish)
		}
		return nil
	})
	if err != nil {
		log.Error().Err(err).Msg("CheckTimeouts: batch transaction failed")
		return
	}

	// Колбэки завершения — строго после успешного коммита (B1-порядок).
	for _, cb := range onCommitCallbacks {
		cb()
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
	// P-M9: JOIN с фильтром auto_start=true заменяет Preload + повторную проверку.
	if err := db.WithContext(ctx).
		Joins("JOIN game_settings ON game_settings.game_id = games.id AND game_settings.auto_start = true").
		Where("games.is_draft = false AND games.starts_at IS NOT NULL AND games.starts_at <= ?", now).
		Limit(batchSize).
		Find(&games).Error; err != nil {
		log.Error().Err(err).Msg("CheckAutoStartGames: failed to fetch games")
		return
	}

	// PF-5 (pass 29): один батч-COUNT вместо COUNT на каждую игру (N+1).
	gameIDs := make([]uint, 0, len(games))
	for _, g := range games {
		gameIDs = append(gameIDs, g.ID)
	}
	startedCounts := make(map[uint]int64, len(gameIDs))
	if len(gameIDs) > 0 {
		var rows []struct {
			GameID uint
			Cnt    int64
		}
		if err := db.WithContext(ctx).Model(&GamePassing{}).
			Select("game_id, COUNT(*) AS cnt").
			Where("game_id IN ? AND status = ?", gameIDs, StatusStarted).
			Group("game_id").
			Scan(&rows).Error; err != nil {
			log.Error().Err(err).Msg("CheckAutoStartGames: failed to count started passings (batch)")
			return
		}
		for _, r := range rows {
			startedCounts[r.GameID] = r.Cnt
		}
	}

	for _, g := range games {
		if startedCounts[g.ID] > 0 {
			continue
		}

		// Транзакция на всю партию passings для одной игры.
		// P-2 (pass 37): первый уровень грузим ОДИН раз на игру, прогрессы
		// создаём batch INSERT (OnConflict DoNothing) — раньше на каждый
		// passing шли Count+First+Create+Save = 4 запроса (N+1 при автостарте).
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var passings []GamePassing
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("game_id = ? AND status = ?", g.ID, StatusAccepted).Find(&passings).Error; err != nil {
				return err
			}
			if len(passings) == 0 {
				return nil
			}

			var firstLevel level.Level
			if err := tx.Where("game_id = ?", g.ID).Order("position ASC").Limit(1).First(&firstLevel).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrNoLevels
				}
				return err
			}
			if firstLevel.ID == 0 {
				return ErrNoLevels
			}

			now := time.Now()
			// P-2 (pass 38): создаём прогрессы пачкой (CreateInBatches) с явным
			// ON CONFLICT по (game_passing_id, level_id) — раньше был цикл
			// 2 запроса × passing и OnConflict без Columns (no-op без unique).
			progresses := make([]LevelProgress, 0, len(passings))
			for _, p := range passings {
				progresses = append(progresses, LevelProgress{
					GamePassingID: p.ID,
					LevelID:       firstLevel.ID,
					StartedAt:     now,
				})
			}
			// S-1 (pass 39): ON CONFLICT с WHERE — частичный unique-индекс
			// (game_passing_id, level_id) WHERE deleted_at IS NULL; без предиката
			// Postgres не сопоставит конфликт с частичным индексом.
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "game_passing_id"}, {Name: "level_id"}},
				Where:     clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "deleted_at IS NULL"}}},
				DoNothing: true,
			}).CreateInBatches(&progresses, levelProgressBatchSize).Error; err != nil {
				log.Error().Err(err).Uint("game_id", g.ID).Msg("CheckAutoStartGames: failed to init first levels")
				return err
			}
			// Статус прохождений пачкой.
			passingIDs := make([]uint, 0, len(passings))
			for _, p := range passings {
				passingIDs = append(passingIDs, p.ID)
			}
			if err := tx.Model(&GamePassing{}).Where("id IN ?", passingIDs).Update("status", StatusStarted).Error; err != nil {
				log.Error().Err(err).Uint("game_id", g.ID).Msg("CheckAutoStartGames: failed to update passings status")
				return err
			}
			return nil
		}); err != nil {
			log.Error().Err(err).Uint("game_id", g.ID).Msg("CheckAutoStartGames: transaction failed")
		}
	}
}
