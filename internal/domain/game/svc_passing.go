// internal/domain/game/game_passing_service.go
package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gengine-0/internal/domain/team"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Sentinel-ошибки прохождений (A-M1, pass 33): позволяют хендлерам делать
// errors.Is вместо сравнения строк.
var (
	ErrMaxTeamsReached     = errors.New("достигнут лимит команд на игру")
	ErrGameFull            = errors.New("все места в игре заняты")
	ErrApplicationClosed   = errors.New("приём заявок закрыт")
	ErrNotInPassing        = errors.New("вы не в прохождении")
	ErrPassingNotFound     = errors.New("прохождение не найдено")
	ErrAlreadyApplied      = errors.New("заявка уже подана")
	ErrStatusNotAllowed    = errors.New("невозможно перевести прохождение в этот статус")
	ErrNotCaptainOrManager = errors.New("только капитан или модератор может менять статус заявки")
)

type GamePassingService struct {
	db          *gorm.DB
	repo        GamePassingRepository
	teamService *team.TeamService
	coAuthor    *CoAuthorService
	progressSvc *LevelProgressService
	hub         *ws.RoomHub
	monitorSvc  MonitorServiceInterface
	sseMgr      *SSEManager
}

func NewGamePassingService(db *gorm.DB, ts *team.TeamService, ca *CoAuthorService, progressSvc *LevelProgressService) *GamePassingService {
	return &GamePassingService{db: db, teamService: ts, coAuthor: ca, progressSvc: progressSvc}
}

// WithRepository внедряет репозиторий прохождений (A-H3, pass 33).
func (s *GamePassingService) WithRepository(repo GamePassingRepository) *GamePassingService {
	s.repo = repo
	return s
}

// WithHub устанавливает WebSocket-хаб для broadcast-уведомлений.
func (s *GamePassingService) WithHub(hub *ws.RoomHub) *GamePassingService {
	s.hub = hub
	return s
}

// WithMonitorService устанавливает сервис мониторинга для инвалидации кэша.
func (s *GamePassingService) WithMonitorService(monitorSvc MonitorServiceInterface) *GamePassingService {
	s.monitorSvc = monitorSvc
	return s
}

// WithSSEManager устанавливает SSE-менеджер для broadcast-уведомлений.
func (s *GamePassingService) WithSSEManager(sseMgr *SSEManager) *GamePassingService {
	s.sseMgr = sseMgr
	return s
}

// Apply подаёт заявку на игру.
// Обёрнуто в транзакцию для предотвращения race condition при одновременных заявках.
func (s *GamePassingService) Apply(ctx context.Context, gameID, teamID, userID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t team.Team
		if err := tx.First(&t, teamID).Error; err != nil {
			return err
		}
		if t.CaptainID != userID {
			return errors.New("только капитан может подать заявку")
		}
		var game Game
		if err := tx.First(&game, gameID).Error; err != nil {
			return err
		}
		if game.IsDraft {
			return errors.New("нельзя подать заявку на черновик")
		}
		// S6: Проверка дедлайна регистрации
		if game.RegistrationDeadline != nil && game.RegistrationDeadline.Before(time.Now()) {
			return ErrApplicationClosed
		}
		if game.StartsAt != nil && game.StartsAt.Before(time.Now()) {
			return errors.New("игра уже началась")
		}
		var acceptedCount int64
		if err := tx.Model(&GamePassing{}).Where("game_id = ? AND status IN (?, ?)", gameID, StatusAccepted, StatusStarted).Count(&acceptedCount).Error; err != nil {
			return err
		}
		if int(acceptedCount) >= game.MaxTeamNumber {
			return ErrMaxTeamsReached
		}
		passing := GamePassing{GameID: gameID, TeamID: teamID, Status: StatusPending}
		res := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "game_id"}, {Name: "team_id"}},
			DoNothing: true,
		}).Create(&passing)
		if res.Error != nil {
			return res.Error
		}
		// C-3: RowsAffected==0 — дубликат (ON CONFLICT DO NOTHING); реальная
		// ошибка БД уже возвращена выше.
		if res.RowsAffected == 0 {
			return ErrAlreadyApplied
		}
		return nil
	})
}

// ListByGamePaginated возвращает прохождения для игры с пагинацией.
// A-H3 (pass 33): через репозиторий, а не экспортированный DB.
func (s *GamePassingService) ListByGamePaginated(ctx context.Context, gameID uint, page, perPage int) ([]GamePassing, int64, error) {
	return s.repo.ListByGamePaginated(ctx, gameID, page, perPage)
}

// ListTestPassings возвращает тестовые прохождения для игры.
func (s *GamePassingService) ListTestPassings(ctx context.Context, gameID uint, result *[]GamePassing) error {
	passings, err := s.repo.ListTestPassings(ctx, gameID)
	if err != nil {
		return err
	}
	*result = passings
	return nil
}

