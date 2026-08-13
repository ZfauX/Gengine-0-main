package tournament_test

import (
	"context"
	"testing"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/tournament"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTournamentDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t,
		&tournament.Tournament{},
		&tournament.TournamentGame{},
		&tournament.TournamentTeam{},
		&tournament.TournamentResult{},
		&game.Game{},
		&game.GamePassing{},
		&team.Team{},
		&user.User{},
	)
}

func createTournamentUser(t *testing.T, db *gorm.DB, email string) *user.User {
	u := &user.User{Email: email, Name: email, Password: "hashed"}
	require.NoError(t, db.Create(u).Error)
	return u
}

func createTournamentGame(t *testing.T, db *gorm.DB, authorID uint, name string) *game.Game {
	g := &game.Game{Name: name, AuthorID: authorID}
	require.NoError(t, db.Create(g).Error)
	return g
}

// ---------- Вспомогательные функции для создания сервисов с репозиториями ----------

func newTeamService(db *gorm.DB) *team.TeamService {
	teamRepo := team.NewGormTeamRepo(db)
	return team.NewTeamService(teamRepo)
}

func newTournamentService(db *gorm.DB, teamSvc *team.TeamService) *tournament.TournamentService {
	tournamentRepo := tournament.NewGormTournamentRepo(db)
	tournamentGameRepo := tournament.NewGormTournamentGameRepo(db)
	tournamentTeamRepo := tournament.NewGormTournamentTeamRepo(db)
	tournamentResultRepo := tournament.NewGormTournamentResultRepo(db)
	cfg := &config.Config{}
	return tournament.NewTournamentService(
		db,
		tournamentRepo,
		tournamentGameRepo,
		tournamentTeamRepo,
		tournamentResultRepo,
		teamSvc,
		cfg,
	)
}

// ---------- Тесты ----------

func TestTournamentService_CreateAndAddGame(t *testing.T) {
	db := setupTournamentDB(t)
	teamSvc := newTeamService(db)
	svc := newTournamentService(db, teamSvc)

	author := createTournamentUser(t, db, "auth@test.com")
	trn := &tournament.Tournament{Name: "Test Tour", AuthorID: author.ID}
	require.NoError(t, svc.Create(context.Background(), trn))

	g := createTournamentGame(t, db, author.ID, "Game 1")
	require.NoError(t, svc.AddGame(context.Background(), trn.ID, g.ID, author.ID))

	games, err := svc.ListGames(context.Background(), trn.ID)
	require.NoError(t, err)
	assert.Len(t, games, 1)
	assert.Equal(t, g.ID, games[0].ID)
}

func TestTournamentService_Apply(t *testing.T) {
	db := setupTournamentDB(t)
	teamSvc := newTeamService(db)
	svc := newTournamentService(db, teamSvc)

	author := createTournamentUser(t, db, "auth@test.com")
	cap := createTournamentUser(t, db, "cap@test.com")
	trn := &tournament.Tournament{Name: "T", AuthorID: author.ID}
	require.NoError(t, svc.Create(context.Background(), trn))

	tm, _ := teamSvc.CreateTeam(context.Background(), "Team", cap.ID)

	err := svc.Apply(context.Background(), trn.ID, tm.ID, cap.ID)
	require.NoError(t, err)

	var tt tournament.TournamentTeam
	require.NoError(t, db.Where("tournament_id = ? AND team_id = ?", trn.ID, tm.ID).First(&tt).Error)
	assert.Equal(t, tm.ID, tt.TeamID)
}

