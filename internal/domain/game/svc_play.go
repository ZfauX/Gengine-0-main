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

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/cache"
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

// A-4 (pass 39): sentinel-ошибки игрового процесса — позволяют хендлерам
// различать причины (машинно-читаемо) вместо сравнения строк.
var (
	// ErrLevelNotFileUpload — уровень не поддерживает файловые ответы.
	ErrLevelNotFileUpload = errors.New("этот уровень не поддерживает файловые ответы")
	// ErrGameNotStarted — игра ещё не запущена.
	ErrGameNotStarted = errors.New("игра не запущена")
	// ErrHintsDisabled — подсказки запрещены настройками игры.
	ErrHintsDisabled = errors.New("подсказки запрещены")
	// ErrNoHintAvailable — на уровне нет подсказки.
	ErrNoHintAvailable = errors.New("на этом уровне нет подсказки")
	// ErrTestingOnly — действие доступно только для тестового прохождения
	// (L-10, pass 40: единый sentinel вместо дубликата в двух методах).
	ErrTestingOnly = errors.New("тестовый режим доступен только для тестового прохождения")
)

// GamePlayService отвечает за игровой процесс: отправку кодов, файлов, подсказок,
// работу с чёрным ящиком и тестовый режим.
type GamePlayService struct {
	db          *gorm.DB
	gameRepo    GameRepository
	passingRepo GamePassingRepository
	attemptSvc  *AttemptService
	monitorSvc  MonitorServiceInterface
	hub         *ws.RoomHub
	coAuthorSvc *CoAuthorService
	sseMgr      *SSEManager
	// cache (DEEP-REVIEW P5, pass 46): кэш статичных настроек игры.
	// Если nil — настройки читаются из БД каждый раз.
	cache cache.CacheStore
	// snapshotDispatcher дебаунсит пересчёт снапшота мониторинга (S3).
	// Если nil — пересчёт выполняется синхронно (fallback для тестов).
	snapshotDispatcher *SnapshotDispatcher
	// gameFinishedCallback вызывается при завершении последнего уровня игры
	// (начисление турнирных очков, пересчёт результатов). Настраивается из app-слоя.
	gameFinishedCallback GameCompletionCallback
}

// WithRepository внедряет репозиторий игр (A-H2, pass 33) — для типизированных
// счётчиков вместо raw SQL через db.
func (s *GamePlayService) WithRepository(repo GameRepository) *GamePlayService {
	s.gameRepo = repo
	return s
}

// WithPassingRepository внедряет репозиторий прохождений (A-H1, pass 34) —
// для read-путей GetPassingWithGame вместо s.db.
func (s *GamePlayService) WithPassingRepository(repo GamePassingRepository) *GamePlayService {
	s.passingRepo = repo
	return s
}

// NewGamePlayService создаёт новый экземпляр GamePlayService.
// A-1 (pass 36): удалены мёртвые поля cfg и progressSvc — ни один метод
// GamePlayService их не использовал (progressSvc был только в GamePassingService).
func NewGamePlayService(
	db *gorm.DB,
	attemptSvc *AttemptService,
	monitorSvc MonitorServiceInterface,
	hub *ws.RoomHub,
	coAuthorSvc *CoAuthorService,
) *GamePlayService {
	return &GamePlayService{
		db:          db,
		attemptSvc:  attemptSvc,
		monitorSvc:  monitorSvc,
		hub:         hub,
		coAuthorSvc: coAuthorSvc,
	}
}

// WithSSEManager устанавливает SSE-менеджер для broadcast-уведомлений.
func (s *GamePlayService) WithSSEManager(sseMgr *SSEManager) *GamePlayService {
	s.sseMgr = sseMgr
	return s
}

