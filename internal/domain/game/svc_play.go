// internal/domain/game/game_play_service.go
package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/metrics"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrGameNotActive возвращается при попытке получить данные для неактивного прохождения.
var ErrGameNotActive = errors.New("игра не активна")

// ErrHintLimitReached — лимит подсказок исчерпан. Sentinel позволяет хендлерам
// передавать клиенту машинно-читаемый код ошибки (UX16, без сравнения строк).
var ErrHintLimitReached = errors.New("лимит подсказок исчерпан")

// ErrOnlyAuthor — действие доступно только автору игры.
var ErrOnlyAuthor = errors.New("только автор может подтвердить ответ")

// ErrBlackboxOnly — подтверждение доступно только для уровней типа «чёрный ящик».
var ErrBlackboxOnly = errors.New("подтверждение ответа доступно только для уровней типа чёрный ящик")

// ErrNotInGame — пользователь не участвует в прохождении игры.
var ErrNotInGame = errors.New("вы не участвуете в этом прохождении")

// GamePlayService отвечает за игровой процесс: отправку кодов, файлов, подсказок,
// работу с чёрным ящиком и тестовый режим.
type GamePlayService struct {
	db          *gorm.DB
	attemptSvc  *AttemptService
	progressSvc *LevelProgressService
	monitorSvc  MonitorServiceInterface
	hub         *ws.RoomHub
	coAuthorSvc *CoAuthorService
	cfg         *config.Config
	sseMgr      *SSEManager
	// snapshotDispatcher дебаунсит пересчёт снапшота мониторинга (S3).
	// Если nil — пересчёт выполняется синхронно (fallback для тестов).
	snapshotDispatcher *SnapshotDispatcher
	// gameFinishedCallback вызывается при завершении последнего уровня игры
	// (начисление турнирных очков, пересчёт результатов). Настраивается из app-слоя.
	gameFinishedCallback GameCompletionCallback
}

// NewGamePlayService создаёт новый экземпляр GamePlayService.
func NewGamePlayService(
	db *gorm.DB,
	attemptSvc *AttemptService,
	progressSvc *LevelProgressService,
	monitorSvc MonitorServiceInterface,
	hub *ws.RoomHub,
	coAuthorSvc *CoAuthorService,
	cfg *config.Config,
) *GamePlayService {
	return &GamePlayService{
		db:          db,
		attemptSvc:  attemptSvc,
		progressSvc: progressSvc,
		monitorSvc:  monitorSvc,
		hub:         hub,
		coAuthorSvc: coAuthorSvc,
		cfg:         cfg,
	}
}

// WithSSEManager устанавливает SSE-менеджер для broadcast-уведомлений.
func (s *GamePlayService) WithSSEManager(sseMgr *SSEManager) *GamePlayService {
	s.sseMgr = sseMgr
	return s
}

// WithGameFinishedCallback устанавливает колбэк завершения игры (турнирные очки и пр.).
func (s *GamePlayService) WithGameFinishedCallback(cb GameCompletionCallback) *GamePlayService {
	s.gameFinishedCallback = cb
	return s
}

// finishCallback возвращает замыкание для CompleteLevel, вызывающее общий колбэк.
// Использует context.WithoutCancel, чтобы начисление очков завершилось даже
// при отмене контекста запроса (клиент отключился).
func (s *GamePlayService) finishCallback(ctx context.Context, gameID uint) func() {
	if s.gameFinishedCallback == nil {
		return nil
	}
	return func() {
		s.gameFinishedCallback(context.WithoutCancel(ctx), gameID)
	}
}

