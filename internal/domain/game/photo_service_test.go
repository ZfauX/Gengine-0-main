package game_test

import (
	"context"
	"testing"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPhotoTest(t *testing.T) (*gorm.DB, *game.PhotoService) {
	t.Helper()
	db := testutil.SetupPostgresDB(t, allModels...)
	photoSvc := game.NewPhotoService(db)
	return db, photoSvc
}

func TestPhotoService_Create(t *testing.T) {
	db, photoSvc := setupPhotoTest(t)
	author := createUser(t, db, "create_photo@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Create Photo Game")

	photo := &game.Photo{
		GameID: g.ID,
		UserID: author.ID,
		Path:   "uploads/test.jpg",
	}
	err := photoSvc.Create(photo)
	require.NoError(t, err)
	assert.NotZero(t, photo.ID)

	var count int64
	db.Model(&game.Photo{}).Where("id = ?", photo.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestPhotoService_ListByGame(t *testing.T) {
	db, photoSvc := setupPhotoTest(t)
	author := createUser(t, db, "list_photos@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "List Photo Game")

	photo1 := &game.Photo{GameID: g.ID, UserID: author.ID, Path: "uploads/1.jpg"}
	photo2 := &game.Photo{GameID: g.ID, UserID: author.ID, Path: "uploads/2.jpg"}
	require.NoError(t, photoSvc.Create(photo1))
	require.NoError(t, photoSvc.Create(photo2))

	photos, err := photoSvc.List(context.Background(), g.ID)
	require.NoError(t, err)
	assert.Len(t, photos, 2)
}
