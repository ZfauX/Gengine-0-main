package game

import (
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

func (s *GamePassingService) Apply(gameID, teamID, userID uint) error {
	var t team.Team
	if err := s.DB.First(&t, teamID).Error; err != nil {
		return err
	}
	if t.CaptainID != userID {
		return errors.New("только капитан может подать заявку")
	}
	var game Game
	if err := s.DB.First(&game, gameID).Error; err != nil {
		return err
	}
	if game.IsDraft {
		return errors.New("нельзя подать заявку на черновик")
	}
	var existing GamePassing
	if err := s.DB.Where("game_id = ? AND team_id = ?", gameID, teamID).First(&existing).Error; err == nil {
		return errors.New("заявка уже подана")
	}
	passing := GamePassing{GameID: gameID, TeamID: teamID, Status: StatusPending}
	return s.DB.Create(&passing).Error
}

func (s *GamePassingService) ListByGame(gameID uint) ([]GamePassing, error) {
	var passings []GamePassing
	err := s.DB.Preload("Team.Captain").Where("game_id = ?", gameID).Find(&passings).Error
	return passings, err
}

func (s *GamePassingService) UpdateStatus(passingID uint, status GamePassingStatus, userID uint) error {
	var passing GamePassing
	if err := s.DB.First(&passing, passingID).Error; err != nil {
		return err
	}
	var g Game
	if err := s.DB.First(&g, passing.GameID).Error; err != nil {
		return err
	}
	ok, _ := s.coAuthor.HasPermission(passing.GameID, userID, "moderator")
	if !ok {
		return errors.New("только автор или модератор может менять статус заявки")
	}
	passing.Status = status
	return s.DB.Save(&passing).Error
}

func (s *GamePassingService) StartGame(passingID, userID uint) error {
	var passing GamePassing
	if err := s.DB.First(&passing, passingID).Error; err != nil {
		return err
	}
	var t team.Team
	if err := s.DB.First(&t, passing.TeamID).Error; err != nil {
		return err
	}
	isCaptain := (t.CaptainID == userID)
	if !isCaptain {
		ok, _ := s.coAuthor.HasPermission(passing.GameID, userID, "moderator")
		if !ok {
			return errors.New("только капитан или автор/модератор может начать игру")
		}
	}
	if passing.Status != StatusAccepted {
		return errors.New("игра ещё не принята или уже началась")
	}
	passing.Status = StatusStarted
	if err := s.DB.Save(&passing).Error; err != nil {
		return err
	}
	if err := NewLevelProgressService(s.DB).InitFirstLevel(passingID); err != nil {
		log.Error().Err(err).Uint("passing", passingID).Msg("StartGame: InitFirstLevel failed")
	}
	return nil
}