// SubmitCode обрабатывает отправку текстового кода с транзакцией и блокировкой.
func (s *GamePlayService) SubmitCode(ctx context.Context, passingID, userID uint, code string) (*SubmitResult, error) {
	var result *SubmitResult
	var savedGameID uint
	var savedLevelID uint
	// onCommit (колбэк завершения игры) вызываем ПОСЛЕ коммита транзакции.
	var onCommitFn func()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Блокируем прогресс текущего уровня
		progress, progressErr := GetCurrentProgressForUpdate(tx, passingID)
		if progressErr != nil {
			return progressErr
		}

		// 2. Проверяем членство в команде (возвращает gameID — без повторного load passing)
		gameID, checkErr := CheckTeamMembership(tx, passingID, userID)
		if checkErr != nil {
			return checkErr
		}

		// 3. Отправляем код через attemptService с передачей tx
		att, success, submitErr := s.attemptSvc.SubmitCodeWithTx(ctx, tx, progress, code)
		if submitErr != nil {
			return submitErr
		}

		if success {
			// 5. Завершаем уровень (gameID получен из CheckTeamMembership)
			onCommit, completeErr := CompleteLevel(tx, progress, s.finishCallback(ctx, gameID))
			if completeErr != nil {
				return completeErr
			}
			onCommitFn = onCommit
			savedGameID = gameID
			savedLevelID = progress.LevelID
			result = &SubmitResult{Attempt: att, GameID: gameID}
		} else {
			result = &SubmitResult{Attempt: att, GameID: 0}
		}

		// 6. Сохраняем лог
		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("код ***: %s", map[bool]string{true: "принят", false: "неверный"}[success]),
		}
		return tx.Create(&logEntry).Error
	})

	if err != nil {
		return nil, err
	}

	// Колбэк завершения игры — строго после коммита (B1-порядок: расчёт мест
	// раньше начисления турнирных очков выполняется внутри onGameFinished).
	if onCommitFn != nil {
		onCommitFn()
	}

	// Отправляем обновления ПОСЛЕ коммита транзакции.
	// Тяжёлый пересчёт снапшота и результатов — в дебаунс-воркере (S3).
	// В тестах воркер не установлен → синхронный fallback.
	if result != nil && result.Attempt != nil {
		if result.GameID != 0 {
			s.broadcastLevelComplete(savedGameID, passingID, savedLevelID)
		}
		if result.GameID != 0 {
			s.scheduleSnapshot(result.GameID)
		}
	}

	return result, nil
}

// SubmitFile обрабатывает файловый ответ с транзакцией и блокировкой.
func (s *GamePlayService) SubmitFile(ctx context.Context, passingID, userID uint, filePath string) (*Attempt, error) {
	var attempt *Attempt
	var gameID uint

	fileErr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		progress, progressErr := GetCurrentProgressForUpdate(tx, passingID)
		if progressErr != nil {
			return progressErr
		}

		gid, checkErr := CheckTeamMembership(tx, passingID, userID)
		if checkErr != nil {
			return checkErr
		}
		gameID = gid

		// Q5: Проверяем, что уровень поддерживает файловые ответы
		var lvl level.Level
		if findErr := tx.Where("id = ?", progress.LevelID).First(&lvl).Error; findErr != nil {
			return findErr
		}
		if lvl.Type != level.TypeFileUpload {
			return errors.New("этот уровень не поддерживает файловые ответы")
		}

		att, submitErr := s.attemptSvc.SubmitFileWithTx(ctx, tx, progress, filePath)
		if submitErr != nil {
			return submitErr
		}
		attempt = att

		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("загружен файл: %s", filepath.Base(filepath.Clean(filePath))),
		}
		return tx.Create(&logEntry).Error
	})

	if fileErr != nil {
		return nil, fileErr
	}

	if attempt != nil && gameID != 0 {
		s.scheduleSnapshot(gameID)
	}
	return attempt, nil
}

