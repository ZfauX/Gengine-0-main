// internal/domain/tournament/service.go
package tournament

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/email"

	"github.com/lib/pq"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Sentinel-ошибки турнира (MED-4, pass 29): handler'ы различают
// «нет прав»/«уже существует» через errors.Is, а не строки.
var (
	ErrTournamentEditForbidden   = stderrors.New("только автор может редактировать турнир")
	ErrTournamentManageForbidden = stderrors.New("только автор турнира может добавлять игры")
	ErrTournamentRemoveForbidden = stderrors.New("только автор турнира может удалять игры")
	ErrGameAlreadyInTournament   = stderrors.New("игра уже в турнире")
	ErrCaptainOnly               = stderrors.New("только капитан может подать заявку")
	ErrTeamAlreadyInTournament   = stderrors.New("команда уже участвует в турнире")
)

type TournamentService struct {
	db                   *gorm.DB
	tournamentRepo       TournamentRepository
	tournamentGameRepo   TournamentGameRepository
	tournamentTeamRepo   TournamentTeamRepository
	tournamentResultRepo TournamentResultRepository
	teamService          *team.TeamService
	cfg                  *config.Config
	// cache опционален (F-5, pass 31): турнирный лидерборд кэшируется на 30с.
	cache cache.CacheStore
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

// WithCache устанавливает кэш для лидерборда (F-5, pass 31).
func (s *TournamentService) WithCache(c cache.CacheStore) *TournamentService {
	s.cache = c
	return s
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
		return ErrTournamentEditForbidden
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
		return ErrTournamentManageForbidden
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&TournamentGame{}).Where("tournament_id = ? AND game_id = ?", tournamentID, gameID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrGameAlreadyInTournament
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
		if err := tx.Create(&tg).Error; err != nil {
			return err
		}

		// #4: создаём StatusPending прохождения для всех команд, уже поданных
		// в турнир — иначе команды, зарегистрированные до добавления игры,
		// не получат passing и не будут начислены очки в новой игре.
		var teams []TournamentTeam
		if err := tx.Where("tournament_id = ?", tournamentID).Find(&teams).Error; err != nil {
			return err
		}
		passings := make([]game.GamePassing, 0, len(teams))
		for _, tt := range teams {
			passings = append(passings, game.GamePassing{
				GameID: gameID,
				TeamID: tt.TeamID,
				Status: game.StatusPending,
			})
		}
		// S3 (pass 30): ON CONFLICT DO NOTHING — RemoveGame не удаляет
		// game_passings (только сбрасывает флаги); при пере-добавлении игры
		// строки уже существуют → без конфликта нельзя.
		if len(passings) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&passings, 100).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *TournamentService) RemoveGame(ctx context.Context, tournamentID, gameID, userID uint) error {
	t, err := s.tournamentRepo.GetByID(ctx, tournamentID)
	if err != nil {
		return err
	}
	if t.AuthorID != userID {
		return ErrTournamentRemoveForbidden
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Get all finished passings for this game — read inside the transaction
		var passings []game.GamePassing
		if err = tx.Where("game_id = ? AND status = ?", gameID, game.StatusFinished).Find(&passings).Error; err != nil {
			return err
		}

		// Deduct points from tournament results for each team that finished this game
		// Bulk-fetch all results first to avoid N+1 queries
		if len(passings) > 0 {
			teamIDs := make([]uint, 0, len(passings))
			points := make([]int, 0, len(passings))
			for _, p := range passings {
				teamIDs = append(teamIDs, p.TeamID)
				points = append(points, p.TournamentPoints)
			}

			// F-3 (pass 31): batch списание вместо построчного Save/Delete —
			// один UPDATE через unnest + один DELETE обнулённых результатов.
			if err = tx.Exec(`
				UPDATE tournament_results tr
				SET score = GREATEST(0, tr.score - t.points),
				    games_played = tr.games_played - 1
				FROM unnest(?::bigint[], ?::int[]) AS t(team_id, points)
				WHERE tr.tournament_id = ? AND tr.team_id = t.team_id
			`, pq.Array(teamIDs), pq.Array(points), tournamentID).Error; err != nil {
				return err
			}
			if err = tx.Exec(`
				DELETE FROM tournament_results
				WHERE tournament_id = ? AND team_id = ANY(?)
				  AND games_played <= 0
			`, tournamentID, pq.Array(teamIDs)).Error; err != nil {
				return err
			}
		}

		// #5: сбрасываем флаги начисления на прохождениях игры — если игру
		// пере-добавят, команды смогут пройти её заново и будут начислены.
		// G4: ограничиваем командами ЭТОГО турнира — сброс для всех прохождений
		// игры затронул бы команды других турниров и привёл бы к двойному
		// начислению очков, если игра участвует в нескольких турнирах.
		if err = tx.Model(&game.GamePassing{}).
			Where("game_id = ? AND team_id IN (SELECT team_id FROM tournament_teams WHERE tournament_id = ?)", gameID, tournamentID).
			Updates(map[string]any{"tournament_scored": false, "tournament_points": 0}).Error; err != nil {
			return err
		}

		// Remove the game from the tournament
		return tx.Where("tournament_id = ? AND game_id = ?", tournamentID, gameID).Delete(&TournamentGame{}).Error
	})
	if err != nil {
		return err
	}
	// F-5 (pass 31): инвалидируем кэш лидерборда турнира.
	if s.cache != nil {
		s.cache.DeleteWithCtx(ctx, fmt.Sprintf("tournament:leaderboard:%d", tournamentID))
	}
	return nil
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
		return ErrCaptainOnly
	}

	_, getErr := s.tournamentTeamRepo.GetByTournamentAndTeam(ctx, tournamentID, teamID)
	if getErr == nil {
		return ErrTeamAlreadyInTournament
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
			return ErrTeamAlreadyInTournament
		}
		passings := make([]game.GamePassing, 0, len(games))
		for _, g := range games {
			if existingMap[g.ID] {
				continue
			}
			passings = append(passings, game.GamePassing{
				GameID: g.ID,
				TeamID: teamID,
				Status: game.StatusPending,
			})
		}
		// S3 (pass 30): ON CONFLICT DO NOTHING — защита от гонки и от
		// пере-добавления уже существующих прохождений.
		if len(passings) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&passings, 100).Error; err != nil {
				log.Error().Err(err).Uint("team_id", teamID).Msg("Apply: failed to create passings")
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

	existing, err := s.tournamentTeamRepo.GetByTournamentAndTeamIDs(ctx, tournamentID, teamIDs)
	if err != nil {
		// A-4 (pass 37): сбой БД — консервативно false (не раскрываем участие).
		log.Error().Err(err).Uint("tournament_id", tournamentID).Msg("CanApply: failed to check existing teams")
		return false
	}
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

		scoredIDs := make([]uint, 0, len(passings))
		// Собираем дельты (не аккумулированные значения) — UpsertMany
		// инкрементирует существующую строку через EXCLUDED (M9, pass 30).
		deltaResults := make([]TournamentResult, 0, len(passings))
		// P-1 (pass 37): точки за места собираем в map, затем один batch UPDATE
		// (CASE id) — раньше был N+1 построчных UPDATE внутри транзакции с
		// advisory lock (игра со 100 командами = 100 UPDATE).
		pointsByPassing := make(map[uint]int, len(passings))
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

			deltaResults = append(deltaResults, TournamentResult{
				TournamentID: tournament.ID,
				TeamID:       p.TeamID,
				Score:        points,
				GamesPlayed:  1,
			})
			// Сохраняем точное значение начисленных очков для точного списания (C-M2).
			pointsByPassing[p.ID] = points
			scoredIDs = append(scoredIDs, p.ID)
		}

		// Batch UPDATE tournament_points (P-1, pass 37): один запрос на все
		// прохождения вместо N построчных.
		if len(pointsByPassing) > 0 {
			passingIDs := make([]uint, 0, len(pointsByPassing))
			cases := make([]string, 0, len(pointsByPassing))
			args := make([]any, 0, len(pointsByPassing)*2)
			for id, pts := range pointsByPassing {
				cases = append(cases, "WHEN ? THEN ?")
				args = append(args, id, pts)
				passingIDs = append(passingIDs, id)
			}
			idPlaceholders := joinPlaceholders(len(passingIDs))
			q := fmt.Sprintf(
				"UPDATE game_passings SET tournament_points = CASE id %s ELSE tournament_points END WHERE id IN (%s)",
				strings.Join(cases, " "),
				idPlaceholders,
			)
			args = append(args, toAnySlice(passingIDs)...)
			if ptsErr := tx.Exec(q, args...).Error; ptsErr != nil {
				return ptsErr
			}
		}

		// Единый батч-upsert вместо построчного Save (M9, pass 30).
		if len(deltaResults) > 0 {
			if upsErr := s.tournamentResultRepo.UpsertMany(tx, deltaResults); upsErr != nil {
				return upsErr
			}
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
		return
	}
	// F-5 (pass 31): инвалидируем кэш лидерборда турнира.
	if s.cache != nil {
		s.cache.DeleteWithCtx(ctx, fmt.Sprintf("tournament:leaderboard:%d", tg.TournamentID))
	}
}

func (s *TournamentService) GetLeaderboard(ctx context.Context, tournamentID uint) ([]TournamentResult, error) {
	// F-5 (pass 31): лидерборд кэшируется на 30с — на каждой загрузке
	// страницы турнира пере-запрос был избыточен (player-лидерборд уже кэширован).
	if s.cache != nil {
		key := fmt.Sprintf("tournament:leaderboard:%d", tournamentID)
		if v, err := s.cache.GetOrSetWithCtx(ctx, key, 30*time.Second, func() (any, error) {
			return s.tournamentResultRepo.GetLeaderboard(ctx, tournamentID)
		}); err == nil {
			if results, ok := v.([]TournamentResult); ok {
				return results, nil
			}
		}
	}
	return s.tournamentResultRepo.GetLeaderboard(ctx, tournamentID)
}

// joinPlaceholders возвращает "?, ?, ..." для n значений (P-1, pass 37).
func joinPlaceholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ", ")
}

// toAnySlice конвертирует срез в []any для GORM-аргументов.
func toAnySlice[T any](s []T) []any {
	result := make([]any, len(s))
	for i, v := range s {
		result[i] = v
	}
	return result
}