func TestTournamentService_Leaderboard(t *testing.T) {
	db := setupTournamentDB(t)
	teamSvc := newTeamService(db)
	svc := newTournamentService(db, teamSvc)

	author := createTournamentUser(t, db, "lb@test.com")
	trn := &tournament.Tournament{
		Name:                   "Leaderboard",
		AuthorID:               author.ID,
		PointsForFirst:         10,
		PointsForSecond:        7,
		PointsForThird:         5,
		PointsForParticipation: 2,
	}
	require.NoError(t, svc.Create(context.Background(), trn))

	tm1, _ := teamSvc.CreateTeam(context.Background(), "T1", author.ID)
	// PASS-9 (reviewer #6): один пользователь не может быть капитаном двух команд —
	// второй капитан создаётся отдельным пользователем.
	cap2 := createTournamentUser(t, db, "lb-cap2@test.com")
	tm2, _ := teamSvc.CreateTeam(context.Background(), "T2", cap2.ID)

	db.Create(&tournament.TournamentResult{TournamentID: trn.ID, TeamID: tm1.ID, Score: 12, GamesPlayed: 2})
	db.Create(&tournament.TournamentResult{TournamentID: trn.ID, TeamID: tm2.ID, Score: 8, GamesPlayed: 2})

	results, err := svc.GetLeaderboard(context.Background(), trn.ID)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, tm1.ID, results[0].TeamID)
	assert.Equal(t, tm2.ID, results[1].TeamID)
}

func intPtr(v int) *int { return &v }

// TestTournamentService_UpdateScoresForGame: места 1/2 начисляются, прохождения
// помечаются tournament_scored, сохраняется точное значение tournament_points,
// повторный вызов не удваивает очки (T-H1).
func TestTournamentService_UpdateScoresForGame(t *testing.T) {
	db := setupTournamentDB(t)
	teamSvc := newTeamService(db)
	svc := newTournamentService(db, teamSvc)
	ctx := context.Background()

	author := createTournamentUser(t, db, "sc@test.com")
	trn := &tournament.Tournament{
		Name: "Scoring", AuthorID: author.ID,
		PointsForFirst: 10, PointsForSecond: 7, PointsForThird: 5, PointsForParticipation: 2,
	}
	require.NoError(t, svc.Create(ctx, trn))

	g := createTournamentGame(t, db, author.ID, "G1")
	require.NoError(t, svc.AddGame(ctx, trn.ID, g.ID, author.ID))

	tm1, _ := teamSvc.CreateTeam(ctx, "T1", author.ID)
	// PASS-9 (reviewer #6): разные капитаны для двух команд.
	cap2 := createTournamentUser(t, db, "score-cap2@test.com")
	tm2, _ := teamSvc.CreateTeam(ctx, "T2", cap2.ID)
	require.NoError(t, db.Create(&tournament.TournamentTeam{TournamentID: trn.ID, TeamID: tm1.ID}).Error)
	require.NoError(t, db.Create(&tournament.TournamentTeam{TournamentID: trn.ID, TeamID: tm2.ID}).Error)

	p1 := &game.GamePassing{GameID: g.ID, TeamID: tm1.ID, Status: game.StatusFinished, Place: intPtr(1)}
	p2 := &game.GamePassing{GameID: g.ID, TeamID: tm2.ID, Status: game.StatusFinished, Place: intPtr(2)}
	require.NoError(t, db.Create(p1).Error)
	require.NoError(t, db.Create(p2).Error)

	svc.UpdateScoresForGame(ctx, g.ID)

	var r1, r2 tournament.TournamentResult
	require.NoError(t, db.Where("team_id = ?", tm1.ID).First(&r1).Error)
	assert.Equal(t, 10, r1.Score)
	assert.Equal(t, 1, r1.GamesPlayed)
	require.NoError(t, db.Where("team_id = ?", tm2.ID).First(&r2).Error)
	assert.Equal(t, 7, r2.Score)

	var storedP1 game.GamePassing
	require.NoError(t, db.First(&storedP1, p1.ID).Error)
	// DEEP-REVIEW (pass 46): вместо булева флага — массив начисленных турниров.
	assert.Contains(t, storedP1.TournamentScoredIDs, int64(trn.ID))
	assert.Equal(t, 10, storedP1.TournamentPoints)

	// Идемпотентность: второй вызов не удваивает.
	svc.UpdateScoresForGame(ctx, g.ID)
	require.NoError(t, db.Where("team_id = ?", tm1.ID).First(&r1).Error)
	assert.Equal(t, 10, r1.Score)
}

