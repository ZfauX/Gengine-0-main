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
	coAuthorSvc := game.NewCoAuthorService().WithRepository(game.NewGormCoAuthorRepo(db))
	noteSvc := game.NewNoteService(game.NewGormNoteRepo(db), coAuthorSvc)
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

func TestNoteService_Create_LevelOwnership(t *testing.T) {
	db, noteSvc := setupNoteTest(t)
	author := createUser(t, db, "note_level@test.com", "pass")
	g := createPublishedGameWithSettings(t, db, author.ID, "Note Level Game")
	other := createPublishedGameWithSettings(t, db, author.ID, "Other Game")
	lvl := createLevel(t, db, g.ID, "Level 1", 1)

	t.Run("уровень своей игры разрешён", func(t *testing.T) {
		note, err := noteSvc.Create(context.Background(), g.ID, &lvl.ID, author.ID, "Note with own level")
		require.NoError(t, err)
		require.NotNil(t, note.LevelID)
		assert.Equal(t, lvl.ID, *note.LevelID)
	})

	t.Run("уровень чужой игры отклонён", func(t *testing.T) {
		// DEEP-REVIEW PASS-2 (#19): level_id не принадлежит gameID → ошибка,
		// заметка не создаётся (cross-game reference запрещена).
		_, err := noteSvc.Create(context.Background(), other.ID, &lvl.ID, author.ID, "Cross-game note")
		require.Error(t, err)
		assert.ErrorIs(t, err, game.ErrNoteInvalidLevel)

		var count int64
		db.Model(&game.Note{}).Where("game_id = ? AND text = ?", other.ID, "Cross-game note").Count(&count)
		assert.Equal(t, int64(0), count)
	})
}