// WithCache устанавливает кэш для статичных настроек игры (DEEP-REVIEW P5).
func (s *GamePlayService) WithCache(c cache.CacheStore) *GamePlayService {
	s.cache = c
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

		// 2. Проверяем членство в команде (возвращает gameID+teamID — без повторного load passing)
		gameID, teamID, checkErr := CheckTeamMembership(tx, passingID, userID)
		if checkErr != nil {
			return checkErr
		}

		// 3. Отправляем код через attemptService с передачей tx
		att, success, submitErr := s.attemptSvc.SubmitCodeWithTx(ctx, tx, progress, code, teamID)
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
			// P-5 (pass 39): денормализованный game_id для запросов без JOIN.
			GameID:  gameID,
			LevelID: progress.LevelID,
			Message: fmt.Sprintf("код ***: %s", map[bool]string{true: "принят", false: "неверный"}[success]),
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

		gid, _, checkErr := CheckTeamMembership(tx, passingID, userID)
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
			return ErrLevelNotFileUpload
		}

		att, submitErr := s.attemptSvc.SubmitFileWithTx(ctx, tx, progress, filePath)
		if submitErr != nil {
			return submitErr
		}
		attempt = att

		logEntry := Log{
			GamePassingID: passingID,
			// P-5 (pass 39): денормализованный game_id.
			GameID:  gameID,
			LevelID: progress.LevelID,
			Message: fmt.Sprintf("загружен файл: %s", filepath.Base(filepath.Clean(filePath))),
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

		gid, _, checkErr := CheckTeamMembership(tx, passingID, userID)
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
			return ErrGameNotStarted
		}
		var settings GameSetting
		if findErr := tx.Where("game_id = ?", gameID).First(&settings).Error; findErr != nil {
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				// M-1: gameID из CheckTeamMembership (passing загружен Select("status") — GameID=0).
				settings = *defaultGameSetting(gameID)
			} else {
				return fmt.Errorf("failed to load game settings: %w", findErr)
			}
		}

		if !settings.AllowHints {
			return ErrHintsDisabled
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
			return ErrNoHintAvailable
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
			// P-5 (pass 39): денормализованный game_id.
			GameID:  gameID,
			LevelID: progress.LevelID,
			Message: fmt.Sprintf("использована подсказка (+%d сек)", penalty),
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

		// G8: принимать ответы может автор ИЛИ модератор игры (соавтор с ролью
		// moderator). Раньше проверка ограничивала только автором — модератор,
		// не состоящий в команде, не мог принять ответ «чёрного ящика».
		ok, permErr := s.coAuthorSvc.HasPermissionTx(ctx, tx, passing.GameID, userID, RoleModerator)
		if permErr != nil {
			return permErr
		}
		if !ok {
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
			// P-5 (pass 39): денормализованный game_id.
			GameID:  gameID,
			LevelID: progress.LevelID,
			Message: "автор принял ответ",
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

		// G1: тестовые маршруты не должны влиять на реальные прохождения.
		// SubmitTestCode создаёт Attempt{Success:true} и вызывает CompleteLevel —
		// это допустимо только для тестовой сессии (StatusTesting), иначе автор
		// мог бы завершить уровень реальной команды по passingID.
		if passing.Status != StatusTesting {
			return ErrTestingOnly
		}

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

		// G1: пропуск уровня допустим только для тестовой сессии, иначе автор
		// мог бы пропустить уровень реальной команды по passingID.
		if passing.Status != StatusTesting {
			return ErrTestingOnly
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
		// A-H2 (pass 33): типизированный счётчик через репозиторий (семантика
		// started+accepted сохранена — это «не завершена», в отличие от
		// CountActivePassings = started+testing для редактирования игры).
		// A-M3 (pass 34): gameRepo всегда инъектирован через wire — raw-DB
		// fallback удалён (мёртвый код).
		var active int64
		var err error
		if s.gameRepo != nil {
			active, err = s.gameRepo.CountPassingsInStatuses(timeoutCtx, gameID,
				[]GamePassingStatus{StatusStarted, StatusAccepted})
		}
		if err != nil {
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
// A-3 (pass 36): все read-запросы через репозитории (s.db только транзакции).
func (s *GamePlayService) GetGameplayData(ctx context.Context, passingID uint) (*GameplayData, error) {
	passing, err := s.passingRepo.GetByIDWithTeam(ctx, passingID)
	if err != nil {
		return nil, err
	}

	// Проверяем статус прохождения: данные должны быть доступны только для активных игр
	if passing.Status != StatusStarted && passing.Status != StatusTesting {
		return nil, ErrGameNotActive
	}

	// P-7 (pass 33): settings и progress независимы — грузим параллельно
	// (раньше settings шли последовательно после passing-preload GameSetting).
	var settings GameSetting
	var progress *LevelProgress
	var g errgroup.Group

	g.Go(func() error {
		// DEEP-REVIEW P5 (pass 46) + PASS-2 (#12): настройки игры статичны —
		// кэшируем на 60с через cacheGetJSON (работает и с in-memory, и с
		// Valkey; раньше GetOrSetWithCtx+type-assert с Valkey не хитился).
		if s.cache != nil {
			cacheKey := fmt.Sprintf("game:settings:%d", passing.GameID)
			var cachedGameSetting *GameSetting
			if cacheGetJSON(s.cache, ctx, cacheKey, &cachedGameSetting) && cachedGameSetting != nil {
				settings = *cachedGameSetting
				return nil
			}
			gs, err := s.gameRepo.GetGameSettingByGameID(ctx, passing.GameID)
			if err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					log.Error().Err(err).Uint("game_id", passing.GameID).Msg("GetGameplayData: failed to load settings, using defaults")
				}
				settings = *defaultGameSetting(passing.GameID)
				return nil
			}
			if gs == nil {
				gs = defaultGameSetting(passing.GameID)
			}
			settings = *gs
			s.cache.SetWithCtx(ctx, cacheKey, gs, 60*time.Second)
			return nil
		}
		// A-2 (pass 41): gs вместо s — раньше локальная s shadowing'ила receiver.
		gs, err := s.gameRepo.GetGameSettingByGameID(ctx, passing.GameID)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				// На не-NotFound ошибке тоже используем дефолты (C-M9), а не
				// zero-value с AllowHints=false.
				log.Error().Err(err).Uint("game_id", passing.GameID).Msg("GetGameplayData: failed to load settings, using defaults")
			}
			// settings не обязательны — используем единые значения по умолчанию (B5).
			settings = *defaultGameSetting(passing.GameID)
			return nil
		}
		if gs != nil {
			settings = *gs
		}
		return nil
	})

	g.Go(func() error {
		p, err := s.passingRepo.GetCurrentProgressWithLevel(ctx, passingID)
		if err != nil {
			return err
		}
		progress = p
		return nil
	})

	if waitErr := g.Wait(); waitErr != nil {
		return nil, waitErr
	}

	// Оптимизация (P-H4): attempts и voting session независимы — грузим
	// параллельно через errgroup вместо последовательных round-trips.
	var attempts []Attempt
	votingActive := false

	g2 := errgroup.Group{}
	g2.Go(func() error {
		atts, err := s.passingRepo.GetAttemptsByProgress(ctx, progress.ID, 50)
		if err != nil {
			log.Error().Err(err).Uint("progress_id", progress.ID).Msg("GetGameplayData: failed to fetch attempts")
			return err
		}
		attempts = atts
		return nil
	})
	g2.Go(func() error {
		_, open, err := s.passingRepo.GetOpenVotingSession(ctx, passingID, progress.LevelID)
		if err != nil {
			log.Error().Err(err).Uint("passing_id", passingID).Uint("level_id", progress.LevelID).Msg("GetGameplayData: voting session query failed")
			return err
		}
		votingActive = open
		return nil
	})
	// Ошибки фоновых запросов не должны валить страницу — данные уже есть,
	// но логируем результат ожидания, чтобы не терять сбои БД молча (G11).
	if waitErr := g2.Wait(); waitErr != nil {
		log.Error().Err(waitErr).Uint("passing_id", passingID).Msg("GetGameplayData: background queries failed")
	}

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
		Passing:      *passing,
		Level:        progress.Level,
		Settings:     settings,
		Attempts:     attempts,
		VotingActive: votingActive,
		TimeLimitSec: timeLimitSec,
	}, nil
}

// GetPassingWithGame загружает Passing с GameID для проверки прав.
// A-H1 (pass 34): через GamePassingRepository — убран raw s.db read.
func (s *GamePlayService) GetPassingWithGame(ctx context.Context, passingID uint) (*GamePassing, error) {
	return s.passingRepo.GetByIDWithGame(ctx, passingID)
}

// IsTeamMember проверяет, является ли пользователь участником команды.
// A-H1 (pass 34): через GameRepository.IsTeamMember — убран raw s.db read.
func (s *GamePlayService) IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error) {
	return s.gameRepo.IsTeamMember(ctx, teamID, userID)
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
