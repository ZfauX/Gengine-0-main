// internal/domain/game/svc_review_test.go
// Тесты ReviewService: M13 (pass 30) — Create инвалидирует точечные кэши,
// но НЕ сбрасывает глобальную версию листинга.
package game_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ensureReviewsUniqueIndex добавляет production-индекс idx_reviews_game_user
// (миграция 000034), который GORM AutoMigrate не создаёт — ON CONFLICT в
// Create требует его.
func ensureReviewsUniqueIndex(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_game_user ON reviews(game_id, user_id)").Error)
}

// M13: создание отзыва инвалидирует rating/reviews/game, но не games:list:version.
func TestReviewService_Create_InvalidatesCachesWithoutListingVersion(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	ensureReviewsUniqueIndex(t, db)
	c := cache.NewCacheWithLRU(time.Minute, time.Minute, 1000)

	svc := game.NewReviewService(db).WithCache(c)
	ctx := context.Background()

	// Игра + автор + команда + капитан + завершённое прохождение (для CanReview).
	author := createUser(t, db, "review-author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Review Cache Game")
	tm := createTeam(t, db, author.ID)
	createPassing(t, db, g.ID, tm.ID, game.StatusFinished)

	// Закладываем кэши, которые должны быть инвалидированы.
	ratingKey := fmt.Sprintf("rating:game:%d", g.ID)
	reviewsKey := fmt.Sprintf("reviews:game:%d", g.ID)
	gameKey := fmt.Sprintf("game:%d", g.ID)
	c.SetWithCtx(ctx, ratingKey, 4.5, time.Minute)
	c.SetWithCtx(ctx, reviewsKey, []game.Review{{GameID: g.ID}}, time.Minute)
	c.SetWithCtx(ctx, gameKey, g, time.Minute)
	// Ставим версию листинга — она НЕ должна измениться.
	c.SetWithCtx(ctx, "games:list:version", int64(100), time.Minute)
	before, _ := c.GetWithCtx(ctx, "games:list:version")

	// Пользователь (не автор) оставляет отзыв; он — капитан команды с finished passing.
	user2 := createUser(t, db, "review-user@test.com", "pass")
	require.NoError(t, db.Model(tm).Update("captain_id", user2.ID).Error)
	err := svc.Create(context.Background(), g.ID, user2.ID, 5, "Отличная игра")
	require.NoError(t, err)

	// Точечные кэши удалены.
	_, ratingOK := c.GetWithCtx(ctx, ratingKey)
	assert.False(t, ratingOK, "rating:game:%d должен быть инвалидирован", g.ID)
	_, reviewsOK := c.GetWithCtx(ctx, reviewsKey)
	assert.False(t, reviewsOK, "reviews:game:%d должен быть инвалидирован", g.ID)
	_, gameOK := c.GetWithCtx(ctx, gameKey)
	assert.False(t, gameOK, "game:%d должен быть инвалидирован", g.ID)

	// Глобальная версия листинга НЕ тронута (M13).
	after, _ := c.GetWithCtx(ctx, "games:list:version")
	assert.Equal(t, before, after, "M13: games:list:version не должна меняться при создании отзыва")
}

// Дубликат отзыва отклоняется (ON CONFLICT DO NOTHING).
func TestReviewService_Create_DuplicateRejected(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	ensureReviewsUniqueIndex(t, db)
	svc := game.NewReviewService(db)

	author := createUser(t, db, "review-dupe-author@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Review Dupe Game")
	tm := createTeam(t, db, author.ID)

	user2 := createUser(t, db, "review-dupe-user@test.com", "pass")
	require.NoError(t, db.Model(tm).Update("captain_id", user2.ID).Error)
	createPassing(t, db, g.ID, tm.ID, game.StatusFinished)

	err := svc.Create(context.Background(), g.ID, user2.ID, 4, "Хорошо")
	require.NoError(t, err)
	err = svc.Create(context.Background(), g.ID, user2.ID, 5, "Повторный отзыв")
	require.Error(t, err, "повторный отзыв должен отклоняться")
	// Дубликат ловится в CanReview («вы не можете оставить отзыв»);
	// ветка ON CONFLICT «уже оставили отзыв» — защита от гонки.
	assert.NotEqual(t, "", err.Error())
}

// Валидация диапазона рейтинга.
func TestReviewService_Create_InvalidRating(t *testing.T) {
	db := testutil.SetupPostgresDB(t, allModels...)
	svc := game.NewReviewService(db)

	err := svc.Create(context.Background(), 1, 1, 0, "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "от 1 до 5")
	err = svc.Create(context.Background(), 1, 1, 6, "x")
	require.Error(t, err)
}
