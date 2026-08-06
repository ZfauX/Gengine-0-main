// internal/domain/tournament/service.go
package tournament

import (
	"context"
	stderrors "errors"
	"fmt"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/email"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type TournamentService struct {
	db                   *gorm.DB
	tournamentRepo       TournamentRepository
	tournamentGameRepo   TournamentGameRepository
	tournamentTeamRepo   TournamentTeamRepository
	tournamentResultRepo TournamentResultRepository
	teamService          *team.TeamService
	cfg                  *config.Config
}

func NewTournamentService(
	db *gorm.DB,
	tournamentRepo TournamentRepository,
	tournamentGameRepo TournamentGameRepository,
	tournamentTeamRepo TournamentTeamRepository,
	tournamentResultRepo TournamentResultRepository,
	teamService *team.TeamService,
	cfg *config.Config,
) *TournamentService {
	return &TournamentService{
		db:                   db,
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
		return stderrors.New("только автор может редактировать турнир")
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
		return stderrors.New("только автор турнира может добавлять игры")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&TournamentGame{}).Where("tournament_id = ? AND game_id = ?", tournamentID, gameID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return stderrors.New("игра уже в турнире")
		}
		var order int64
		if err := tx.Model(&TournamentGame{}).Where("tournament_id = ?", tournamentID).Count(&order).Error; err != nil {
			return err
		}
		tg := TournamentGame{
			TournamentID: tournamentID,
			GameID:       gameID,
			OrderIndex:   int(order),
		}
		return tx.Create(&tg).Error
	})
}

