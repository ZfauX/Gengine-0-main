// internal/domain/tournament/service.go
package tournament

import (
	"context"
	"errors"
	"fmt"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/email"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type TournamentService struct {
	tournamentRepo       TournamentRepository
	tournamentGameRepo   TournamentGameRepository
	tournamentTeamRepo   TournamentTeamRepository
	tournamentResultRepo TournamentResultRepository
	teamService          *team.TeamService
	cfg                  *config.Config
}

func NewTournamentService(
	tournamentRepo TournamentRepository,
	tournamentGameRepo TournamentGameRepository,
	tournamentTeamRepo TournamentTeamRepository,
	tournamentResultRepo TournamentResultRepository,
	teamService *team.TeamService,
	cfg *config.Config,
) *TournamentService {
	return &TournamentService{
		tournamentRepo:       tournamentRepo,
		tournamentGameRepo:   tournamentGameRepo,
		tournamentTeamRepo:   tournamentTeamRepo,
		tournamentResultRepo: tournamentResultRepo,
		teamService:          teamService,
		cfg:                  cfg,
	}
}

func (s *TournamentService) Create(ctx context.Context, t *Tournament) error {
	return s.tournamentRepo.Create(ctx, t)
}

func (s *TournamentService) GetByID(ctx context.Context, id uint) (*Tournament, error) {
	return s.tournamentRepo.GetByID(ctx, id)
}

func (s *TournamentService) List(ctx context.Context) ([]Tournament, error) {
	return s.tournamentRepo.List(ctx)
}

func (s *TournamentService) Update(ctx context.Context, id uint, updated *Tournament, userID uint) error {
	t, err := s.tournamentRepo.GetByID(ctx, id)
	if err != nil {
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
	return s.tournamentRepo.Update(ctx, t)
}

// ---------- Игры турнира ----------

func (s *TournamentService) AddGame(ctx context.Context, tournamentID, gameID, userID uint) error {
	t, err := s.tournamentRepo.GetByID(ctx, tournamentID)
	if err != nil {
		return err
	}
	if t.AuthorID != userID {
		return errors.New("только автор турнира может добавлять игры")
	}
	games, err := s.tournamentGameRepo.ListGames(ctx, tournamentID)
	if err != nil {
		return err
	}
	for _, g := range games {
		if g.ID == gameID {
			return errors.New("игра уже в турнире")
		}
	}
	order := len(games)
	return s.tournamentGameRepo.AddGame(ctx, tournamentID, gameID, order)
}

func (s *TournamentService) RemoveGame(ctx context.Context, tournamentID, gameID, userID uint) error {
	t, err := s.tournamentRepo.GetByID(ctx, tournamentID)
	if err != nil {
		return err
	}
	if t.AuthorID != userID {
		return errors.New("только автор турнира может удалять игры")
	}
	return s.tournamentGameRepo.RemoveGame(ctx, tournamentID, gameID)
}

func (s *TournamentService) ListGames(ctx context.Context, tournamentID uint) ([]game.Game, error) {
	return s.tournamentGameRepo.ListGames(ctx, tournamentID)
}

func (s *TournamentService) GetAvailableGames(ctx context.Context, tournamentID, userID uint) ([]game.Game, error) {
	return s.tournamentGameRepo.GetAvailableGames(ctx, tournamentID, userID)
}

// ---------- Заявки ----------

func (s *TournamentService) Apply(ctx context.Context, tournamentID, teamID, userID uint) error {
	if !s.teamService.CanManageTeam(ctx, teamID, userID) {
		return errors.New("только капитан может подать заявку")
	}

	_, err := s.tournamentTeamRepo.GetByTournamentAndTeam(ctx, tournamentID, teamID)
	if err == nil {
		return errors.New("команда уже участвует в турнире")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	if err := s.tournamentTeamRepo.AddTeam(ctx, tournamentID, teamID); err != nil {
		return err
	}

	games, err := s.tournamentGameRepo.ListGames(ctx, tournamentID)
	if err != nil {
		log.Error().Err(err).Uint("tournament_id", tournamentID).Msg("Apply: failed to list tournament games")
	}
	for _, g := range games {
		_, err := s.tournamentTeamRepo.FindByGameAndTeam(ctx, g.ID, teamID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			passing := game.GamePassing{
				GameID: g.ID,
				TeamID: teamID,
				Status: game.StatusPending,
			}
			if err := s.tournamentTeamRepo.CreatePassing(ctx, &passing); err != nil {
				log.Error().Err(err).Uint("game_id", g.ID).Uint("team_id", teamID).Msg("Apply: failed to create passing")
			}
		}
	}

	if s.cfg != nil && s.cfg.SMTP.Enabled {
		// Используем глобальную очередь вместо локального сервиса
		tournamentPtr, err := s.tournamentRepo.GetByID(ctx, tournamentID)
		if err == nil {
			teamObj, err := s.tournamentTeamRepo.GetTeam(ctx, teamID)
			if err == nil {
				captain, err := s.tournamentTeamRepo.GetCaptain(ctx, teamObj.CaptainID)
				if err == nil {
					if err := email.Enqueue(
						captain.Email,
						"Заявка на турнир",
						fmt.Sprintf("Ваша команда «%s» подала заявку на турнир «%s».", teamObj.Name, tournamentPtr.Name),
					); err != nil {
						log.Error().Err(err).Str("email", captain.Email).Msg("failed to enqueue tournament application email")
					}
				}
			}
		}
	}
	return nil
}

func (s *TournamentService) CanApply(ctx context.Context, tournamentID, userID uint) bool {
	teams, err := s.teamService.GetMyTeams(ctx, userID)
	if err != nil || len(teams) == 0 {
		return false
	}
	for _, t := range teams {
		_, err := s.tournamentTeamRepo.GetByTournamentAndTeam(ctx, tournamentID, t.ID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true
		}
	}
	return false
}

// ---------- Подсчёт очков ----------

func (s *TournamentService) UpdateScoresForGame(ctx context.Context, gameID uint) {
	tg, err := s.tournamentGameRepo.FindByGameID(ctx, gameID)
	if err != nil {
		return
	}
	tournament, err := s.tournamentRepo.GetByID(ctx, tg.TournamentID)
	if err != nil {
		return
	}

	passings, err := s.tournamentGameRepo.ListFinishedPassings(ctx, gameID, game.StatusFinished)
	if err != nil {
		return
	}

	for _, p := range passings {
		_, err := s.tournamentTeamRepo.GetByTournamentAndTeam(ctx, tournament.ID, p.TeamID)
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

		result, err := s.tournamentResultRepo.GetByTournamentAndTeam(ctx, tournament.ID, p.TeamID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result = &TournamentResult{
				TournamentID: tournament.ID,
				TeamID:       p.TeamID,
				Score:        points,
				GamesPlayed:  1,
			}
		} else if err == nil {
			result.Score += points
			result.GamesPlayed++
		} else {
			continue
		}
		_ = s.tournamentResultRepo.Upsert(ctx, result)
	}
}

func (s *TournamentService) GetLeaderboard(ctx context.Context, tournamentID uint) ([]TournamentResult, error) {
	return s.tournamentResultRepo.GetLeaderboard(ctx, tournamentID)
}