// TestTournamentService_RemoveGame: удаление игры, если очки НЕ начислены,
// работает; после начисления очков автору ЗАПРЕЩЕНО (PASS-9 security HIGH #1:
// автор мог обнулить чужие очки перед закрытием турнира), админу — можно.
// Повторный AddGame той же игры невозможен (uniqueIndex + soft-delete).
func TestTournamentService_RemoveGame(t *testing.T) {
	db := setupTournamentDB(t)
	teamSvc := newTeamService(db)
	svc := newTournamentService(db, teamSvc)
	ctx := context.Background()

	author := createTournamentUser(t, db, "rm@test.com")
	trn := &tournament.Tournament{
		Name: "Remove", AuthorID: author.ID,
		PointsForFirst: 10, PointsForSecond: 7, PointsForThird: 5, PointsForParticipation: 2,
	}
	require.NoError(t, svc.Create(ctx, trn))

	// Игра 1: удаление ДО начисления — допустимо.
	g1 := createTournamentGame(t, db, author.ID, "G1")
	require.NoError(t, svc.AddGame(ctx, trn.ID, g1.ID, author.ID))
	require.NoError(t, svc.RemoveGame(ctx, trn.ID, g1.ID, author.ID, false))

	// Игра 2: после начисления очков автору запрещено, админу — можно.
	g2 := createTournamentGame(t, db, author.ID, "G2")
	require.NoError(t, svc.AddGame(ctx, trn.ID, g2.ID, author.ID))

	tm1, _ := teamSvc.CreateTeam(ctx, "T1", author.ID)
	require.NoError(t, db.Create(&tournament.TournamentTeam{TournamentID: trn.ID, TeamID: tm1.ID}).Error)

	p1 := &game.GamePassing{GameID: g2.ID, TeamID: tm1.ID, Status: game.StatusFinished, Place: intPtr(1)}
	require.NoError(t, db.Create(p1).Error)
	svc.UpdateScoresForGame(ctx, g2.ID)

	var r1 tournament.TournamentResult
	require.NoError(t, db.Where("team_id = ?", tm1.ID).First(&r1).Error)
	assert.Equal(t, 10, r1.Score)

	// Автору после начисления удаление ЗАПРЕЩЕНО (PASS-9 security HIGH #1).
	require.Error(t, svc.RemoveGame(ctx, trn.ID, g2.ID, author.ID, false))

	// Админу разрешено (поддержка/исправление) — очки списываются.
	require.NoError(t, svc.RemoveGame(ctx, trn.ID, g2.ID, author.ID, true))

	var count int64
	require.NoError(t, db.Model(&tournament.TournamentResult{}).Where("team_id = ?", tm1.ID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

// DEEP-REVIEW (pass 46): игра в 2 турнирах получает очки в ОБОИХ.
func TestTournamentService_UpdateScoresForGame_MultiTournament(t *testing.T) {
	db := setupTournamentDB(t)
	teamSvc := newTeamService(db)
	svc := newTournamentService(db, teamSvc)
	ctx := context.Background()

	author := createTournamentUser(t, db, "multi@test.com")
	trnA := &tournament.Tournament{Name: "A", AuthorID: author.ID, PointsForFirst: 10, PointsForParticipation: 2}
	trnB := &tournament.Tournament{Name: "B", AuthorID: author.ID, PointsForFirst: 20, PointsForParticipation: 3}
	require.NoError(t, svc.Create(ctx, trnA))
	require.NoError(t, svc.Create(ctx, trnB))

	g := createTournamentGame(t, db, author.ID, "G-Multi")
	require.NoError(t, svc.AddGame(ctx, trnA.ID, g.ID, author.ID))
	require.NoError(t, svc.AddGame(ctx, trnB.ID, g.ID, author.ID))

	tm1, _ := teamSvc.CreateTeam(ctx, "TM", author.ID)
	require.NoError(t, db.Create(&tournament.TournamentTeam{TournamentID: trnA.ID, TeamID: tm1.ID}).Error)
	require.NoError(t, db.Create(&tournament.TournamentTeam{TournamentID: trnB.ID, TeamID: tm1.ID}).Error)

	p1 := &game.GamePassing{GameID: g.ID, TeamID: tm1.ID, Status: game.StatusFinished, Place: intPtr(1)}
	require.NoError(t, db.Create(p1).Error)

	// Начисляем очки: сначала турнир A, затем B.
	svc.UpdateScoresForGame(ctx, g.ID)

	var rA tournament.TournamentResult
	require.NoError(t, db.Where("tournament_id = ? AND team_id = ?", trnA.ID, tm1.ID).First(&rA).Error)
	assert.Equal(t, 10, rA.Score, "турнир A должен получить очки")

	svc.UpdateScoresForGame(ctx, g.ID)

	var rB tournament.TournamentResult
	require.NoError(t, db.Where("tournament_id = ? AND team_id = ?", trnB.ID, tm1.ID).First(&rB).Error)
	assert.Equal(t, 20, rB.Score, "турнир B должен получить очки (раньше терялись)")

	// Идемпотентность: третий вызов не удваивает.
	svc.UpdateScoresForGame(ctx, g.ID)
	require.NoError(t, db.Where("tournament_id = ? AND team_id = ?", trnA.ID, tm1.ID).First(&rA).Error)
	assert.Equal(t, 10, rA.Score)
	require.NoError(t, db.Where("tournament_id = ? AND team_id = ?", trnB.ID, tm1.ID).First(&rB).Error)
	assert.Equal(t, 20, rB.Score)
}

// H2 (PASS-7): RemoveGame из одного турнира удаляет элемент из ОБОИХ
// параллельных массивов (scored_ids и scored_points) по ПОЗИЦИИ. Раньше
// удалялся только id, scored_points оставался — индексы разъезжались, и
// повторное начисление/списание использовало устаревшее значение очков.
func TestTournamentService_RemoveGame_MultiTournamentParallelArrays(t *testing.T) {
	db := setupTournamentDB(t)
	teamSvc := newTeamService(db)
	svc := newTournamentService(db, teamSvc)
	ctx := context.Background()

	author := createTournamentUser(t, db, "h2@test.com")
	// Очки первого места: в A=10, в B=20 — значения РАЗНЫЕ, чтобы ловить
	// удаление по значению (array_remove(20) не должен удалить очки B).
	trnA := &tournament.Tournament{Name: "A", AuthorID: author.ID, PointsForFirst: 10, PointsForParticipation: 2}
	trnB := &tournament.Tournament{Name: "B", AuthorID: author.ID, PointsForFirst: 20, PointsForParticipation: 3}
	require.NoError(t, svc.Create(ctx, trnA))
	require.NoError(t, svc.Create(ctx, trnB))

	g := createTournamentGame(t, db, author.ID, "G-H2")
	require.NoError(t, svc.AddGame(ctx, trnA.ID, g.ID, author.ID))
	require.NoError(t, svc.AddGame(ctx, trnB.ID, g.ID, author.ID))

	tm1, _ := teamSvc.CreateTeam(ctx, "H2", author.ID)
	require.NoError(t, db.Create(&tournament.TournamentTeam{TournamentID: trnA.ID, TeamID: tm1.ID}).Error)
	require.NoError(t, db.Create(&tournament.TournamentTeam{TournamentID: trnB.ID, TeamID: tm1.ID}).Error)

	p1 := &game.GamePassing{GameID: g.ID, TeamID: tm1.ID, Status: game.StatusFinished, Place: intPtr(1)}
	require.NoError(t, db.Create(p1).Error)

	svc.UpdateScoresForGame(ctx, g.ID)
	svc.UpdateScoresForGame(ctx, g.ID)

	var pAfter game.GamePassing
	require.NoError(t, db.First(&pAfter, p1.ID).Error)
	require.Len(t, pAfter.TournamentScoredIDs, 2, "оба турнира начислены")
	require.Len(t, pAfter.TournamentScoredPoints, 2, "оба параллельных массива заполнены")

	// Удаляем игру из турнира A (как админ — автору запрещено после начисления,
	// PASS-9 security HIGH #1) — из обоих массивов уходит первый элемент.
	require.NoError(t, svc.RemoveGame(ctx, trnA.ID, g.ID, author.ID, true))

	var pRem game.GamePassing
	require.NoError(t, db.First(&pRem, p1.ID).Error)
	require.Len(t, pRem.TournamentScoredIDs, 1, "id турнира A удалён из массива")
	require.Len(t, pRem.TournamentScoredPoints, 1, "очки турнира A удалены из параллельного массива")
	require.Equal(t, int64(trnB.ID), pRem.TournamentScoredIDs[0])
	require.Equal(t, int64(20), pRem.TournamentScoredPoints[0], "оставшиеся очки принадлежат турниру B (20), а не A (10)")
}
