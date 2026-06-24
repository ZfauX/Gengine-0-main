// internal/domain/level/service_test.go
package level_test

import (
	"context"
	"testing"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Все тесты используют единую тестовую базу PostgreSQL,
// которая автоматически мигрирует модели и очищает таблицы перед каждым тестом.

// ---------- LevelService ----------

func TestLevelService_Create(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	svc := newLevelService(db)

	author := createUser(t, db, "author@test.com", "pass")
	g := createGame(t, db, author.ID, "Test Game")

	lvl := &level.Level{Name: "Level 1", Position: 1}
	err := svc.Create(context.Background(), g.ID, lvl, author.ID)
	require.NoError(t, err)
	assert.NotZero(t, lvl.ID)
	assert.Equal(t, g.ID, lvl.GameID)
}

func TestLevelService_Create_NotAuthor(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	svc := newLevelService(db)

	author := createUser(t, db, "author@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createGame(t, db, author.ID, "Game")

	lvl := &level.Level{Name: "L1", Position: 1}
	err := svc.Create(context.Background(), g.ID, lvl, other.ID)
	assert.Error(t, err)
}

func TestLevelService_Update(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	svc := newLevelService(db)

	author := createUser(t, db, "upd@test.com", "pass")
	g := createGame(t, db, author.ID, "Update Game")

	lvl := &level.Level{Name: "Old", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)

	updated := &level.Level{Name: "New", Position: 2}
	err := svc.Update(context.Background(), lvl.ID, updated, author.ID)
	require.NoError(t, err)

	var result level.Level
	db.First(&result, lvl.ID)
	assert.Equal(t, "New", result.Name)
	assert.Equal(t, 2, result.Position)
}

func TestLevelService_Duplicate(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	svc := newLevelService(db)

	author := createUser(t, db, "dup@test.com", "pass")
	g := createGame(t, db, author.ID, "Dup Game")

	original := &level.Level{Name: "Original", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(original).Error)
	q := &level.Question{LevelID: original.ID, Text: "Q"}
	require.NoError(t, db.Create(q).Error)
	a := &level.Answer{QuestionID: q.ID, Code: "code"}
	require.NoError(t, db.Create(a).Error)

	newLvl, err := svc.Duplicate(context.Background(), original.ID, author.ID)
	require.NoError(t, err)
	assert.Contains(t, newLvl.Name, "копия")

	var questions []level.Question
	db.Where("level_id = ?", newLvl.ID).Find(&questions)
	assert.Len(t, questions, 1)
	assert.Equal(t, "Q", questions[0].Text)

	var answers []level.Answer
	db.Where("question_id = ?", questions[0].ID).Find(&answers)
	assert.Len(t, answers, 1)
	assert.Equal(t, "code", answers[0].Code)
}

func TestLevelService_Move(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	svc := newLevelService(db)

	author := createUser(t, db, "move@test.com", "pass")
	g := createGame(t, db, author.ID, "Move Game")

	l1 := &level.Level{Name: "L1", Position: 1, GameID: g.ID}
	l2 := &level.Level{Name: "L2", Position: 2, GameID: g.ID}
	require.NoError(t, db.Create(l1).Error)
	require.NoError(t, db.Create(l2).Error)

	err := svc.Move(context.Background(), l2.ID, "up", author.ID)
	require.NoError(t, err)

	db.First(l1, l1.ID)
	db.First(l2, l2.ID)
	assert.Equal(t, 2, l1.Position)
	assert.Equal(t, 1, l2.Position)
}

func TestLevelService_MoveDown(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	svc := newLevelService(db)

	author := createUser(t, db, "movedown@test.com", "pass")
	g := createGame(t, db, author.ID, "MoveDown Game")

	l1 := &level.Level{Name: "L1", Position: 1, GameID: g.ID}
	l2 := &level.Level{Name: "L2", Position: 2, GameID: g.ID}
	require.NoError(t, db.Create(l1).Error)
	require.NoError(t, db.Create(l2).Error)

	err := svc.Move(context.Background(), l1.ID, "down", author.ID)
	require.NoError(t, err)

	db.First(l1, l1.ID)
	db.First(l2, l2.ID)
	assert.Equal(t, 2, l1.Position)
	assert.Equal(t, 1, l2.Position)
}

func TestLevelService_Duplicate_NotAuthor(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	svc := newLevelService(db)

	author := createUser(t, db, "auth@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createGame(t, db, author.ID, "Dup Game")

	original := &level.Level{Name: "Original", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(original).Error)

	_, err := svc.Duplicate(context.Background(), original.ID, other.ID)
	assert.Error(t, err)
}

func TestLevelService_ListByGame(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	svc := newLevelService(db)

	author := createUser(t, db, "list@test.com", "pass")
	g := createGame(t, db, author.ID, "List Game")

	l1 := &level.Level{Name: "L1", Position: 1, GameID: g.ID}
	l2 := &level.Level{Name: "L2", Position: 2, GameID: g.ID}
	require.NoError(t, db.Create(l1).Error)
	require.NoError(t, db.Create(l2).Error)

	levels, err := svc.ListByGame(context.Background(), g.ID)
	require.NoError(t, err)
	assert.Len(t, levels, 2)
}

func TestLevelService_GetByID(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	svc := newLevelService(db)

	author := createUser(t, db, "getbyid@test.com", "pass")
	g := createGame(t, db, author.ID, "GetByID Game")
	lvl := &level.Level{Name: "Test", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)

	result, err := svc.GetByID(context.Background(), lvl.ID)
	require.NoError(t, err)
	assert.Equal(t, "Test", result.Name)
}

// ---------- QuestionService ----------

func TestQuestionService_Create(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	qSvc := newQuestionService(db)

	author := createUser(t, db, "q@test.com", "pass")
	g := createGame(t, db, author.ID, "Q Game")
	lvl := &level.Level{Name: "L", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)

	q := &level.Question{Text: "What?"}
	err := qSvc.Create(context.Background(), lvl.ID, q, author.ID)
	require.NoError(t, err)
	assert.NotZero(t, q.ID)
	assert.Equal(t, "What?", q.Text)
}

func TestQuestionService_Delete(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	qSvc := newQuestionService(db)

	author := createUser(t, db, "qdel@test.com", "pass")
	g := createGame(t, db, author.ID, "QDel Game")
	lvl := &level.Level{Name: "L", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)

	q := &level.Question{LevelID: lvl.ID, Text: "Delete me"}
	require.NoError(t, db.Create(q).Error)

	err := qSvc.Delete(context.Background(), q.ID, author.ID)
	require.NoError(t, err)

	var deleted level.Question
	err = db.First(&deleted, q.ID).Error
	assert.Error(t, err)
}

func TestQuestionService_Update(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	qSvc := newQuestionService(db)

	author := createUser(t, db, "qup@test.com", "pass")
	g := createGame(t, db, author.ID, "QUpd Game")
	lvl := &level.Level{Name: "L", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)

	q := &level.Question{LevelID: lvl.ID, Text: "Old text"}
	require.NoError(t, db.Create(q).Error)

	updated := &level.Question{Text: "New text"}
	err := qSvc.Update(context.Background(), q.ID, updated, author.ID)
	require.NoError(t, err)

	var result level.Question
	db.First(&result, q.ID)
	assert.Equal(t, "New text", result.Text)
}

func TestQuestionService_ListByLevel(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	qSvc := newQuestionService(db)

	author := createUser(t, db, "qlist@test.com", "pass")
	g := createGame(t, db, author.ID, "QList Game")
	lvl := &level.Level{Name: "L", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)

	q1 := &level.Question{LevelID: lvl.ID, Text: "Q1"}
	q2 := &level.Question{LevelID: lvl.ID, Text: "Q2"}
	require.NoError(t, db.Create(q1).Error)
	require.NoError(t, db.Create(q2).Error)

	questions, err := qSvc.ListByLevel(context.Background(), lvl.ID)
	require.NoError(t, err)
	assert.Len(t, questions, 2)
}

func TestQuestionService_Create_NotAuthor(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	qSvc := newQuestionService(db)

	author := createUser(t, db, "auth@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createGame(t, db, author.ID, "QNotAuth Game")
	lvl := &level.Level{Name: "L", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)

	q := &level.Question{Text: "Q"}
	err := qSvc.Create(context.Background(), lvl.ID, q, other.ID)
	assert.Error(t, err)
}

// ---------- AnswerService ----------

func TestAnswerService_Create(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	aSvc := newAnswerService(db)

	author := createUser(t, db, "ans@test.com", "pass")
	g := createGame(t, db, author.ID, "Ans Game")
	lvl := &level.Level{Name: "L", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)
	q := &level.Question{LevelID: lvl.ID, Text: "Q"}
	require.NoError(t, db.Create(q).Error)

	a := &level.Answer{Code: "123"}
	err := aSvc.Create(context.Background(), q.ID, a, author.ID)
	require.NoError(t, err)
	assert.NotZero(t, a.ID)
	assert.Equal(t, "123", a.Code)
}

func TestAnswerService_DeleteLast(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	aSvc := newAnswerService(db)

	author := createUser(t, db, "last@test.com", "pass")
	g := createGame(t, db, author.ID, "Last Ans Game")
	lvl := &level.Level{Name: "L", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)
	q := &level.Question{LevelID: lvl.ID, Text: "Q"}
	require.NoError(t, db.Create(q).Error)

	a := &level.Answer{QuestionID: q.ID, Code: "only"}
	require.NoError(t, db.Create(a).Error)

	err := aSvc.Delete(context.Background(), a.ID, author.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "хотя бы один вариант")
}

func TestAnswerService_Delete(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	aSvc := newAnswerService(db)

	author := createUser(t, db, "adel@test.com", "pass")
	g := createGame(t, db, author.ID, "Del Ans Game")
	lvl := &level.Level{Name: "L", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)
	q := &level.Question{LevelID: lvl.ID, Text: "Q"}
	require.NoError(t, db.Create(q).Error)

	a1 := &level.Answer{QuestionID: q.ID, Code: "code1"}
	a2 := &level.Answer{QuestionID: q.ID, Code: "code2"}
	require.NoError(t, db.Create(a1).Error)
	require.NoError(t, db.Create(a2).Error)

	err := aSvc.Delete(context.Background(), a1.ID, author.ID)
	require.NoError(t, err)

	var deleted level.Answer
	err = db.First(&deleted, a1.ID).Error
	assert.Error(t, err)
}

func TestAnswerService_ListByQuestion(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	aSvc := newAnswerService(db)

	author := createUser(t, db, "alist@test.com", "pass")
	g := createGame(t, db, author.ID, "AList Game")
	lvl := &level.Level{Name: "L", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)
	q := &level.Question{LevelID: lvl.ID, Text: "Q"}
	require.NoError(t, db.Create(q).Error)

	a1 := &level.Answer{QuestionID: q.ID, Code: "a1"}
	a2 := &level.Answer{QuestionID: q.ID, Code: "a2"}
	require.NoError(t, db.Create(a1).Error)
	require.NoError(t, db.Create(a2).Error)

	answers, err := aSvc.ListByQuestion(context.Background(), q.ID)
	require.NoError(t, err)
	assert.Len(t, answers, 2)
}

func TestAnswerService_Create_NotAuthor(t *testing.T) {
	db := testutil.SetupPostgresDB(t,
		&level.Level{}, &level.Question{}, &level.Answer{},
		&game.Game{}, &game.GameSetting{}, &game.CoAuthor{},
		&user.User{},
	)
	aSvc := newAnswerService(db)

	author := createUser(t, db, "auth@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	g := createGame(t, db, author.ID, "AnsNotAuth Game")
	lvl := &level.Level{Name: "L", Position: 1, GameID: g.ID}
	require.NoError(t, db.Create(lvl).Error)
	q := &level.Question{LevelID: lvl.ID, Text: "Q"}
	require.NoError(t, db.Create(q).Error)

	a := &level.Answer{Code: "456"}
	err := aSvc.Create(context.Background(), q.ID, a, other.ID)
	assert.Error(t, err)
}

// ---------- Вспомогательные функции ----------

// simpleGameAuthorizer — реализация middleware.GameAuthorizer для тестов.
type simpleGameAuthorizer struct {
	db *gorm.DB
}

func (a *simpleGameAuthorizer) IsUserManager(gameID, userID uint) (bool, error) {
	var g game.Game
	if err := a.db.First(&g, gameID).Error; err != nil {
		return false, err
	}
	return g.AuthorID == userID, nil
}

func (a *simpleGameAuthorizer) HasPermission(gameID, userID uint, role string) (bool, error) {
	return a.IsUserManager(gameID, userID)
}

// simpleActiveGameManager — реализация level.ActiveGameManager для тестов.
type simpleActiveGameManager struct{}

func (m *simpleActiveGameManager) DeleteLevelFromActiveGame(ctx context.Context, gameID, levelID, userID uint) error {
	return nil
}

// Конструкторы сервисов с репозиториями.
func newLevelService(db *gorm.DB) *level.LevelService {
	levelRepo := level.NewGormLevelRepo(db)
	questionRepo := level.NewGormQuestionRepo(db)
	answerRepo := level.NewGormAnswerRepo(db)
	authorizer := &simpleGameAuthorizer{db}
	agm := &simpleActiveGameManager{}
	return level.NewLevelService(levelRepo, questionRepo, answerRepo, authorizer, agm)
}

func newQuestionService(db *gorm.DB) *level.QuestionService {
	questionRepo := level.NewGormQuestionRepo(db)
	levelRepo := level.NewGormLevelRepo(db)
	authorizer := &simpleGameAuthorizer{db}
	return level.NewQuestionService(questionRepo, levelRepo, authorizer)
}

func newAnswerService(db *gorm.DB) *level.AnswerService {
	answerRepo := level.NewGormAnswerRepo(db)
	questionRepo := level.NewGormQuestionRepo(db)
	levelRepo := level.NewGormLevelRepo(db)
	authorizer := &simpleGameAuthorizer{db}
	return level.NewAnswerService(answerRepo, questionRepo, levelRepo, authorizer)
}

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
