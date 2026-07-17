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
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GamePlayService отвечает за игровой процесс: отправку кодов, файлов, подсказок,
// работу с чёрным ящиком и тестовый режим.
type GamePlayService struct {
	db          *gorm.DB
	attemptSvc  *AttemptService
	progressSvc *LevelProgressService
	monitorSvc  *MonitorService
	hub         *ws.RoomHub
	coAuthorSvc *CoAuthorService
	cfg         *config.Config
}

// NewGamePlayService создаёт новый экземпляр GamePlayService.
func NewGamePlayService(
	db *gorm.DB,
	attemptSvc *AttemptService,
	progressSvc *LevelProgressService,
	monitorSvc *MonitorService,
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

// SubmitCode обрабатывает отправку текстового кода с транзакцией и блокировкой.
func (s *GamePlayService) SubmitCode(ctx context.Context, passingID, userID uint, code string) (*SubmitResult, error) {
	var result *SubmitResult

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Блокируем прогресс текущего уровня
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		// 2. Проверяем членство в команде
		if err := checkTeamMembership(tx, passingID, userID); err != nil {
			return err
		}

		// 3. Загружаем passing для получения GameID
		var passing GamePassing
		if err := tx.First(&passing, passingID).Error; err != nil {
			return err
		}

		// 4. Отправляем код через attemptService с передачей tx
		att, success, err := s.attemptSvc.SubmitCodeWithTx(tx, progress, code)
		if err != nil {
			return err
		}

		if success {
			// 5. Завершаем уровень — при завершении последнего уровня вызываем расчёт результатов
			gameID := passing.GameID
			if err := CompleteLevel(tx, progress, nil); err != nil {
				return err
			}
			// Сохраняем gameID для вызова после транзакции
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

	// Отправляем WebSocket-обновления и рассчитываем результаты ПОСЛЕ транзакции
	if result != nil && result.Attempt != nil {
		s.broadcastSnapshot(ctx, passingID)
		// Рассчитываем результаты, если игра завершена (на основе gameID)
		if result.GameID != 0 {
			if s.monitorSvc != nil {
				if err := s.monitorSvc.CalculateResults(ctx, result.GameID); err != nil {
					log.Error().Err(err).Uint("game_id", result.GameID).Msg("SubmitCode: CalculateResults failed")
				}
			}
		}
	}

	return result, nil
}

// SubmitFile обрабатывает файловый ответ с транзакцией и блокировкой.
func (s *GamePlayService) SubmitFile(ctx context.Context, passingID, userID uint, filePath string) (*Attempt, error) {
	var attempt *Attempt

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		if err := checkTeamMembership(tx, passingID, userID); err != nil {
			return err
		}

		// Q5: Проверяем, что уровень поддерживает файловые ответы
		var lvl level.Level
		if err := tx.Where("id = ?", progress.LevelID).First(&lvl).Error; err != nil {
			return err
		}
		if lvl.Type != level.TypeFileUpload {
			return errors.New("этот уровень не поддерживает файловые ответы")
		}

		att, err := s.attemptSvc.SubmitFileWithTx(tx, progress, filePath)
		if err != nil {
			return err
		}
		attempt = att

		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("загружен файл: %s", filepath.Base(filepath.Clean(filePath))),
		}
		return tx.Create(&logEntry).Error
	})

	if err != nil {
		return nil, err
	}

	if attempt != nil {
		s.broadcastSnapshot(ctx, passingID)
	}
	return attempt, nil
}

// UseHint использует подсказку с транзакцией и блокировкой.
func (s *GamePlayService) UseHint(ctx context.Context, passingID, userID uint) (string, error) {
	var hintText string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		if err := checkTeamMembership(tx, passingID, userID); err != nil {
			return err
		}

		var passing GamePassing
		if err := tx.First(&passing, passingID).Error; err != nil {
			return err
		}
		if passing.Status != StatusStarted {
			return errors.New("игра не запущена")
		}
		var settings GameSetting
		if err := tx.Where("game_id = ?", passing.GameID).First(&settings).Error; err != nil {
			settings = GameSetting{AllowHints: true, HintPenaltySeconds: 300, MaxHints: 3}
		}

		if !settings.AllowHints {
			return errors.New("подсказки запрещены")
		}
		if settings.MaxHints > 0 && progress.HintsUsed >= settings.MaxHints {
			return errors.New("лимит подсказок исчерпан")
		}

		progress.HintsUsed++
		penalty := settings.HintPenaltySeconds * progress.HintsUsed
		progress.PenaltySeconds += penalty
		if err := tx.Save(progress).Error; err != nil {
			return err
		}

		// Получаем текст подсказки из вопросов уровня
		var lvl level.Level
		if err := tx.Where("id = ?", progress.LevelID).First(&lvl).Error; err != nil {
			return err
		}
		if len(lvl.Questions) > 0 {
			hintText = lvl.Questions[0].Hint
		}

		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("использована подсказка (+%d сек)", penalty),
		}
		if err := tx.Create(&logEntry).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	// Отправляем WebSocket-обновление после фиксации транзакции
	s.broadcastSnapshot(ctx, passingID)
	return hintText, nil
}

