// internal/domain/game/repository_sqlmock_gameplay_test.go
// T-1 (pass 37): sqlmock-тесты для read-методов GetGameplayData (A-3, pass 36).
package game

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGormGamePassingRepo_GetByIDWithTeam(t *testing.T) {
	db, mock := newMockGorm(t)
	repo := NewGormGamePassingRepo(db)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "game_passings" WHERE "game_passings"."id" = $1 AND "game_passings"."deleted_at" IS NULL ORDER BY "game_passings"."id" LIMIT $2`)).
		WithArgs(uint(10), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "game_id", "team_id", "status"}).AddRow(10, 1, 2, string(StatusStarted)))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, captain_id FROM "teams" WHERE "teams"."id" = $1 AND "teams"."deleted_at" IS NULL`)).
		WithArgs(uint(2)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "captain_id"}).AddRow(2, "Команда", 5))

	p, err := repo.GetByIDWithTeam(context.Background(), 10)
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, uint(10), p.ID)
	require.Equal(t, "Команда", p.Team.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGormGamePassingRepo_GetCurrentProgressWithLevel(t *testing.T) {
	db, mock := newMockGorm(t)
	repo := NewGormGamePassingRepo(db)

	// Основной запрос прогресса (WHERE finished_at IS NULL).
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "level_progresses" WHERE (game_passing_id = $1 AND finished_at IS NULL) AND "level_progresses"."deleted_at" IS NULL ORDER BY "level_progresses"."id" LIMIT $2`)).
		WithArgs(uint(5), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "game_passing_id", "level_id", "started_at"}).AddRow(77, 5, 3, time.Now()))
	// Preload Level с Select колонок.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, game_id, name, description, type, hint, position FROM "levels" WHERE "levels"."id" = $1 AND "levels"."deleted_at" IS NULL`)).
		WithArgs(uint(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "game_id", "name", "description", "type", "hint", "position"}).AddRow(3, 1, "Уровень", "описание", "code", "подсказка", 1))
	// M11 (PASS-3): Preload Questions (текст/подсказки, БЕЗ Answers.Code).
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, level_id, text, hint FROM "questions" WHERE "questions"."level_id" = $1 AND "questions"."deleted_at" IS NULL`)).
		WithArgs(uint(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "level_id", "text", "hint"}).AddRow(9, 3, "Вопрос 1", "подсказка"))

	progress, err := repo.GetCurrentProgressWithLevel(context.Background(), 5)
	require.NoError(t, err)
	require.NotNil(t, progress)
	require.Equal(t, uint(77), progress.ID)
	require.Equal(t, "Уровень", progress.Level.Name)
	require.Len(t, progress.Level.Questions, 1, "вопросы уровня должны загружаться (M11)")
	require.Equal(t, "Вопрос 1", progress.Level.Questions[0].Text)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGormGamePassingRepo_GetAttemptsByProgress(t *testing.T) {
	db, mock := newMockGorm(t)
	repo := NewGormGamePassingRepo(db)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, created_at, code, success FROM "attempts" WHERE level_progress_id = $1 AND "attempts"."deleted_at" IS NULL ORDER BY created_at DESC LIMIT $2`)).
		WithArgs(uint(77), 50).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "code", "success"}).
			AddRow(1, time.Now(), "a", true).
			AddRow(2, time.Now(), "b", false))

	attempts, err := repo.GetAttemptsByProgress(context.Background(), 77, 50)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	require.True(t, attempts[0].Success)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGormGamePassingRepo_GetOpenVotingSession(t *testing.T) {
	db, mock := newMockGorm(t)
	repo := NewGormGamePassingRepo(db)

	// Открытая сессия есть.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "blackbox_voting_sessions" WHERE (game_passing_id = $1 AND level_id = $2 AND is_open = true) AND "blackbox_voting_sessions"."deleted_at" IS NULL ORDER BY "blackbox_voting_sessions"."id" LIMIT $3`)).
		WithArgs(uint(5), uint(3), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "game_passing_id", "level_id", "is_open"}).AddRow(9, 5, 3, true))

	sess, open, err := repo.GetOpenVotingSession(context.Background(), 5, 3)
	require.NoError(t, err)
	require.True(t, open)
	require.NotNil(t, sess)
	require.Equal(t, uint(9), sess.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGormGamePassingRepo_GetOpenVotingSession_None(t *testing.T) {
	db, mock := newMockGorm(t)
	repo := NewGormGamePassingRepo(db)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "blackbox_voting_sessions" WHERE (game_passing_id = $1 AND level_id = $2 AND is_open = true) AND "blackbox_voting_sessions"."deleted_at" IS NULL ORDER BY "blackbox_voting_sessions"."id" LIMIT $3`)).
		WithArgs(uint(5), uint(3), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	sess, open, err := repo.GetOpenVotingSession(context.Background(), 5, 3)
	require.NoError(t, err)
	require.False(t, open)
	require.Nil(t, sess)
	require.NoError(t, mock.ExpectationsWereMet())
}