// UseHint использует подсказку с транзакцией и блокировкой.
func (s *GamePlayService) UseHint(ctx context.Context, passingID, userID uint) (string, error) {
	var hintText string
	var gameID uint
	var levelID uint
	var hintsUsed int

	transactionErr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		progress, progressErr := GetCurrentProgressForUpdate(tx, passingID)
		if progressErr != nil {
			return progressErr
		}

		gid, checkErr := CheckTeamMembership(tx, passingID, userID)
		if checkErr != nil {
			return checkErr
		}
		gameID = gid

		// passing нужен только для проверки статуса.
		var passing GamePassing
		if findErr := tx.Select("status").First(&passing, passingID).Error; findErr != nil {
			return findErr
		}
		levelID = progress.LevelID

		if passing.Status != StatusStarted {
			return errors.New("игра не запущена")
		}
		var settings GameSetting
		if findErr := tx.Where("game_id = ?", gameID).First(&settings).Error; findErr != nil {
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				settings = *defaultGameSetting(passing.GameID)
			} else {
				return fmt.Errorf("failed to load game settings: %w", findErr)
			}
		}

		if !settings.AllowHints {
			return errors.New("подсказки запрещены")
		}
		if settings.MaxHints > 0 && progress.HintsUsed >= settings.MaxHints {
			return ErrHintLimitReached
		}

		// Получаем текст подсказки из вопросов уровня.
		// P-4: загружаем только hint первого вопроса, не всю графу Questions.
		var hintOnly struct {
			Hint string
		}
		if findErr := tx.Table("questions").Select("hint").
			Where("level_id = ?", progress.LevelID).
			Order("id ASC").Limit(1).Scan(&hintOnly).Error; findErr != nil {
			return findErr
		}
		// C-13: у уровня нет вопросов/подсказки — не списываем и не штрафуем.
		if hintOnly.Hint == "" {
			return errors.New("на этом уровне нет подсказки")
		}
		hintText = hintOnly.Hint

		progress.HintsUsed++
		hintsUsed = progress.HintsUsed
		penalty := settings.HintPenaltySeconds
		progress.PenaltySeconds += penalty
		if saveErr := tx.Save(progress).Error; saveErr != nil {
			return saveErr
		}

		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("использована подсказка (+%d сек)", penalty),
		}
		if createErr := tx.Create(&logEntry).Error; createErr != nil {
			return createErr
		}

		return nil
	})

	if transactionErr != nil {
		return "", transactionErr
	}

	// Отправляем WebSocket-обновление после фиксации транзакции (дебаунс, S3)
	if gameID != 0 {
		s.scheduleSnapshot(gameID)
	}
	// Отправляем SSE-уведомление о доступной подсказке
	if s.sseMgr != nil {
		s.sseMgr.Broadcast(gameID, "hint_available", map[string]any{
			"game_id":    gameID,
			"passing_id": passingID,
			"level_id":   levelID,
			"hints_used": hintsUsed,
		})
	}
	return hintText, nil
}

// AcceptBlackboxAnswer подтверждает ответ на уровне "чёрный ящик" с транзакцией и блокировкой.
func (s *GamePlayService) AcceptBlackboxAnswer(ctx context.Context, passingID, userID uint) error {
	var gameID uint
	var savedLevelID uint
	var onCommitFn func()

	transactionErr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		progress, progressErr := GetCurrentProgressForUpdate(tx, passingID)
		if progressErr != nil {
			return progressErr
		}

		// Проверяем, что уровень требует подтверждения (чёрный ящик)
		var lvl level.Level
		if findErr := tx.Where("id = ?", progress.LevelID).First(&lvl).Error; findErr != nil {
			return findErr
		}
		if lvl.Type != level.TypeBlackbox {
			return ErrBlackboxOnly
		}

		var passing GamePassing
		if findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&passing, passingID).Error; findErr != nil {
			return findErr
		}
		gameID = passing.GameID
		var game Game
		if findErr := tx.First(&game, passing.GameID).Error; findErr != nil {
			return findErr
		} else if game.AuthorID != userID {
			return ErrOnlyAuthor
		}

		if acceptErr := s.attemptSvc.AcceptPendingAttemptWithTx(ctx, tx, progress); acceptErr != nil {
			return acceptErr
		}

		onCommit, completeErr := CompleteLevel(tx, progress, s.finishCallback(ctx, gameID))
		if completeErr != nil {
			return completeErr
		}
		onCommitFn = onCommit
		savedLevelID = progress.LevelID

		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       "автор принял ответ",
		}
		if err := tx.Create(&logEntry).Error; err != nil {
			return err
		}
		return nil
	})

	if transactionErr != nil {
		return transactionErr
	}

	// Колбэк завершения игры — после коммита (B1).
	if onCommitFn != nil {
		onCommitFn()
	}

	// Шлём обновления ПОСЛЕ транзакции: лёгкое событие — синхронно,
	// тяжёлый пересчёт снапшота — в дебаунс-воркере (S3).
	s.broadcastLevelComplete(gameID, passingID, savedLevelID)
	if gameID != 0 {
		s.scheduleSnapshot(gameID)
	}
	return nil
}

