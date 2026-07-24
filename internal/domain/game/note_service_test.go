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

func setupNoteTest(t *testing.T) (*gorm.DB, *game.NoteService) {
	t.Helper()
	db := testutil.SetupPostgresDB(t, allModels...)
	coAuthorSvc := game.NewCoAuthorService(db)
	noteSvc := game.NewNoteService(db, coAuthorSvc)
	return db, noteSvc
}

func TestNoteService_Create(t *testing.T) {
	db, noteSvc := setupNoteTest(t)
	author := createUser(t, db, "create_note@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Create Note Game")

	note, err := noteSvc.Create(context.Background(), g.ID, nil, author.ID, "Test note")
	require.NoError(t, err)
	assert.NotZero(t, note.ID)
	assert.Equal(t, "Test note", note.Text)
	assert.Equal(t, g.ID, note.GameID)
	assert.Equal(t, author.ID, note.UserID)

	var count int64
	db.Model(&game.Note{}).Where("id = ?", note.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestNoteService_List(t *testing.T) {
	db, noteSvc := setupNoteTest(t)
	author := createUser(t, db, "list_notes@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "List Note Game")

	_, err := noteSvc.Create(context.Background(), g.ID, nil, author.ID, "Note A")
	require.NoError(t, err)
	_, err = noteSvc.Create(context.Background(), g.ID, nil, author.ID, "Note B")
	require.NoError(t, err)

	notes, err := noteSvc.ListByGame(context.Background(), g.ID, author.ID)
	require.NoError(t, err)
	assert.Len(t, notes, 2)
}

func TestNoteService_CreateAndCheckDB(t *testing.T) {
	db, noteSvc := setupNoteTest(t)
	author := createUser(t, db, "get_note@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Get Note Game")

	created, err := noteSvc.Create(context.Background(), g.ID, nil, author.ID, "Find me")
	require.NoError(t, err)
	assert.NotZero(t, created.ID)

	var count int64
	db.Model(&game.Note{}).Where("id = ?", created.ID).Count(&count)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, "Find me", created.Text)
}
