// internal/domain/tournament/service.go
package tournament

import (
	"errors"
	"fmt"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/email"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type TournamentService struct {
	DB          *gorm.DB
	teamService *team.TeamService
	cfg         *config.Config
}

func NewTournamentService(db *gorm.DB, ts *team.TeamService, cfg *config.Config) *TournamentService {
	return &TournamentService{DB: db, teamService: ts, cfg: cfg}
}

// Create создаёт новый турнир.
func (s *TournamentService) Create(t *Tournament) error {
	return s.DB.Create(t).Error
}

// GetByID возвращает турнир по ID.
func (s *TournamentService) GetByID(id uint) (*Tournament, error) {
	var t Tournament
	if err := s.DB.Preload("Author").First(&t, id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// List возвращает все турниры.
func (s *TournamentService) List() ([]Tournament, error) {
	var tournaments []Tournament
	err := s.DB.Preload("Author").Order("created_at DESC").Find(&tournaments).Error
	return tournaments, err
}

// Update обновляет турнир (только автор).
func (s *TournamentService) Update(id uint, updated *Tournament, userID uint) error {
	var t Tournament
	if err := s.DB.First(&t, id).Error; err != nil {
		return err
	}
	if t.AuthorID != userID {
		return errors.New("только автор может редактировать турнир")
	}
	t.Name = updated.Name
	t.Description = updated.Description
	t.PointsForFirst = updated.PointsForFirst
	t.PointsForSecond = updated.PointsForSecond
	t.PointsForThird = updated.PointsForThird
	t.PointsForParticipation = updated.PointsForParticipation
	return s.DB.Save(&t).Error
}

// ---------- Игры турнира ----------

// AddGame добавляет игру в турнир.
func (s *TournamentService) AddGame(tournamentID, gameID, userID uint) error {
	var t Tournament
	if err := s.DB.First(&t, tournamentID).Error; err != nil {
		return err
	}
	if t.AuthorID != userID {
		return errors.New("только автор турнира может добавлять игры")
	}

	var g game.Game
	if err := s.DB.First(&g, gameID).Error; err != nil {
		return err
	}

	var count int64
	s.DB.Model(&TournamentGame{}).Where("tournament_id = ? AND game_id = ?", tournamentID, gameID).Count(&count)
	if count > 0 {
		return errors.New("игра уже в турнире")
	}

	tg := TournamentGame{
		TournamentID: tournamentID,
		GameID:       gameID,
	}
	return s.DB.Create(&tg).Error
}

// RemoveGame удаляет игру из турнира.
func (s *TournamentService) RemoveGame(tournamentID, gameID, userID uint) error {
	var t Tournament
	if err := s.DB.First(&t, tournamentID).Error; err != nil {
		return err
	}
	if t.AuthorID != userID {
		return errors.New("только автор турнира может удалять игры")
	}
	return s.DB.Where("tournament_id = ? AND game_id = ?", tournamentID, gameID).Delete(&TournamentGame{}).Error
}

// ListGames возвращает игры, входящие в турнир.
func (s *TournamentService) ListGames(tournamentID uint) ([]game.Game, error) {
	var games []game.Game
	err := s.DB.Joins("JOIN tournament_games ON tournament_games.game_id = games.id").
		Where("tournament_games.tournament_id = ?", tournamentID).
		Order("tournament_games.order_index ASC").
		Find(&games).Error
	return games, err
}

// GetAvailableGames возвращает игры автора, которые ещё не добавлены в турнир.
func (s *TournamentService) GetAvailableGames(tournamentID, userID uint) ([]game.Game, error) {
	var games []game.Game
	subQuery := s.DB.Table("tournament_games").Select("game_id").Where("tournament_id = ?", tournamentID)
	err := s.DB.Where("author_id = ? AND id NOT IN (?)", userID, subQuery).Find(&games).Error
	return games, err
}

// ---------- Заявки ----------

// Apply подаёт заявку команды на участие в турнире и отправляет email-уведомление.
func (s *TournamentService) Apply(tournamentID, teamID, userID uint) error {
	if !s.teamService.CanManageTeam(teamID, userID) {
		return errors.New("только капитан может подать заявку")
	}

	var count int64
	s.DB.Model(&TournamentTeam{}).Where("tournament_id = ? AND team_id = ?", tournamentID, teamID).Count(&count)
	if count > 0 {
		return errors.New("команда уже участвует в турнире")
	}

	tt := TournamentTeam{
		TournamentID: tournamentID,
		TeamID:       teamID,
	}
	if err := s.DB.Create(&tt).Error; err != nil {
		return err
	}

	games, err := s.ListGames(tournamentID)
	if err == nil {
		for _, g := range games {
			var existing game.GamePassing
			err := s.DB.Where("game_id = ? AND team_id = ?", g.ID, teamID).First(&existing).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				passing := game.GamePassing{
					GameID: g.ID,
					TeamID: teamID,
					Status: game.StatusPending,
				}
				_ = s.DB.Create(&passing)
			}
		}
	}

	if s.cfg != nil {
		emailService := email.NewEmailService(s.cfg)
		var tournament Tournament
		var team team.Team
		if err := s.DB.First(&tournament, tournamentID).Error; err == nil {
			if err := s.DB.First(&team, teamID).Error; err == nil {
				var captain user.User
				if err := s.DB.First(&captain, team.CaptainID).Error; err == nil {
					if err := emailService.Send(captain.Email, "Заявка на турнир",
						fmt.Sprintf("Ваша команда «%s» подала заявку на турнир «%s».", team.Name, tournament.Name)); err != nil {
						log.Error().Err(err).Str("team", team.Name).Str("tournament", tournament.Name).Msg("failed to send tournament application email")
					}
				}
			}
		}
	}
	return nil
}

// CanApply проверяет, может ли пользователь подать заявку на турнир.
func (s *TournamentService) CanApply(tournamentID, userID uint) bool {
	teams, err := s.teamService.GetMyTeams(userID)
	if err != nil || len(teams) == 0 {
		return false
	}
	var count int64
	for _, t := range teams {
		count = 0
		s.DB.Model(&TournamentTeam{}).Where("tournament_id = ? AND team_id = ?", tournamentID, t.ID).Count(&count)
		if count == 0 {
			return true
		}
	}
	return false
}

// ---------- Подсчёт очков ----------

// UpdateScoresForGame пересчитывает очки турнира после завершения конкретной игры.
func (s *TournamentService) UpdateScoresForGame(gameID uint) {
	var tg TournamentGame
	if err := s.DB.Where("game_id = ?", gameID).First(&tg).Error; err != nil {
		return
	}

	var tournament Tournament
	if err := s.DB.First(&tournament, tg.TournamentID).Error; err != nil {
		return
	}

	var passings []game.GamePassing
	s.DB.Where("game_id = ? AND status = ?", gameID, game.StatusFinished).Find(&passings)

	for _, p := range passings {
		var tt TournamentTeam
		err := s.DB.Where("tournament_id = ? AND team_id = ?", tournament.ID, p.TeamID).First(&tt).Error
		if err != nil {
			continue
		}

		points := tournament.PointsForParticipation
		if p.Place != nil {
			switch *p.Place {
			case 1:
				points = tournament.PointsForFirst
			case 2:
				points = tournament.PointsForSecond
			case 3:
				points = tournament.PointsForThird
			}
		}

		var result TournamentResult
		err = s.DB.Where("tournament_id = ? AND team_id = ?", tournament.ID, p.TeamID).First(&result).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result = TournamentResult{
				TournamentID: tournament.ID,
				TeamID:       p.TeamID,
				Score:        points,
				GamesPlayed:  1,
			}
			s.DB.Create(&result)
		} else if err == nil {
			result.Score += points
			result.GamesPlayed++
			s.DB.Save(&result)
		}
	}
}

// GetLeaderboard возвращает турнирную таблицу, отсортированную по очкам.
func (s *TournamentService) GetLeaderboard(tournamentID uint) ([]TournamentResult, error) {
	var results []TournamentResult
	err := s.DB.Preload("Team").
		Where("tournament_id = ?", tournamentID).
		Order("score DESC").
		Find(&results).Error
	return results, err
}