// StartTesting создаёт тестовое прохождение с транзакцией.
func (s *GamePlayService) StartTesting(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	var passing *GamePassing

	// Проверка прав: только автор или модератор может запускать тестирование
	ok, permErr := s.coAuthorSvc.HasPermission(ctx, gameID, userID, RoleModerator)
	if permErr != nil {
		return nil, fmt.Errorf("ошибка проверки прав: %w", permErr)
	}
	if !ok {
		return nil, errors.New("только автор или модератор может запускать тестирование")
	}

	testingErr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Проверяем наличие уровней в игре
		var levelCount int64
		if countErr := tx.Model(&level.Level{}).Where("game_id = ?", gameID).Count(&levelCount).Error; countErr != nil {
			return countErr
		}
		if levelCount == 0 {
			return errors.New("игра не содержит уровней")
		}

		// Проверяем, не существует ли уже тестовое прохождение для этой игры И ЭТОГО
		// пользователя (B9). Раньше фильтр был только по game_id + шаблону имени
		// `_test_%` — второй модератор не мог запустить свой тест для той же игры.
		var existing GamePassing
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Joins("JOIN teams ON teams.id = game_passings.team_id").
			Where("game_passings.game_id = ? AND game_passings.status = ? AND teams.captain_id = ?", gameID, StatusTesting, userID).
			First(&existing).Error
		if findErr == nil {
			return fmt.Errorf("тестовое прохождение уже существует")
		} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}

		// Создаём или переиспользуем тестовую команду (C1): старые orphan-
		// команды `_test_<userID>` без прохождений не должны накапливаться.
		testTeam := team.Team{
			Name:      fmt.Sprintf("_test_%d", userID),
			CaptainID: userID,
		}
		var existingTeam team.Team
		teamErr := tx.Where("name = ?", testTeam.Name).First(&existingTeam).Error
		switch {
		case teamErr == nil:
			// #3: блокируем строку команды — два параллельных StartTesting не
			// переиспользуют одну orphan-команду одновременно (второй увидит
			// уже созданное прохождение и создаст новую команду).
			if lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existingTeam, existingTeam.ID).Error; lockErr != nil {
				return lockErr
			}
			var passingCount int64
			if countErr := tx.Model(&GamePassing{}).Where("team_id = ?", existingTeam.ID).Count(&passingCount).Error; countErr != nil {
				return countErr
			}
			if passingCount == 0 {
				// Орфан-команда — переиспользуем её.
				testTeam = existingTeam
			} else {
				// Команда используется — создаём новую (имя не уникально).
				if createErr := tx.Create(&testTeam).Error; createErr != nil {
					return createErr
				}
			}
		case errors.Is(teamErr, gorm.ErrRecordNotFound):
			if createErr := tx.Create(&testTeam).Error; createErr != nil {
				return createErr
			}
		default:
			return teamErr
		}

		passing = &GamePassing{
			GameID: gameID,
			TeamID: testTeam.ID,
			Status: StatusTesting,
		}
		if createErr := tx.Create(passing).Error; createErr != nil {
			return createErr
		}
		metrics.IncGamePassings(string(StatusTesting))

		// Инициализируем первый уровень с транзакцией
		txProgressSvc := NewLevelProgressService(tx)
		return txProgressSvc.InitFirstLevel(ctx, passing.ID)
	})

	if testingErr != nil {
		return nil, testingErr
	}
	return passing, nil
}

// SubmitTestCode отправляет код в тестовом режиме с транзакцией.
func (s *GamePlayService) SubmitTestCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error) {
	var attempt *Attempt
	var gameID uint
	var savedLevelID uint
	// #6: колбэк завершения — после коммита (паттерн onCommit, как в SubmitCode).
	var onCommitFn func()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		var passing GamePassing
		if err := tx.First(&passing, passingID).Error; err != nil {
			return err
		}
		gameID = passing.GameID

		attempt = &Attempt{
			LevelProgressID: progress.ID,
			Code:            code,
			Success:         true,
		}
		if err := tx.Create(attempt).Error; err != nil {
			return err
		}

		onCommit, completeErr := CompleteLevel(tx, progress, nil)
		if completeErr != nil {
			return completeErr
		}
		onCommitFn = onCommit
		savedLevelID = progress.LevelID
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Колбэк завершения — строго после коммита (B1-порядок).
	if onCommitFn != nil {
		onCommitFn()
	}

	if attempt != nil && savedLevelID != 0 {
		s.broadcastLevelComplete(gameID, passingID, savedLevelID)
	}

	if attempt != nil && gameID != 0 {
		s.scheduleSnapshot(gameID)
	}
	return attempt, nil
}