func (s *TournamentService) RemoveGame(ctx context.Context, tournamentID, gameID, userID uint) error {
	t, err := s.tournamentRepo.GetByID(ctx, tournamentID)
	if err != nil {
		return err
	}
	if t.AuthorID != userID {
		return stderrors.New("только автор турнира может удалять игры")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Get all finished passings for this game — read inside the transaction
		var passings []game.GamePassing
		if err := tx.Where("game_id = ? AND status = ?", gameID, game.StatusFinished).Find(&passings).Error; err != nil {
			return err
		}

		// Deduct points from tournament results for each team that finished this game
		// Bulk-fetch all results first to avoid N+1 queries
		if len(passings) > 0 {
			teamIDs := make([]uint, 0, len(passings))
			for _, p := range passings {
				teamIDs = append(teamIDs, p.TeamID)
			}
			var results []TournamentResult
			if err := tx.Where("tournament_id = ? AND team_id IN ?", tournamentID, teamIDs).Find(&results).Error; err != nil {
				return err
			}
			// Build lookup map
			resultByTeam := make(map[uint]*TournamentResult, len(results))
			for i := range results {
				resultByTeam[results[i].TeamID] = &results[i]
			}

			for _, p := range passings {
				result, found := resultByTeam[p.TeamID]
				if !found {
					continue
				}

				// Точное списание начисленного (C-M2): не пересчитываем по
				// текущему месту/настройкам PointsFor* (они могли измениться).
				points := p.TournamentPoints

				result.Score -= points
				result.GamesPlayed--

				if result.GamesPlayed <= 0 {
					result.Score = 0
					if err := tx.Delete(&result).Error; err != nil {
						return err
					}
				} else {
					if result.Score < 0 {
						result.Score = 0
					}
					if err := s.tournamentResultRepo.Upsert(tx, result); err != nil {
						return err
					}
				}
			}
		}

		// Remove the game from the tournament
		return tx.Where("tournament_id = ? AND game_id = ?", tournamentID, gameID).Delete(&TournamentGame{}).Error
	})
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
		return stderrors.New("только капитан может подать заявку")
	}

	_, getErr := s.tournamentTeamRepo.GetByTournamentAndTeam(ctx, tournamentID, teamID)
	if getErr == nil {
		return stderrors.New("команда уже участвует в турнире")
	}
	if !stderrors.Is(getErr, gorm.ErrRecordNotFound) {
		return getErr
	}

	// Read-only: fetch games outside transaction
	games, err := s.tournamentGameRepo.ListGames(ctx, tournamentID)
	if err != nil {
		log.Error().Err(err).Uint("tournament_id", tournamentID).Msg("Apply: failed to list tournament games")
		return err
	}

	gameIDs := make([]uint, len(games))
	for i, g := range games {
		gameIDs[i] = g.ID
	}

	existingPassings, err := s.tournamentTeamRepo.FindPassingsByGamesAndTeam(ctx, gameIDs, teamID)
	if err != nil {
		log.Error().Err(err).Uint("team_id", teamID).Msg("Apply: FindPassingsByGamesAndTeam failed")
		return err
	}
	existingMap := make(map[uint]bool)
	for _, p := range existingPassings {
		existingMap[p.GameID] = true
	}

	// Transaction: add team + create passings
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		added, addErr := s.tournamentTeamRepo.AddTeamTx(tx, ctx, tournamentID, teamID)
		if addErr != nil {
			return addErr
		}
		if !added {
			// Конкурентная заявка уже добавила команду (C-H3).
			return stderrors.New("команда уже участвует в турнире")
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
			if err := tx.Create(&passing).Error; err != nil {
				log.Error().Err(err).Uint("game_id", g.ID).Uint("team_id", teamID).Msg("Apply: failed to create passing")
				return err
			}
		}
		return nil
	}); err != nil {
		return err
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
		// C-M8: не глотаем молча — ops должны видеть сбои начисления.
		log.Warn().Err(err).Uint("game_id", gameID).Msg("UpdateScoresForGame: game not in tournament")
		return
	}
	tournament, err := s.tournamentRepo.GetByID(ctx, tg.TournamentID)
	if err != nil {
		log.Warn().Err(err).Uint("game_id", gameID).Uint("tournament_id", tg.TournamentID).Msg("UpdateScoresForGame: failed to load tournament")
		return
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Сериализация начислений по игре (C-C1): параллельные финиши команд
		// не должны начислить очки дважды — паттерн как в svc_rating.go.
		if lockErr := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(gameID)).Error; lockErr != nil {
			return fmt.Errorf("pg_advisory_xact_lock: %w", lockErr)
		}

		// Читаем только ещё не начисленные прохождения ВНУТРИ транзакции после
		// блокировки — второй вызов увидит уже помеченные tournament_scored=true.
		var passings []game.GamePassing
		if passErr := tx.Where("game_id = ? AND status = ? AND tournament_scored = false",
			gameID, game.StatusFinished).Find(&passings).Error; passErr != nil {
			return passErr
		}
		if len(passings) == 0 {
			return nil
		}

		teamIDs := make([]uint, len(passings))
		for i, p := range passings {
			teamIDs[i] = p.TeamID
		}

		// Читаем турнирные команды ВНУТРИ транзакции через tx — репозиторий
		// использует внешний r.db (отдельное соединение без search_path в схеме).
		var tournamentTeams []TournamentTeam
		if teamsErr := tx.Where("tournament_id = ? AND team_id IN ?", tournament.ID, teamIDs).Find(&tournamentTeams).Error; teamsErr != nil {
			log.Error().Err(teamsErr).Uint("tournament_id", tournament.ID).Msg("UpdateScoresForGame: failed to get tournament teams")
			return teamsErr
		}
		inTournament := make(map[uint]bool)
		for _, tt := range tournamentTeams {
			inTournament[tt.TeamID] = true
		}

		// Read existing results inside the transaction to prevent concurrent read races
		var existingResults []TournamentResult
		if findErr := tx.Where("tournament_id = ? AND team_id IN ?", tournament.ID, teamIDs).Find(&existingResults).Error; findErr != nil {
			return findErr
		}
		resultMap := make(map[uint]*TournamentResult)
		for i := range existingResults {
			resultMap[existingResults[i].TeamID] = &existingResults[i]
		}

		scoredIDs := make([]uint, 0, len(passings))
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
			if upsErr := s.tournamentResultRepo.Upsert(tx, result); upsErr != nil {
				return upsErr
			}
			// Сохраняем точное значение начисленных очков для точного списания (C-M2).
			if pointsErr := tx.Model(&game.GamePassing{}).Where("id = ?", p.ID).Update("tournament_points", points).Error; pointsErr != nil {
				return pointsErr
			}
			scoredIDs = append(scoredIDs, p.ID)
		}

		// Помечаем прохождения начисленными в той же транзакции (идемпотентность).
		if len(scoredIDs) > 0 {
			if markErr := tx.Model(&game.GamePassing{}).
				Where("id IN ?", scoredIDs).
				Update("tournament_scored", true).Error; markErr != nil {
				return markErr
			}
		}
		return nil
	})
	if err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("UpdateScoresForGame: transaction failed")
	}
}

func (s *TournamentService) GetLeaderboard(ctx context.Context, tournamentID uint) ([]TournamentResult, error) {
	return s.tournamentResultRepo.GetLeaderboard(ctx, tournamentID)
}
