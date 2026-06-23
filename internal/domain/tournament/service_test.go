package tournament_test

import (
	"testing"

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

func TestTournamentService_CreateAndAddGame(t *testing.T) {
	db := setupTournamentDB(t)
	teamSvc := team.NewTeamService(db)
	svc := tournament.NewTournamentService(db, teamSvc, nil)

	author := createTournamentUser(t, db, "auth@test.com")
	trn := &tournament.Tournament{Name: "Test Tour", AuthorID: author.ID}
	require.NoError(t, svc.Create(trn))

	g := createTournamentGame(t, db, author.ID, "Game 1")
	require.NoError(t, svc.AddGame(trn.ID, g.ID, author.ID))

	games, err := svc.ListGames(trn.ID)
	require.NoError(t, err)
	assert.Len(t, games, 1)
	assert.Equal(t, g.ID, games[0].ID)
}

func TestTournamentService_Apply(t *testing.T) {
	db := setupTournamentDB(t)
	teamSvc := team.NewTeamService(db)
	svc := tournament.NewTournamentService(db, teamSvc, nil)

	author := createTournamentUser(t, db, "auth@test.com")
	cap := createTournamentUser(t, db, "cap@test.com")
	trn := &tournament.Tournament{Name: "T", AuthorID: author.ID}
	require.NoError(t, svc.Create(trn))

	tm, _ := teamSvc.CreateTeam("Team", cap.ID)

	err := svc.Apply(trn.ID, tm.ID, cap.ID)
	require.NoError(t, err)

	var tt tournament.TournamentTeam
	require.NoError(t, db.Where("tournament_id = ? AND team_id = ?", trn.ID, tm.ID).First(&tt).Error)
	assert.Equal(t, tm.ID, tt.TeamID)
}

func TestTournamentService_Leaderboard(t *testing.T) {
	db := setupTournamentDB(t)
	teamSvc := team.NewTeamService(db)
	svc := tournament.NewTournamentService(db, teamSvc, nil)

	author := createTournamentUser(t, db, "lb@test.com")
	trn := &tournament.Tournament{
		Name:                  "Leaderboard",
		AuthorID:              author.ID,
		PointsForFirst:        10,
		PointsForSecond:       7,
		PointsForThird:        5,
		PointsForParticipation: 2,
	}
	require.NoError(t, svc.Create(trn))

	tm1, _ := teamSvc.CreateTeam("T1", author.ID)
	tm2, _ := teamSvc.CreateTeam("T2", author.ID)

	db.Create(&tournament.TournamentResult{TournamentID: trn.ID, TeamID: tm1.ID, Score: 12, GamesPlayed: 2})
	db.Create(&tournament.TournamentResult{TournamentID: trn.ID, TeamID: tm2.ID, Score: 8, GamesPlayed: 2})

	results, err := svc.GetLeaderboard(trn.ID)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, tm1.ID, results[0].TeamID)
	assert.Equal(t, tm2.ID, results[1].TeamID)
}
