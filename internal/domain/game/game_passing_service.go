// internal/domain/game/game_passing_service.go
package game

import (
	"context"
	"errors"

	"gengine-0/internal/domain/team"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type GamePassingService struct {
	DB          *gorm.DB
	teamService *team.TeamService
	coAuthor    *CoAuthorService
}

func NewGamePassingService(db *gorm.DB, ts *team.TeamService, ca *CoAuthorService) *GamePassingService {
	return &GamePassingService{DB: db, teamService: ts, coAuthor: ca}
}

// Apply подаёт заявку на игру.
func (s *GamePassingService) Apply(ctx context.Context, gameID, teamID, userID uint) error {
	var t team.Team
	if err := s.DB.WithContext(ctx).First(&t, teamID).Error; err != nil {
		return err
	}
	if t.CaptainID != userID {
		return errors.New("только капитан может подать заявку")
	}
	var game Game
	if err := s.DB.WithContext(ctx).First(&game, gameID).Error; err != nil {
		return err
	}
	if game.IsDraft {
		return errors.New("нельзя подать заявку на черновик")
	}
	var existing GamePassing
	if err := s.DB.WithContext(ctx).Where("game_id = ? AND team_id = ?", gameID, teamID).First(&existing).Error; err == nil {
		return errors.New("заявка уже подана")
	}
	passing := GamePassing{GameID: gameID, TeamID: teamID, Status: StatusPending}
	return s.DB.WithContext(ctx).Create(&passing).Error
}

// ListByGame возвращает все прохождения для игры.
func (s *GamePassingService) ListByGame(ctx context.Context, gameID uint) ([]GamePassing, error) {
	var passings []GamePassing
	err := s.DB.WithContext(ctx).Preload("Team.Captain").Where("game_id = ?", gameID).Find(&passings).Error
	return passings, err
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
	ok, err := s.coAuthor.HasPermission(passing.GameID, userID, "moderator")
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
	var passing GamePassing
	if err := s.DB.WithContext(ctx).First(&passing, passingID).Error; err != nil {
		return err
	}
	var t team.Team
	if err := s.DB.WithContext(ctx).First(&t, passing.TeamID).Error; err != nil {
		return err
	}
	isCaptain := (t.CaptainID == userID)
	if !isCaptain {
		ok, err := s.coAuthor.HasPermission(passing.GameID, userID, "moderator")
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
	if err := s.DB.WithContext(ctx).Save(&passing).Error; err != nil {
		return err
	}
	if err := NewLevelProgressService(s.DB).InitFirstLevel(ctx, passingID); err != nil {
		log.Error().Err(err).Uint("passing", passingID).Msg("StartGame: InitFirstLevel failed")
	}
	return nil
}

// GetTeamsByCaptain возвращает команды, где пользователь является капитаном.
// Этот метод добавлен для использования в хендлере, чтобы избежать прямого доступа к teamService.
func (s *GamePassingService) GetTeamsByCaptain(ctx context.Context, userID uint) ([]team.Team, error) {
	return s.teamService.GetTeamsByCaptain(ctx, userID)
}
