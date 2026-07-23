// internal/domain/game/game_listing_service_test.go
package game_test

import (
	"context"
	"testing"
	"time"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupGameListingTest создаёт изолированную БД с моделями и search_vector колонкой.
func setupGameListingTest(t *testing.T) (*gorm.DB, *game.GameListingService) {
	t.Helper()
	db := testutil.SetupPostgresDB(t, allModels...)

	// Добавляем search_vector колонку, необходимую для полнотекстового поиска
	db.Exec(`ALTER TABLE games ADD COLUMN IF NOT EXISTS search_vector tsvector;`)

	repo := game.NewGormGameRepo(db)
	svc := game.NewGameListingService(repo)
	return db, svc
}

// createDraftGame создаёт черновик игры.
func createDraftGame(t *testing.T, db *gorm.DB, authorID uint, name string) *game.Game {
	t.Helper()
	g := &game.Game{
		Name:          name,
		Description:   "Draft game",
		AuthorID:      authorID,
		Visibility:    "public",
		IsDraft:       true,
		MaxTeamNumber: 10,
	}
	require.NoError(t, db.Create(g).Error)
	return g
}

// createGameWithStartsAt создаёт игру с указанной датой начала.
func createGameWithStartsAt(t *testing.T, db *gorm.DB, authorID uint, name string, startsAt time.Time) *game.Game {
	t.Helper()
	regDeadline := startsAt.Add(-24 * time.Hour)
	g := &game.Game{
		Name:                 name,
		Description:          "Scheduled game",
		AuthorID:             authorID,
		Visibility:           "public",
		IsDraft:              false,
		StartsAt:             &startsAt,
		RegistrationDeadline: &regDeadline,
		MaxTeamNumber:        10,
	}
	require.NoError(t, db.Create(g).Error)
	return g
}

func TestGameListingService_ListFilteredPaginated_NoFilter(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")

	g1 := createPublishedGameWithSettings(t, db, author.ID, "Game One")
	g2 := createPublishedGameWithSettings(t, db, author.ID, "Game Two")

	filter := game.GameFilter{ViewerID: author.ID}
	games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, games, 2)

	ids := []uint{games[0].ID, games[1].ID}
	assert.Contains(t, ids, g1.ID)
	assert.Contains(t, ids, g2.ID)
}

func TestGameListingService_ListFilteredPaginated_WithSearch(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")

	createPublishedGameWithSettings(t, db, author.ID, "Alpha Quest")
	createPublishedGameWithSettings(t, db, author.ID, "Beta Adventure")

	filter := game.GameFilter{
		ViewerID: author.ID,
		Search:   "Alpha",
	}
	games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, games, 1)
	assert.Equal(t, "Alpha Quest", games[0].Name)
}

func TestGameListingService_ListFilteredPaginated_DraftFilter(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")

	createDraftGame(t, db, author.ID, "Draft Game")
	createPublishedGameWithSettings(t, db, author.ID, "Published Game")

	t.Run("filterDraft", func(t *testing.T) {
		filter := game.GameFilter{
			ViewerID: author.ID,
			Status:   "draft",
		}
		games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Len(t, games, 1)
		assert.Equal(t, "Draft Game", games[0].Name)
		assert.True(t, games[0].IsDraft)
	})

	t.Run("filterPublished", func(t *testing.T) {
		filter := game.GameFilter{
			ViewerID: author.ID,
			Status:   "published",
		}
		games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Len(t, games, 1)
		assert.Equal(t, "Published Game", games[0].Name)
		assert.False(t, games[0].IsDraft)
	})
}

func TestGameListingService_ListFilteredPaginated_Pagination(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")

	for i := 1; i <= 5; i++ {
		createPublishedGameWithSettings(t, db, author.ID, "Game")
	}

	filter := game.GameFilter{ViewerID: author.ID}

	t.Run("page 1, perPage 2", func(t *testing.T) {
		games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(5), total)
		assert.Len(t, games, 2)
	})

	t.Run("page 3, perPage 2", func(t *testing.T) {
		games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 3, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(5), total)
		assert.Len(t, games, 1)
	})

	t.Run("page out of range", func(t *testing.T) {
		games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 10, 2)
		require.NoError(t, err)
		assert.Equal(t, int64(5), total)
		assert.Len(t, games, 0)
	})
}

func TestGameListingService_ListFilteredPaginated_SortByName(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")

	createPublishedGameWithSettings(t, db, author.ID, "C Game")
	createPublishedGameWithSettings(t, db, author.ID, "A Game")
	createPublishedGameWithSettings(t, db, author.ID, "B Game")

	filter := game.GameFilter{ViewerID: author.ID}
	sort := &game.GameSort{
		Field: "name",
		Order: game.SortAsc,
	}

	games, total, err := svc.ListFilteredPaginated(context.Background(), filter, sort, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, games, 3)
	assert.Equal(t, "A Game", games[0].Name)
	assert.Equal(t, "B Game", games[1].Name)
	assert.Equal(t, "C Game", games[2].Name)
}

