// internal/domain/game/game_passing_service.go
package game

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"gengine-0/internal/domain/team"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GamePassingService struct {
	DB          *gorm.DB
	teamService *team.TeamService
	coAuthor    *CoAuthorService
	progressSvc *LevelProgressService
	hub         *ws.RoomHub
	monitorSvc  *MonitorService
}

func NewGamePassingService(db *gorm.DB, ts *team.TeamService, ca *CoAuthorService, progressSvc *LevelProgressService) *GamePassingService {
	return &GamePassingService{DB: db, teamService: ts, coAuthor: ca, progressSvc: progressSvc}
}

// WithHub устанавливает WebSocket-хаб для broadcast-уведомлений.
func (s *GamePassingService) WithHub(hub *ws.RoomHub) *GamePassingService {
	s.hub = hub
	return s
}

// WithMonitorService устанавливает сервис мониторинга для инвалидации кэша.
func (s *GamePassingService) WithMonitorService(monitorSvc *MonitorService) *GamePassingService {
	s.monitorSvc = monitorSvc
	return s
}

// Apply подаёт заявку на игру.
// Обёрнуто в транзакцию для предотвращения race condition при одновременных заявках.
func (s *GamePassingService) Apply(ctx context.Context, gameID, teamID, userID uint) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		var acceptedCount int64
		if err := tx.Model(&GamePassing{}).Where("game_id = ? AND status IN (?, ?)", gameID, StatusAccepted, StatusStarted).Count(&acceptedCount).Error; err != nil {
			return err
		}
		if int(acceptedCount) >= game.MaxTeamNumber {
			return errors.New("достигнут лимит команд на игру")
		}
		var existing GamePassing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_id = ? AND team_id = ?", gameID, teamID).
			First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		} else {
			return errors.New("заявка уже подана")
		}
		passing := GamePassing{GameID: gameID, TeamID: teamID, Status: StatusPending}
		return tx.Create(&passing).Error
	})
}

// ListByGame возвращает все прохождения для игры.
func (s *GamePassingService) ListByGame(ctx context.Context, gameID uint) ([]GamePassing, error) {
	var passings []GamePassing
	err := s.DB.WithContext(ctx).Preload("Team.Captain").Where("game_id = ?", gameID).Find(&passings).Error
	return passings, err
}

// ListTestPassings возвращает тестовые прохождения для игры.
func (s *GamePassingService) ListTestPassings(ctx context.Context, gameID uint, result *[]GamePassing) error {
	return s.DB.WithContext(ctx).Where("game_id = ? AND status = ?", gameID, StatusTesting).Find(result).Error
}

// UpdateStatus обновляет статус прохождения.
func (s *GamePassingService) UpdateStatus(ctx context.Context, passingID uint, status GamePassingStatus, userID uint) error {
	var passing GamePassing
	if err := s.DB.WithContext(ctx).First(&passing, passingID).Error; err != nil {
		return err
	}
	var g Game
	if err := s.DB.WithContext(ctx).First(&g, passing.GameID).Error; err != nil {
		return err
	}
	ok, err := s.coAuthor.HasPermission(ctx, passing.GameID, userID, RoleModerator)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("только автор или модератор может менять статус заявки")
	}
	passing.Status = status
	return s.DB.WithContext(ctx).Save(&passing).Error
}

// StartGame запускает игру для прохождения.
func (s *GamePassingService) StartGame(ctx context.Context, passingID, userID uint) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			ok, err := s.coAuthor.HasPermission(ctx, passing.GameID, userID, RoleModerator)
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
			log.Error().Err(err).Uint("passing", passingID).Msg("StartGame: InitFirstLevel failed")
		}

		// Отправляем WebSocket-уведомление о старте игры
		s.broadcastGameStart(ctx, passing.GameID, passingID, t.Name)

		return nil
	})
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

	// Используем контекст с таймаутом для предотвращения зависания
	_, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Инвалидируем кэш мониторинга
	if s.monitorSvc != nil {
		s.monitorSvc.InvalidateCache(gameID)
	}

	// Формируем JSON-уведомление
	notification := map[string]interface{}{
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

// GetTeamsByCaptain возвращает команды, где пользователь является капитаном.
// Этот метод добавлен для использования в хендлере, чтобы избежать прямого доступа к teamService.
func (s *GamePassingService) GetTeamsByCaptain(ctx context.Context, userID uint) ([]team.Team, error) {
	return s.teamService.GetTeamsByCaptain(ctx, userID)
}