// AcceptBlackboxAnswer подтверждает ответ на уровне "чёрный ящик" с транзакцией и блокировкой.
func (s *GamePlayService) AcceptBlackboxAnswer(ctx context.Context, passingID, userID uint) error {
	var gameID uint

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		// Проверяем, что уровень требует подтверждения (чёрный ящик)
		var lvl level.Level
		if err := tx.Where("id = ?", progress.LevelID).First(&lvl).Error; err != nil {
			return err
		}
		if lvl.Type != level.TypeBlackbox {
			return errors.New("подтверждение ответа доступно только для уровней типа чёрный ящик")
		}

		var passing GamePassing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&passing, passingID).Error; err != nil {
			return err
		}
		gameID = passing.GameID
		var game Game
		if err := tx.First(&game, passing.GameID).Error; err != nil {
			return err
		} else if game.AuthorID != userID {
			return errors.New("только автор может подтвердить ответ")
		}

		if err := s.attemptSvc.AcceptPendingAttemptWithTx(tx, progress); err != nil {
			return err
		}

		if err := CompleteLevel(tx, progress, nil); err != nil {
			return err
		}

		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       "автор принял ответ",
		}
		return tx.Create(&logEntry).Error
	})

	if err != nil {
		return err
	}

	// Рассчитываем результаты ПОСЛЕ транзакции
	if s.monitorSvc != nil {
		if err := s.monitorSvc.CalculateResults(ctx, gameID); err != nil {
			log.Error().Err(err).Uint("game_id", gameID).Msg("AcceptBlackboxAnswer: CalculateResults failed")
		}
	}

	s.broadcastSnapshot(ctx, passingID)
	return nil
}

// StartTesting создаёт тестовое прохождение с транзакцией.
func (s *GamePlayService) StartTesting(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	var passing *GamePassing

	// Проверка прав: только автор или модератор может запускать тестирование
	ok, err := s.coAuthorSvc.HasPermission(ctx, gameID, userID, RoleModerator)
	if err != nil {
		return nil, fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if !ok {
		return nil, errors.New("только автор или модератор может запускать тестирование")
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Проверяем наличие уровней в игре
		var levelCount int64
		if err := tx.Model(&level.Level{}).Where("game_id = ?", gameID).Count(&levelCount).Error; err != nil {
			return err
		}
		if levelCount == 0 {
			return errors.New("игра не содержит уровней")
		}

		// Проверяем, не существует ли уже тестовое прохождение для этой игры и пользователя
		var existing GamePassing
		if err := tx.Where("game_id = ? AND status = ? AND team_id::text LIKE ?", gameID, StatusTesting, "_test_%").First(&existing).Error; err == nil {
			return fmt.Errorf("тестовое прохождение уже существует")
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		// Создаём тестовую команду
		testTeam := team.Team{
			Name:      fmt.Sprintf("_test_%d", userID),
			CaptainID: userID,
		}
		if err := tx.Create(&testTeam).Error; err != nil {
			return err
		}

		passing = &GamePassing{
			GameID: gameID,
			TeamID: testTeam.ID,
			Status: StatusTesting,
		}
		if err := tx.Create(passing).Error; err != nil {
			return err
		}
		metrics.IncGamePassings(string(StatusTesting))

		// Инициализируем первый уровень с транзакцией
		txProgressSvc := NewLevelProgressService(tx)
		return txProgressSvc.InitFirstLevel(ctx, passing.ID)
	})

	if err != nil {
		return nil, err
	}
	return passing, nil
}

// SubmitTestCode отправляет код в тестовом режиме с транзакцией.
func (s *GamePlayService) SubmitTestCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error) {
	var attempt *Attempt

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		attempt = &Attempt{
			LevelProgressID: progress.ID,
			Code:            code,
			Success:         true,
		}
		if err := tx.Create(attempt).Error; err != nil {
			return err
		}

		if err := CompleteLevel(tx, progress, nil); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	s.broadcastSnapshot(ctx, passingID)
	return attempt, nil
}

