// internal/domain/game/game_play_service.go
package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gengine-0/internal/config"
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
func (s *GamePlayService) SubmitCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error) {
	var attempt *Attempt

	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 1. Блокируем прогресс текущего уровня
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		// 2. Проверяем членство в команде
		if err := checkTeamMembership(tx, passingID, userID); err != nil {
			return err
		}

		// 3. Отправляем код через attemptService с передачей tx
		att, success, err := s.attemptSvc.SubmitCodeWithTx(tx, progress, code)
		if err != nil {
			return err
		}
		attempt = att

		if success {
			// 4. Завершаем уровень
			if err := CompleteLevel(tx, progress); err != nil {
				return err
			}
		}

		// 5. Сохраняем лог
		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("код %s: %s", code, map[bool]string{true: "принят", false: "неверный"}[success]),
		}
		return tx.Create(&logEntry).Error
	})

	if err != nil {
		return nil, err
	}

	// Отправляем WebSocket-обновления после транзакции
	if attempt != nil {
		s.broadcastSnapshot(ctx, passingID)
	}

	return attempt, nil
}

// SubmitFile обрабатывает файловый ответ с транзакцией и блокировкой.
func (s *GamePlayService) SubmitFile(ctx context.Context, passingID, userID uint, filePath string) (*Attempt, error) {
	var attempt *Attempt

	err := s.db.Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		if err := checkTeamMembership(tx, passingID, userID); err != nil {
			return err
		}

		att, err := s.attemptSvc.SubmitFileWithTx(tx, progress, filePath)
		if err != nil {
			return err
		}
		attempt = att

		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("загружен файл: %s", filePath),
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
func (s *GamePlayService) UseHint(ctx context.Context, passingID, userID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
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

		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("использована подсказка (+%d сек)", penalty),
		}
		return tx.Create(&logEntry).Error
	})
}

// AcceptBlackboxAnswer подтверждает ответ на уровне "чёрный ящик" с транзакцией и блокировкой.
func (s *GamePlayService) AcceptBlackboxAnswer(ctx context.Context, passingID, userID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		var passing GamePassing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&passing, passingID).Error; err != nil {
			return err
		}
		var g Game
		if err := tx.First(&g, passing.GameID).Error; err != nil {
			return err
		}
		if g.AuthorID != userID {
			return errors.New("только автор может подтвердить ответ")
		}

		if err := s.attemptSvc.AcceptPendingAttemptWithTx(tx, progress); err != nil {
			return err
		}

		if err := CompleteLevel(tx, progress); err != nil {
			return err
		}

		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       "автор принял ответ",
		}
		return tx.Create(&logEntry).Error
	})
}

// StartTesting создаёт тестовое прохождение с транзакцией.
func (s *GamePlayService) StartTesting(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	var passing *GamePassing

	err := s.db.Transaction(func(tx *gorm.DB) error {
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

		progressSvc := NewLevelProgressService(tx)
		return progressSvc.InitFirstLevel(ctx, passing.ID)
	})

	if err != nil {
		return nil, err
	}
	return passing, nil
}

// SubmitTestCode отправляет код в тестовом режиме с транзакцией.
func (s *GamePlayService) SubmitTestCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error) {
	var attempt *Attempt

	err := s.db.Transaction(func(tx *gorm.DB) error {
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

		if err := CompleteLevel(tx, progress); err != nil {
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
	return s.db.Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		now := time.Now()
		progress.FinishedAt = &now
		if err := tx.Save(progress).Error; err != nil {
			return err
		}
		return AdvanceToNextLevel(tx, passingID, progress.LevelID)
	})
}

// broadcastSnapshot отправляет обновление мониторинга в WebSocket.
func (s *GamePlayService) broadcastSnapshot(ctx context.Context, passingID uint) {
	if s.monitorSvc == nil || s.hub == nil {
		return
	}
	var passing GamePassing
	if err := s.db.WithContext(ctx).Select("game_id").First(&passing, passingID).Error; err != nil {
		log.Error().Err(err).Uint("passing", passingID).Msg("GamePlayService.broadcastSnapshot: failed to find passing")
		return
	}
	gameID := passing.GameID
	s.monitorSvc.InvalidateCache(gameID)
	snapshot, err := s.monitorSvc.GetOrFetchSnapshot(gameID)
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