func TestGameListingService_ListFilteredPaginated_SortByStartsAt(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	now := time.Now()

	createGameWithStartsAt(t, db, author.ID, "Late Game", now.Add(48*time.Hour))
	createGameWithStartsAt(t, db, author.ID, "Early Game", now.Add(24*time.Hour))

	filter := game.GameFilter{ViewerID: author.ID}
	sort := &game.GameSort{
		Field: "starts_at",
		Order: game.SortAsc,
	}

	games, total, err := svc.ListFilteredPaginated(context.Background(), filter, sort, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, games, 2)
	assert.Equal(t, "Early Game", games[0].Name)
	assert.Equal(t, "Late Game", games[1].Name)
}

func TestGameListingService_ListFilteredPaginated_AuthorIDFilter(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author1 := createUser(t, db, "author1@test.com", "pass")
	author2 := createUser(t, db, "author2@test.com", "pass")

	createPublishedGameWithSettings(t, db, author1.ID, "Author1 Game")
	createPublishedGameWithSettings(t, db, author2.ID, "Author2 Game")

	author1ID := author1.ID
	filter := game.GameFilter{
		ViewerID: author1.ID,
		AuthorID: &author1ID,
	}

	games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, games, 1)
	assert.Equal(t, "Author1 Game", games[0].Name)
}

func TestGameListingService_ListByDateRange(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	now := time.Now()

	futureStart := now.Add(7 * 24 * time.Hour)
	createGameWithStartsAt(t, db, author.ID, "Future Game", futureStart)

	from := now.Add(6 * 24 * time.Hour)
	to := now.Add(8 * 24 * time.Hour)

	games, err := svc.ListByDateRange(context.Background(), from, to)
	require.NoError(t, err)
	require.Len(t, games, 1)
	assert.Equal(t, "Future Game", games[0].Name)
}

func TestGameListingService_ListByDateRange_NoGames(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	now := time.Now()

	futureStart := now.Add(7 * 24 * time.Hour)
	createGameWithStartsAt(t, db, author.ID, "Future Game", futureStart)

	from := now.Add(1 * 24 * time.Hour)
	to := now.Add(3 * 24 * time.Hour)

	games, err := svc.ListByDateRange(context.Background(), from, to)
	require.NoError(t, err)
	assert.Len(t, games, 0)
}

func TestGameListingService_ListFilteredPaginated_ViewerSeesOwnDrafts(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	otherUser := createUser(t, db, "other@test.com", "pass")

	createDraftGame(t, db, author.ID, "My Draft")
	createPublishedGameWithSettings(t, db, author.ID, "My Published")

	// Viewer is the author themselves — sees both
	filter := game.GameFilter{ViewerID: author.ID}
	games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, games, 2)

	// Other user should only see the published one
	filter2 := game.GameFilter{ViewerID: otherUser.ID}
	games2, total2, err := svc.ListFilteredPaginated(context.Background(), filter2, nil, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total2)
	assert.Len(t, games2, 1)
	assert.Equal(t, "My Published", games2[0].Name)
}

func TestGameListingService_ListFilteredPaginated_SortDesc(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")

	createPublishedGameWithSettings(t, db, author.ID, "A Game")
	createPublishedGameWithSettings(t, db, author.ID, "B Game")
	createPublishedGameWithSettings(t, db, author.ID, "C Game")

	filter := game.GameFilter{ViewerID: author.ID}
	sort := &game.GameSort{
		Field: "name",
		Order: game.SortDesc,
	}

	games, total, err := svc.ListFilteredPaginated(context.Background(), filter, sort, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, games, 3)
	assert.Equal(t, "C Game", games[0].Name)
	assert.Equal(t, "B Game", games[1].Name)
	assert.Equal(t, "A Game", games[2].Name)
}

func TestGameListingService_ListFilteredPaginated_EmptyResult(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")

	filter := game.GameFilter{ViewerID: author.ID}
	games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Len(t, games, 0)
}

func TestGameListingService_ListFilteredPaginated_DateRangeFilter(t *testing.T) {
	db, svc := setupGameListingTest(t)
	author := createUser(t, db, "author@test.com", "pass")
	now := time.Now()

	g1 := createGameWithStartsAt(t, db, author.ID, "Early", now.Add(24*time.Hour))
	createGameWithStartsAt(t, db, author.ID, "Middle", now.Add(72*time.Hour))
	g3 := createGameWithStartsAt(t, db, author.ID, "Late", now.Add(144*time.Hour))

	t.Run("DateFrom filter", func(t *testing.T) {
		filter := game.GameFilter{
			ViewerID: author.ID,
			DateFrom: now.Add(96 * time.Hour).Format("2006-01-02"),
		}
		games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, g3.ID, games[0].ID)
	})

	t.Run("DateTo filter", func(t *testing.T) {
		filter := game.GameFilter{
			ViewerID: author.ID,
			DateTo:   now.Add(48 * time.Hour).Format("2006-01-02"),
		}
		games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, g1.ID, games[0].ID)
	})

	t.Run("DateFrom and DateTo", func(t *testing.T) {
		filter := game.GameFilter{
			ViewerID: author.ID,
			DateFrom: now.Add(48 * time.Hour).Format("2006-01-02"),
			DateTo:   now.Add(96 * time.Hour).Format("2006-01-02"),
		}
		games, total, err := svc.ListFilteredPaginated(context.Background(), filter, nil, 1, 10)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Len(t, games, 1)
	})
}