// SkipLevelTest пропускает уровень в тестовом режиме с транзакцией.
func (s *GamePlayService) SkipLevelTest(ctx context.Context, passingID, userID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		progress, progressErr := GetCurrentProgressForUpdate(tx, passingID)
		if progressErr != nil {
			return progressErr
		}

		// Проверяем, что пользователь — автор или соавтор игры
		var passing GamePassing
		if findErr := tx.First(&passing, passingID).Error; findErr != nil {
			return findErr
		}

		ok, permErr := s.coAuthorSvc.HasPermission(ctx, passing.GameID, userID, RoleModerator)
		if permErr != nil {
			return fmt.Errorf("ошибка проверки прав: %w", permErr)
		}
		if !ok {
			return errors.New("доступ запрещён: только автор или соавтор может пропускать уровни")
		}

		now := time.Now()
		progress.FinishedAt = &now
		if saveErr := tx.Save(progress).Error; saveErr != nil {
			return saveErr
		}

		_, advanceErr := AdvanceToNextLevel(tx, passingID, progress.LevelID, nil)
		return advanceErr
	})
}

// WithSnapshotDispatcher подключает асинхронный дебаунс-диспетчер снапшотов (S3).
// Вызывается из app-слоя после создания сервиса.
func (s *GamePlayService) WithSnapshotDispatcher(d *SnapshotDispatcher) *GamePlayService {
	s.snapshotDispatcher = d
	return s
}

// scheduleSnapshot планирует пересчёт снапшота игры. Если диспетчер не
// установлен (тесты) — выполняет синхронно, как раньше.
func (s *GamePlayService) scheduleSnapshot(gameID uint) {
	if s.snapshotDispatcher != nil {
		s.snapshotDispatcher.Schedule(gameID)
		return
	}
	s.ProcessSnapshot(context.Background(), gameID)
}

// ProcessSnapshot пересчитывает результаты и рассылает снапшот игры в WebSocket.
// Вызывается асинхронно воркером диспетчера (или синхронно в тестах).
func (s *GamePlayService) ProcessSnapshot(ctx context.Context, gameID uint) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if s.monitorSvc != nil {
		// P-M1: если активных прохождений не осталось (игра завершена), колбэк
		// финиша (onGameFinished) уже выполнил CalculateResults — не дублируем.
		var active int64
		if err := s.db.WithContext(timeoutCtx).Model(&GamePassing{}).
			Where("game_id = ? AND status IN ?", gameID, []string{string(StatusStarted), string(StatusAccepted)}).
			Count(&active).Error; err != nil {
			log.Warn().Err(err).Uint("game_id", gameID).Msg("ProcessSnapshot: failed to count active passings")
		}
		if active > 0 {
			if err := s.monitorSvc.CalculateResults(timeoutCtx, gameID); err != nil {
				log.Error().Err(err).Uint("game_id", gameID).Msg("ProcessSnapshot: CalculateResults failed")
			}
		}
	}
	s.broadcastSnapshotForGame(timeoutCtx, gameID)
}

// broadcastSnapshotForGame отправляет обновление мониторинга в WebSocket.
func (s *GamePlayService) broadcastSnapshotForGame(ctx context.Context, gameID uint) {
	if s.hub == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	if s.monitorSvc == nil {
		log.Warn().Uint("game_id", gameID).Msg("GamePlayService.broadcastSnapshot: monitorSvc is nil, skipping snapshot")
		return
	}
	s.monitorSvc.InvalidateCache(gameID)
	snapshot, err := s.monitorSvc.GetOrFetchSnapshot(ctx, gameID)
	if err != nil {
		log.Error().Err(err).Uint("game", gameID).Msg("GamePlayService.broadcastSnapshot: GetOrFetchSnapshot error")
		return
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		log.Error().Err(err).Uint("game", gameID).Msg("GamePlayService.broadcastSnapshot: failed to marshal snapshot")
		return
	}
	s.hub.BroadcastToRoom(strconv.Itoa(int(gameID)), data)
}

