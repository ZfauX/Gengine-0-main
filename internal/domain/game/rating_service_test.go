// internal/domain/game/rating_service_test.go
package game_test

import (
	"context"
	"testing"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ratingModels — модели для тестов рейтинга (PlayerRating добавляется к базовым).
var ratingModels = []interface{}{
	&user.User{},
	&game.Game{},
	&game.GamePassing{},
	&team.Team{},
	&game.PlayerRating{},
}

func newRatingTestService(t *testing.T) (*gorm.DB, *game.RatingService) {
	t.Helper()
	db := testutil.SetupPostgresDB(t, ratingModels...)
	c, err := cache.NewCache(5*60*1000, 60*1000)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return db, game.NewRatingService(db, c).WithRepository(game.NewGormRatingRepo(db))
}

// D1b: UpdateRatingsForGame начисляет очки автору, участникам команд и капитанам.
func TestRatingService_UpdateRatingsForGame(t *testing.T) {
	db, ratingSvc := newRatingTestService(t)

	author := user.User{Email: "rating-author@test.com", Password: "pass", Name: "A", Role: "user"}
	require.NoError(t, db.Create(&author).Error)
	member := user.User{Email: "rating-member@test.com", Password: "pass", Name: "M", Role: "user"}
	require.NoError(t, db.Create(&member).Error)
	captain := user.User{Email: "rating-cap@test.com", Password: "pass", Name: "C", Role: "user"}
	require.NoError(t, db.Create(&captain).Error)

	g := game.Game{Name: "Rating Game", AuthorID: author.ID, Visibility: "public"}
	require.NoError(t, db.Create(&g).Error)

	tm := team.Team{Name: "Rating Team", CaptainID: captain.ID}
	require.NoError(t, db.Create(&tm).Error)
	require.NoError(t, db.Exec("INSERT INTO team_members (team_id, user_id) VALUES (?, ?)", tm.ID, member.ID).Error)

	// Завершённое прохождение с местом 1.
	place := 1
	passing := game.GamePassing{GameID: g.ID, TeamID: tm.ID, Status: game.StatusFinished, Place: &place}
	require.NoError(t, db.Create(&passing).Error)

	err := ratingSvc.UpdateRatingsForGame(context.Background(), g.ID)
	require.NoError(t, err)

	// Автор: +5 за создание.
	assertRating(t, db, author.ID, 5)
	// Капитан: +10 за 1-е место.
	assertRating(t, db, captain.ID, 10)
	// Участник: +10 за 1-е место.
	assertRating(t, db, member.ID, 10)

	// Повторный вызов ничего не начисляет (идемпотентность rating_scored).
	require.NoError(t, ratingSvc.UpdateRatingsForGame(context.Background(), g.ID))
	assertRating(t, db, author.ID, 5)
	assertRating(t, db, captain.ID, 10)
}

func assertRating(t *testing.T, db *gorm.DB, userID uint, want int) {
	t.Helper()
	var pr game.PlayerRating
	require.NoError(t, db.First(&pr, "user_id = ?", userID).Error)
	assert.Equal(t, want, pr.Score)
}