// UpdateStatus обновляет статус прохождения с транзакцией, блокировкой и валидацией переходов.
func (s *GamePassingService) UpdateStatus(ctx context.Context, passingID uint, status GamePassingStatus, userID uint) error {
	// Валидация переходов статусов
	validTransitions := map[GamePassingStatus][]GamePassingStatus{
		StatusPending:      {StatusAccepted, StatusRejected},
		StatusAccepted:     {StatusStarted},
		StatusStarted:      {StatusFinished, StatusDisqualified},
		StatusFinished:     {},
		StatusRejected:     {},
		StatusDisqualified: {},
		StatusTesting:      {StatusFinished},
	}

	var currentStatus GamePassingStatus
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// JOIN-оптимизация: passing + game в 1 SQL-запросе внутри транзакции
		var passing GamePassing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Joins("Game").First(&passing, passingID).Error; err != nil {
			return err
		}
		currentStatus = passing.Status

		// passing.Game загружен через JOIN
		ok, err := s.coAuthor.HasPermissionTx(tx, passing.GameID, userID, RoleModerator)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotCaptainOrManager
		}

		// Проверка допустимости перехода
		allowedTargets := validTransitions[currentStatus]
		if len(allowedTargets) == 0 {
			return fmt.Errorf("невозможно перейти из %s в %s: %w", currentStatus, status, ErrStatusNotAllowed)
		}
		allowed := false
		for _, st := range allowedTargets {
			if st == status {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("невозможно перейти из %s в %s: %w", currentStatus, status, ErrStatusNotAllowed)
		}

		passing.Status = status
		return tx.Save(&passing).Error
	})

	if err != nil {
		return err
	}

	return nil
}

// StartGame запускает игру для прохождения.
func (s *GamePassingService) StartGame(ctx context.Context, passingID, userID uint) error {
	var gameID uint
	var teamName string

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var passing GamePassing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&passing, passingID).Error; err != nil {
			return err
		}
		var t team.Team
		if err := tx.First(&t, passing.TeamID).Error; err != nil {
			return err
		}
		isCaptain := (t.CaptainID == userID)
		if !isCaptain {
			// Проверка прав ВНУТРИ транзакции (предотвращает race condition)
			ok, err := s.coAuthor.HasPermissionTx(tx, passing.GameID, userID, RoleModerator)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("только капитан или автор/модератор может начать игру")
			}
		}
		if passing.Status != StatusAccepted {
			return errors.New("игра ещё не принята или уже началась")
		}
		passing.Status = StatusStarted
		if err := tx.Save(&passing).Error; err != nil {
			return err
		}
		if err := s.progressSvc.InitFirstLevelWithTx(ctx, tx, passingID); err != nil {
			return err
		}
		// Сохраняем данные для broadcast после транзакции
		gameID = passing.GameID
		teamName = t.Name
		return nil
	})

	if err != nil {
		return err
	}

	// L4: Отправляем WebSocket-уведомление ПОСЛЕ фиксации транзакции
	// L5: Используем контекст с таймаутом правильно
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.broadcastGameStart(timeoutCtx, gameID, passingID, teamName)
	// L6: Отправляем SSE-уведомление о старте игры
	s.broadcastSSEEvent(gameID, "game_started", map[string]any{
		"game_id":    gameID,
		"passing_id": passingID,
		"team_name":  teamName,
		"timestamp":  time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	})

	return nil
}

// broadcastGameStart отправляет WebSocket-уведомление о старте игры всем клиентам мониторинга.
func (s *GamePassingService) broadcastGameStart(ctx context.Context, gameID, passingID uint, teamName string) {
	if s.hub == nil {
		return
	}

	// Проверяем, не отменён ли контекст
	select {
	case <-ctx.Done():
		return
	default:
	}

	// Инвалидируем кэш мониторинга
	if s.monitorSvc != nil {
		s.monitorSvc.InvalidateCache(gameID)
	}

	// Формируем JSON-уведомление
	notification := map[string]any{
		"type":       "game_started",
		"game_id":    gameID,
		"passing_id": passingID,
		"team_name":  teamName,
		"timestamp":  time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}

	data, err := json.Marshal(notification)
	if err != nil {
		log.Error().Err(err).Uint("game", gameID).Msg("GamePassingService.broadcastGameStart: failed to marshal notification")
		return
	}

	// Отправляем в комнату мониторинга игры
	roomID := strconv.Itoa(int(gameID))
	s.hub.BroadcastToRoom(roomID, data)
	log.Info().Uint("game", gameID).Uint("passing", passingID).Str("team", teamName).Msg("GamePassingService: game started notification broadcast")
}

// broadcastSSEEvent отправляет SSE-уведомление всем подписчикам игры.
func (s *GamePassingService) broadcastSSEEvent(gameID uint, eventType string, data any) {
	if s.sseMgr == nil {
		return
	}
	s.sseMgr.Broadcast(gameID, eventType, data)
}

// GetTeamsByCaptain возвращает команды, где пользователь является капитаном.
// Этот метод добавлен для использования в хендлере, чтобы избежать прямого доступа к teamService.
func (s *GamePassingService) GetTeamsByCaptain(ctx context.Context, userID uint) ([]team.Team, error) {
	return s.teamService.GetTeamsByCaptain(ctx, userID)
}
