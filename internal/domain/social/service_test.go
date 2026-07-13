// internal/domain/social/service_test.go
package social_test

import (
	"context"
	"testing"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/social"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSocialDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t,
		&social.PlayerRating{}, &social.Follow{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{},
		&game.Review{}, &game.PlayerRating{},
		&team.Team{},
		&user.User{},
	)
}

// ---------- RatingService ----------

func TestRatingService_UpdateRatingsForGame(t *testing.T) {
	db := setupSocialDB(t)
	// Передаём nil вместо кэша (в тестах кэш не нужен)
	rs := game.NewRatingService(db, nil)

	author := createUser(t, db, "author@test.com", "pass")
	player := createUser(t, db, "player@test.com", "pass")
	g := createGame(t, db, author.ID, "Rating Game")

	tm := createTeam(t, db, player.ID)
	passing := &game.GamePassing{
		GameID: g.ID,
		TeamID: tm.ID,
		Status: game.StatusFinished,
		Place:  intPtr(1),
	}
	require.NoError(t, db.Create(passing).Error)
	require.NoError(t, db.Exec("INSERT INTO team_members (team_id, user_id) VALUES (?, ?)", tm.ID, player.ID).Error)

	err := rs.UpdateRatingsForGame(context.Background(), g.ID)
	require.NoError(t, err)

	var authorRating game.PlayerRating
	db.Where("user_id = ?", author.ID).First(&authorRating)
	assert.Equal(t, 5, authorRating.Score)

	var playerRating game.PlayerRating
	db.Where("user_id = ?", player.ID).First(&playerRating)
	assert.Equal(t, 10, playerRating.Score)
}

func TestRatingService_GetLeaderboard(t *testing.T) {
	db := setupSocialDB(t)
	rs := game.NewRatingService(db, nil)

	u1 := createUser(t, db, "u1@test.com", "pass")
	u2 := createUser(t, db, "u2@test.com", "pass")

	db.Create(&game.PlayerRating{UserID: u1.ID, Score: 100})
	db.Create(&game.PlayerRating{UserID: u2.ID, Score: 200})

	board, err := rs.GetLeaderboard(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, board, 2)
	assert.Equal(t, u2.ID, board[0].UserID)
}

func TestRatingService_GetLeaderboardEmpty(t *testing.T) {
	db := setupSocialDB(t)
	rs := game.NewRatingService(db, nil)

	board, err := rs.GetLeaderboard(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, board, 0)
}

func TestRatingService_UpdateRatingsForGame_NoPassings(t *testing.T) {
	db := setupSocialDB(t)
	rs := game.NewRatingService(db, nil)

	author := createUser(t, db, "author@test.com", "pass")
	g := createGame(t, db, author.ID, "No Passings")

	err := rs.UpdateRatingsForGame(context.Background(), g.ID)
	require.NoError(t, err)

	var authorRating game.PlayerRating
	err = db.Where("user_id = ?", author.ID).First(&authorRating).Error
	require.NoError(t, err)
	assert.Equal(t, 5, authorRating.Score)

	var count int64
	db.Model(&game.PlayerRating{}).Where("user_id != ?", author.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}

// ---------- FollowService ----------

func TestFollowService_FollowAndUnfollow(t *testing.T) {
	db := setupSocialDB(t)
	ctx := context.Background()
	followRepo := social.NewGormFollowRepo(db)
	fs := social.NewFollowService(followRepo)

	follower := createUser(t, db, "follower@test.com", "pass")
	author := createUser(t, db, "author@test.com", "pass")

	err := fs.Follow(ctx, follower.ID, author.ID)
	require.NoError(t, err)
	assert.True(t, fs.IsFollowing(ctx, follower.ID, author.ID))

	err = fs.Follow(ctx, follower.ID, author.ID)
	require.NoError(t, err)

	err = fs.Unfollow(ctx, follower.ID, author.ID)
	require.NoError(t, err)
	assert.False(t, fs.IsFollowing(ctx, follower.ID, author.ID))
}

func TestFollowService_GetSubscriptions(t *testing.T) {
	db := setupSocialDB(t)
	ctx := context.Background()
	followRepo := social.NewGormFollowRepo(db)
	fs := social.NewFollowService(followRepo)

	follower := createUser(t, db, "follower@test.com", "pass")
	author1 := createUser(t, db, "a1@test.com", "pass")
	author2 := createUser(t, db, "a2@test.com", "pass")

	_ = fs.Follow(ctx, follower.ID, author1.ID)
	_ = fs.Follow(ctx, follower.ID, author2.ID)

	authors, err := fs.GetSubscriptions(ctx, follower.ID)
	require.NoError(t, err)
	assert.Len(t, authors, 2)
}

func TestFollowService_UnfollowWhenNotFollowing(t *testing.T) {
	db := setupSocialDB(t)
	ctx := context.Background()
	followRepo := social.NewGormFollowRepo(db)
	fs := social.NewFollowService(followRepo)

	follower := createUser(t, db, "f@test.com", "pass")
	author := createUser(t, db, "a@test.com", "pass")

	err := fs.Unfollow(ctx, follower.ID, author.ID)
	assert.NoError(t, err)
	assert.False(t, fs.IsFollowing(ctx, follower.ID, author.ID))
}

func TestFollowService_IsFollowingFalse(t *testing.T) {
	db := setupSocialDB(t)
	ctx := context.Background()
	followRepo := social.NewGormFollowRepo(db)
	fs := social.NewFollowService(followRepo)

	follower := createUser(t, db, "f@test.com", "pass")
	author := createUser(t, db, "a@test.com", "pass")

	assert.False(t, fs.IsFollowing(ctx, follower.ID, author.ID))
}

func TestFollowService_GetSubscriptionsEmpty(t *testing.T) {
	db := setupSocialDB(t)
	ctx := context.Background()
	followRepo := social.NewGormFollowRepo(db)
	fs := social.NewFollowService(followRepo)

	u := createUser(t, db, "u@test.com", "pass")

	authors, err := fs.GetSubscriptions(ctx, u.ID)
	require.NoError(t, err)
	assert.Len(t, authors, 0)
}

// ---------- Вспомогательные функции ----------

func createUser(t *testing.T, db *gorm.DB, email, _ string) *user.User {
	t.Helper()
	u := &user.User{Email: email, Password: "hashed", Name: email}
	require.NoError(t, db.Create(u).Error)
	return u
}

func createGame(t *testing.T, db *gorm.DB, authorID uint, name string) *game.Game {
	t.Helper()
	g := &game.Game{Name: name, AuthorID: authorID, IsDraft: false}
	require.NoError(t, db.Create(g).Error)
	db.Model(g).Update("is_draft", false)
	return g
}

func createTeam(t *testing.T, db *gorm.DB, captainID uint) *team.Team {
	t.Helper()
	tm := &team.Team{Name: "Team", CaptainID: captainID}
	require.NoError(t, db.Create(tm).Error)
	return tm
}

func intPtr(i int) *int { return &i }
