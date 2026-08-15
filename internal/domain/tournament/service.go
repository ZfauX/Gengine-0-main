// internal/domain/tournament/service.go
package tournament

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/util"

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
	ErrGameNotFound              = stderrors.New("игра не найдена")
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
		// L7 (PASS-17): несуществующая игра не должна добавляться в турнир —
		// раньше FK-ошибка/сирота зависела от наличия ограничения.
		var gameExists int64
		if err := tx.Model(&game.Game{}).Where("id = ? AND deleted_at IS NULL", gameID).Count(&gameExists).Error; err != nil {
			return err
		}
		if gameExists == 0 {
			return ErrGameNotFound
		}
		var count int64
		if err := tx.Model(&TournamentGame{}).Where("tournament_id = ? AND game_id = ?", tournamentID, gameID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrGameAlreadyInTournament
		}
		// L11 (PASS-6): OrderIndex = MAX+1, а не COUNT(*) — после RemoveGame
		// индексы дырявые, и COUNT мог совпасть с существующим OrderIndex.
		var order int64
		if err := tx.Model(&TournamentGame{}).
			Where("tournament_id = ?", tournamentID).
			Select("COALESCE(MAX(order_index), 0) + 1").
			Scan(&order).Error; err != nil {
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

func (s *TournamentService) RemoveGame(ctx context.Context, tournamentID, gameID, userID uint, isAdmin bool) error {
	t, err := s.tournamentRepo.GetByID(ctx, tournamentID)
	if err != nil {
		return err
	}
	if t.AuthorID != userID && !isAdmin {
		return ErrTournamentRemoveForbidden
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// PASS-9 (security HIGH #1): запрещаем автору удаление игры, если очки
		// за неё УЖЕ начислены — иначе remove→add→finish бесконечно накручивает
		// очки (автор списывает чужие и пере-начисляет свои). Глобальный админ
		// может (исправление ошибок/поддержка).
		var scoredCount int64
		if err = tx.Model(&game.GamePassing{}).
			Where("game_id = ? AND team_id IN (SELECT team_id FROM tournament_teams WHERE tournament_id = ?)", gameID, tournamentID).
			Where("? = ANY(tournament_scored_ids)", int64(tournamentID)).
			Count(&scoredCount).Error; err != nil {
			return err
		}
		if scoredCount > 0 && !isAdmin {
			return fmt.Errorf("нельзя удалить игру из турнира: очки уже начислены")
		}

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
				// M4 (PASS-6): списываем ТОЧНО начисленное значение из
				// tournament_scored_points (индекс = позиции tournament_id в
				// tournament_scored_ids). Раньше пересчитывали по ТЕКУЩЕЙ
				// конфигурации — изменение PointsFor* между финишем и удалением
				// давало «фантомные» очки.
				points = append(points, scoredPointsForTournament(p, tournamentID))
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
		// DEEP-REVIEW (pass 46): удаляем именно tournament_id из массива
		// начислений (tournament_scored_ids), а не сбрасываем весь флаг.
		// H2 (PASS-7): scored_points удаляется ПО ПОЗИЦИИ (параллельный массив) —
		// иначе после remove→add→finish индексы разъезжаются и списывается старое
		// значение. array_remove по значению очка нельзя (два турнира с 10 очками).
		pointsRemoveExpr := `(SELECT array_agg(pp.points ORDER BY pp.ord)
			FROM unnest(tournament_scored_points) WITH ORDINALITY AS pp(points, ord)
			WHERE pp.ord <> (SELECT o.ord FROM unnest(tournament_scored_ids) WITH ORDINALITY AS o(id, ord) WHERE o.id = ?))`
		// Reviewer #3 (PASS-9): tournament_points обнуляем только если в массиве
		// НЕ осталось начислений других турниров. Раньше безусловный `= 0`
		// затирал очки, начисленные вторым турниром той же игры.
		// Обнуление происходит в Go после обновления массивов.
		if err = tx.Model(&game.GamePassing{}).
			Where("game_id = ? AND team_id IN (SELECT team_id FROM tournament_teams WHERE tournament_id = ?)", gameID, tournamentID).
			Updates(map[string]any{
				"tournament_scored_ids":    gorm.Expr("array_remove(tournament_scored_ids, ?)", int64(tournamentID)),
				"tournament_scored_points": gorm.Expr(pointsRemoveExpr, int64(tournamentID)),
			}).Error; err != nil {
			return err
		}
		// tournament_points = сумма оставшихся tournament_scored_points.
		if err = tx.Exec(`
			UPDATE game_passings
			SET tournament_points = COALESCE((
				SELECT SUM(pp.points)
				FROM unnest(tournament_scored_points) WITH ORDINALITY AS pp(points, ord)
			), 0)
			WHERE game_id = ? AND team_id IN (SELECT team_id FROM tournament_teams WHERE tournament_id = ?)
		`, gameID, tournamentID).Error; err != nil {
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
	// DEEP-REVIEW (pass 46): раньше FindByGameID возвращал только ПЕРВЫЙ турнир
	// игры — игра в 2+ турнирах начисляла очки только в один. Теперь берём все.
	tournamentIDs, err := s.tournamentGameRepo.FindTournamentIDsByGameID(ctx, gameID)
	if err != nil {
		log.Warn().Err(err).Uint("game_id", gameID).Msg("UpdateScoresForGame: game not in tournament")
		return
	}
	if len(tournamentIDs) == 0 {
		log.Warn().Uint("game_id", gameID).Msg("UpdateScoresForGame: game not in any tournament")
		return
	}

	// P-4 (PASS-9): один запрос GetByIDs вместо цикла K×GetByID (N+1) с лишним
	// Preload Author. Сбойные турниры не прерывают начисление остальным.
	tournaments, loadErr := s.tournamentRepo.GetByIDs(ctx, tournamentIDs)
	if loadErr != nil {
		log.Error().Err(loadErr).Uint("game_id", gameID).Msg("UpdateScoresForGame: failed to load tournaments")
	}
	if len(tournaments) == 0 {
		log.Warn().Uint("game_id", gameID).Msg("UpdateScoresForGame: no tournaments loaded")
		return
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Сериализация начислений по игре (C-C1): параллельные финиши команд
		// не должны начислить очки дважды — паттерн как в svc_rating.go.
		if lockErr := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(gameID)).Error; lockErr != nil {
			return fmt.Errorf("pg_advisory_xact_lock: %w", lockErr)
		}

		// Начисляем очки каждому турниру игры (в одной транзакции — атомарно).
		for _, tournament := range tournaments {
			if scoreErr := s.scoreTournamentInTx(tx, tournament, gameID); scoreErr != nil {
				return scoreErr
			}
		}
		return nil
	})
	if err != nil {
		log.Error().Err(err).Uint("game_id", gameID).Msg("UpdateScoresForGame: transaction failed")
		return
	}
	// F-5 (pass 31): инвалидируем кэш лидербордов всех турниров игры.
	if s.cache != nil {
		for _, tid := range tournamentIDs {
			s.cache.DeleteWithCtx(ctx, fmt.Sprintf("tournament:leaderboard:%d", tid))
		}
	}
}

// scoreTournamentInTx начисляет очки одного турнира за прохождение игры.
// Вызывается внутри транзакции UpdateScoresForGame (под advisory lock на игру).
func (s *TournamentService) scoreTournamentInTx(tx *gorm.DB, tournament *Tournament, gameID uint) error {
	// Читаем только ещё не начисленные прохождения ВНУТРИ транзакции после
	// блокировки — второй вызов увидит уже добавленные в tournament_scored_ids.
	// DEEP-REVIEW (pass 46): проверяем НЕ содержат ли начисления ЭТОТ турнир
	// (NOT tournament_id = ANY(...)) — игра в 2+ турнирах начисляется каждому.
	var passings []game.GamePassing
	if passErr := tx.Where(
		"game_id = ? AND status = ? AND NOT (? = ANY(tournament_scored_ids))",
		gameID, game.StatusFinished, int64(tournament.ID),
	).Find(&passings).Error; passErr != nil {
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

		points := pointsForPlace(tournament, p)

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
		pointsCases := make([]string, 0, len(pointsByPassing))
		args := make([]any, 0, len(pointsByPassing)*2)
		for id, pts := range pointsByPassing {
			pointsCases = append(pointsCases, "WHEN ? THEN ?")
			args = append(args, id, pts)
			passingIDs = append(passingIDs, id)
		}
		idPlaceholders := util.JoinPlaceholders(len(passingIDs))
		// M1 (PASS-17): начисляем К СУЩЕСТВУЮЩИМ очкам (tournament_points +
		// CASE), а не присваиваем — иначе при игре в 2+ турнирах значение
		// становилось «последнего турнира», а не суммой (RemoveGame
		// пересчитывает именно как сумму tournament_scored_points).
		q := fmt.Sprintf(
			"UPDATE game_passings SET tournament_points = tournament_points + (CASE id %s ELSE 0 END) WHERE id IN (%s)",
			strings.Join(pointsCases, " "),
			idPlaceholders,
		)
		args = append(args, util.ToAnySlice(passingIDs)...)
		if ptsErr := tx.Exec(q, args...).Error; ptsErr != nil {
			return ptsErr
		}

		// M4 (PASS-6): отдельный batch — добавляем начисленные очки в
		// tournament_scored_points (параллельно scored_ids) для точного
		// списания при RemoveGame.
		pointsArgs := make([]any, 0, len(pointsByPassing)*2)
		pointsCases2 := make([]string, 0, len(pointsByPassing))
		for id, pts := range pointsByPassing {
			pointsCases2 = append(pointsCases2, "WHEN ? THEN ?")
			pointsArgs = append(pointsArgs, id, pts)
		}
		q2 := fmt.Sprintf(
			"UPDATE game_passings SET tournament_scored_points = array_append(tournament_scored_points, CASE id %s ELSE 0 END) WHERE id IN (%s)",
			strings.Join(pointsCases2, " "),
			idPlaceholders,
		)
		pointsArgs = append(pointsArgs, util.ToAnySlice(passingIDs)...)
		if ptsErr2 := tx.Exec(q2, pointsArgs...).Error; ptsErr2 != nil {
			return ptsErr2
		}
	}

	// Единый батч-upsert вместо построчного Save (M9, pass 30).
	if len(deltaResults) > 0 {
		if upsErr := s.tournamentResultRepo.UpsertMany(tx, deltaResults); upsErr != nil {
			return upsErr
		}
	}

	// Помечаем прохождения начисленными для ЭТОГО турнира в той же транзакции
	// (идемпотентность). tournament_scored_points уже добавлен в batch UPDATE
	// выше (M4, PASS-6) — здесь только scored_ids.
	if len(scoredIDs) > 0 {
		if markErr := tx.Model(&game.GamePassing{}).
			Where("id IN ?", scoredIDs).
			Update("tournament_scored_ids", gorm.Expr("array_append(tournament_scored_ids, ?)", int64(tournament.ID))).Error; markErr != nil {
			return markErr
		}
	}
	return nil
}

// pointsForPlace возвращает очки за место по правилам турнира
// (DEEP-REVIEW PASS-2): единая формула для начисления (scoreTournamentInTx)
// и списания (RemoveGame). Раньше RemoveGame брал общую колонку
// game_passings.tournament_points, которая при 2+ турнирах перезаписывалась.
func pointsForPlace(t *Tournament, p game.GamePassing) int {
	points := t.PointsForParticipation
	if p.Place != nil {
		switch *p.Place {
		case 1:
			points = t.PointsForFirst
		case 2:
			points = t.PointsForSecond
		case 3:
			points = t.PointsForThird
		}
	}
	return points
}

// scoredPointsForTournament (DEEP-REVIEW PASS-6 M4): возвращает ТОЧНО
// начисленное количество очков за прохождение в указанном турнире.
// Значение берётся из tournament_scored_points по индексу tournament_id
// в tournament_scored_ids (параллельные массивы, заполняются при начислении).
// Fallback: если данных нет (старые записи до миграции 000065) — пересчёт
// по текущей конфигурации (pointsForPlace).
func scoredPointsForTournament(p game.GamePassing, tournamentID uint) int {
	for i, tid := range p.TournamentScoredIDs {
		if uint64(tid) == uint64(tournamentID) {
			if i < len(p.TournamentScoredPoints) {
				return int(p.TournamentScoredPoints[i])
			}
			// Массив очков короче (запись до миграции) — неизвестно; безопасно
			// вернуть 0 (не списываем больше, чем начислено).
			return 0
		}
	}
	return 0
}

func (s *TournamentService) GetLeaderboard(ctx context.Context, tournamentID uint) ([]TournamentResult, error) {
	// F-5 (pass 31): лидерборд кэшируется на 30с.
	// DEEP-REVIEW PASS-2 (#12): раньше GetOrSetWithCtx + type-assert НЕ хитился
	// с Valkey (JSON → []map[string]any). Теперь — через GetWithCtx с
	// JSON-fallback, как cacheGetJSON в game-сервисе.
	if s.cache != nil {
		key := fmt.Sprintf("tournament:leaderboard:%d", tournamentID)
		var cached []TournamentResult
		if v, ok := s.cache.GetWithCtx(ctx, key); ok {
			// In-memory: значение уже типизировано.
			if res, ok := v.([]TournamentResult); ok {
				return res, nil
			}
			// Valkey: JSON-строка/байты → unmarshal.
			if raw, ok := v.([]byte); ok {
				if err := json.Unmarshal(raw, &cached); err == nil {
					return cached, nil
				}
			} else if raw, ok := v.(string); ok {
				if err := json.Unmarshal([]byte(raw), &cached); err == nil {
					return cached, nil
				}
			}
		}
		results, err := s.tournamentResultRepo.GetLeaderboard(ctx, tournamentID)
		if err != nil {
			return nil, err
		}
		s.cache.SetWithCtx(ctx, key, results, 30*time.Second)
		return results, nil
	}
	return s.tournamentResultRepo.GetLeaderboard(ctx, tournamentID)
}