// SkipLevelTest пропускает уровень в тестовом режиме с транзакцией.
func (s *GamePlayService) SkipLevelTest(ctx context.Context, passingID, userID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		// Проверяем, что пользователь — автор или соавтор игры
		var passing GamePassing
		if err := tx.First(&passing, passingID).Error; err != nil {
			return err
		}

		ok, err := s.coAuthorSvc.HasPermission(ctx, passing.GameID, userID, RoleModerator)
		if err != nil {
			return fmt.Errorf("ошибка проверки прав: %w", err)
		}
		if !ok {
			return errors.New("доступ запрещён: только автор или соавтор может пропускать уровни")
		}

		now := time.Now()
		progress.FinishedAt = &now
		if err := tx.Save(progress).Error; err != nil {
			return err
		}
		return AdvanceToNextLevel(tx, passingID, progress.LevelID, nil)
	})
}

// broadcastSnapshot отправляет обновление мониторинга в WebSocket.
func (s *GamePlayService) broadcastSnapshot(ctx context.Context, passingID uint) {
	if s.monitorSvc == nil || s.hub == nil {
		return
	}
	// Проверяем, не отменён ли контекст
	select {
	case <-ctx.Done():
		return
	default:
	}

	// Используем контекст с таймаутом для предотвращения зависания
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var passing GamePassing
	if err := s.db.WithContext(timeoutCtx).Select("game_id").First(&passing, passingID).Error; err != nil {
		log.Error().Err(err).Uint("passing", passingID).Msg("GamePlayService.broadcastSnapshot: failed to find passing")
		return
	}
	gameID := passing.GameID
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
// Оптимизация: использует Preload для объединения запросов passing/settings/progress/attempts.
func (s *GamePlayService) GetGameplayData(ctx context.Context, passingID uint) (*GameplayData, error) {
	var passing GamePassing
	if err := s.db.WithContext(ctx).
		Preload("Team").
		Preload("Game.GameSetting").
		First(&passing, passingID).Error; err != nil {
		return nil, err
	}

	var settings GameSetting
	if passing.Game.GameSetting.ID != 0 {
		settings = passing.Game.GameSetting
	} else if err := s.db.WithContext(ctx).Where("game_id = ?", passing.GameID).First(&settings).Error; err != nil {
		// settings не обязательны — при отсутствии или ошибке используются значения по умолчанию
		log.Debug().Err(err).Uint("game_id", passing.GameID).Msg("GetGameplayData: settings not found, using defaults")
	}

	var progress LevelProgress
	err := s.db.WithContext(ctx).
		Preload("Level.Questions.Answers").
		Where("game_passing_id = ? AND finished_at IS NULL", passingID).
		First(&progress).Error
	if err != nil {
		return nil, err
	}

	// Оптимизация: загружаем attempts в одном запросе с LIMIT для последних попыток
	var attempts []Attempt
	if err := s.db.WithContext(ctx).
		Where("level_progress_id = ?", progress.ID).
		Order("created_at DESC").
		Limit(50).
		Find(&attempts).Error; err != nil {
		log.Error().Err(err).Uint("progress_id", progress.ID).Msg("GetGameplayData: failed to fetch attempts")
	}

	var votingSession gameBlackboxVotingSession
	votingActive := s.db.WithContext(ctx).
		Where("game_passing_id = ? AND level_id = ? AND is_open = true", passingID, progress.LevelID).
		First(&votingSession).Error == nil

	timeLimitSec := 0
	if settings.PerLevelTimeLimit > 0 {
		elapsed := time.Since(progress.StartedAt)
		limit := time.Duration(settings.PerLevelTimeLimit) * time.Minute
		remaining := limit - elapsed
		if remaining < 0 {
			remaining = 0
		}
		timeLimitSec = int(remaining.Seconds())
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
	if err := s.db.WithContext(ctx).Select("id", "game_id", "team_id").First(&passing, passingID).Error; err != nil {
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
