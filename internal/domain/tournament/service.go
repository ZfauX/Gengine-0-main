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

	_, getErr := s.tournamentTeamRepo.GetByTournamentAndTeam(ctx, tournamentID, teamID)
	if getErr == nil {
		return errors.New("команда уже участвует в турнире")
	}
	if !errors.Is(getErr, gorm.ErrRecordNotFound) {
		return getErr
	}

	if err := s.tournamentTeamRepo.AddTeam(ctx, tournamentID, teamID); err != nil {
		return err
	}

	games, err := s.tournamentGameRepo.ListGames(ctx, tournamentID)
	if err != nil {
		log.Error().Err(err).Uint("tournament_id", tournamentID).Msg("Apply: failed to list tournament games")
		return err
	}

	gameIDs := make([]uint, len(games))
	for i, g := range games {
		gameIDs[i] = g.ID
	}

	existingPassings, _ := s.tournamentTeamRepo.FindPassingsByGamesAndTeam(ctx, gameIDs, teamID)
	existingMap := make(map[uint]bool)
	for _, p := range existingPassings {
		existingMap[p.GameID] = true
	}

	for _, g := range games {
		if existingMap[g.ID] {
			continue
		}
		passing := game.GamePassing{
			GameID: g.ID,
			TeamID: teamID,
			Status: game.StatusPending,
		}
		if err := s.tournamentTeamRepo.CreatePassing(ctx, &passing); err != nil {
			log.Error().Err(err).Uint("game_id", g.ID).Uint("team_id", teamID).Msg("Apply: failed to create passing")
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

	teamIDs := make([]uint, len(teams))
	for i, t := range teams {
		teamIDs[i] = t.ID
	}

	existing, _ := s.tournamentTeamRepo.GetByTournamentAndTeamIDs(ctx, tournamentID, teamIDs)
	existingMap := make(map[uint]bool)
	for _, tt := range existing {
		existingMap[tt.TeamID] = true
	}

	for _, t := range teams {
		if !existingMap[t.ID] {
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

	teamIDs := make([]uint, len(passings))
	for i, p := range passings {
		teamIDs[i] = p.TeamID
	}

	tournamentTeams, _ := s.tournamentTeamRepo.GetByTournamentAndTeamIDs(ctx, tournament.ID, teamIDs)
	inTournament := make(map[uint]bool)
	for _, tt := range tournamentTeams {
		inTournament[tt.TeamID] = true
	}

	existingResults, _ := s.tournamentResultRepo.GetByTournamentAndTeamIDs(ctx, tournament.ID, teamIDs)
	resultMap := make(map[uint]*TournamentResult)
	for i := range existingResults {
		resultMap[existingResults[i].TeamID] = &existingResults[i]
	}

	for _, p := range passings {
		if !inTournament[p.TeamID] {
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

		result, exists := resultMap[p.TeamID]
		if !exists {
			result = &TournamentResult{
				TournamentID: tournament.ID,
				TeamID:       p.TeamID,
				Score:        points,
				GamesPlayed:  1,
			}
		} else {
			result.Score += points
			result.GamesPlayed++
		}
		_ = s.tournamentResultRepo.Upsert(ctx, result)
	}
}

func (s *TournamentService) GetLeaderboard(ctx context.Context, tournamentID uint) ([]TournamentResult, error) {
	return s.tournamentResultRepo.GetLeaderboard(ctx, tournamentID)
}