// GetGameplayData загружает все данные, необходимые для отображения страницы геймплея.
func (s *GamePlayService) GetGameplayData(ctx context.Context, passingID uint) (*GameplayData, error) {
	var passing GamePassing
	if err := s.db.WithContext(ctx).
		Preload("Team").
		Preload("Game.GameSetting").
		First(&passing, passingID).Error; err != nil {
		return nil, err
	}

	// Проверяем статус прохождения: данные должны быть доступны только для активных игр
	if passing.Status != StatusStarted && passing.Status != StatusTesting {
		return nil, ErrGameNotActive
	}

	var settings GameSetting
	if passing.Game.GameSetting.ID != 0 {
		settings = passing.Game.GameSetting
	} else {
		if err := s.db.WithContext(ctx).Where("game_id = ?", passing.GameID).First(&settings).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// settings не обязательны — используем единые значения по умолчанию (B5).
				settings = *defaultGameSetting(passing.GameID)
			} else {
				// На не-NotFound ошибке тоже используем дефолты (C-M9), а не
				// zero-value с AllowHints=false.
				log.Error().Err(err).Uint("game_id", passing.GameID).Msg("GetGameplayData: failed to load settings, using defaults")
				settings = *defaultGameSetting(passing.GameID)
			}
		}
	}

	var progress LevelProgress
	err := s.db.WithContext(ctx).
		Preload("Level", func(db *gorm.DB) *gorm.DB {
			return db.Select("id, game_id, name, description, type, hint, position")
		}).
		Where("game_passing_id = ? AND finished_at IS NULL", passingID).
		First(&progress).Error
	if err != nil {
		return nil, err
	}

	// Оптимизация (P-H4): attempts и voting session независимы — грузим
	// параллельно через errgroup вместо последовательных round-trips.
	var attempts []Attempt
	var votingSession GameBlackboxVotingSession
	votingActive := false

	var g errgroup.Group
	g.Go(func() error {
		if err := s.db.WithContext(ctx).
			Where("level_progress_id = ?", progress.ID).
			Order("created_at DESC").
			Limit(50).
			Find(&attempts).Error; err != nil {
			log.Error().Err(err).Uint("progress_id", progress.ID).Msg("GetGameplayData: failed to fetch attempts")
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := s.db.WithContext(ctx).
			Where("game_passing_id = ? AND level_id = ? AND is_open = true", passingID, progress.LevelID).
			First(&votingSession).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Error().Err(err).Uint("passing_id", passingID).Uint("level_id", progress.LevelID).Msg("GetGameplayData: voting session query failed")
				return err
			}
		} else {
			votingActive = true
		}
		return nil
	})
	// Ошибки фоновых запросов не должны валить страницу — данные уже есть,
	// но логируем результат ожидания.
	_ = g.Wait()

	timeLimitSec := 0
	if settings.PerLevelTimeLimit > 0 {
		// Защита от zero StartedAt (time.Since(zero) ~ 17000+ лет)
		if progress.StartedAt.IsZero() {
			timeLimitSec = int(time.Duration(settings.PerLevelTimeLimit) * time.Minute / time.Second)
		} else {
			elapsed := time.Since(progress.StartedAt)
			limit := time.Duration(settings.PerLevelTimeLimit) * time.Minute
			remaining := limit - elapsed
			if remaining < 0 {
				remaining = 0
			}
			timeLimitSec = int(remaining.Seconds())
		}
	}

	return &GameplayData{
		Passing:      passing,
		Level:        progress.Level,
		Settings:     settings,
		Attempts:     attempts,
		VotingActive: votingActive,
		TimeLimitSec: timeLimitSec,
	}, nil
}

// GetPassingWithGame загружает Passing с GameID для проверки прав.
func (s *GamePlayService) GetPassingWithGame(ctx context.Context, passingID uint) (*GamePassing, error) {
	var passing GamePassing
	// JOIN-оптимизация: passing + game в 1 SQL-запросе
	if err := s.db.WithContext(ctx).Joins("Game").First(&passing, passingID).Error; err != nil {
		return nil, err
	}
	return &passing, nil
}

// IsTeamMember проверяет, является ли пользователь участником команды.
func (s *GamePlayService) IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error) {
	var t team.Team
	if err := s.db.WithContext(ctx).First(&t, teamID).Error; err != nil {
		return false, err
	}
	if t.CaptainID == userID {
		return true, nil
	}
	var count int64
	if err := s.db.WithContext(ctx).Table("team_members").Where("team_id = ? AND user_id = ?", teamID, userID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// broadcastLevelComplete отправляет SSE-уведомление о завершении уровня.
func (s *GamePlayService) broadcastLevelComplete(gameID, passingID, levelID uint) {
	if s.sseMgr == nil {
		return
	}
	s.sseMgr.Broadcast(gameID, "level_completed", map[string]any{
		"game_id":      gameID,
		"passing_id":   passingID,
		"level_id":     levelID,
		"completed_at": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	})
}
