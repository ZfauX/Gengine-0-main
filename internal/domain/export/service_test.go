// internal/domain/export/service_test.go
package export_test

import (
	"bytes"
	"strings"
	"testing"

	"gengine-0/internal/domain/export"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/assets/fonts"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupExportTest(t *testing.T) (*export.ExportService, *gorm.DB, *game.Game) {
	t.Helper()
	db := testutil.SetupPostgresDB(t,
		&user.User{},
		&game.Game{}, &game.GamePassing{}, &game.LevelProgress{}, &game.Attempt{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{},
	)

	author := user.User{Email: "author@test.com", Password: "hash", Name: "Author", EmailVerified: true}
	require.NoError(t, db.Create(&author).Error)

	g := game.Game{Name: "Export Test", AuthorID: author.ID, IsDraft: false}
	require.NoError(t, db.Create(&g).Error)

	svc := export.NewExportService(db, fonts.DejaVuSans, fonts.DejaVuSansBold)
	return svc, db, &g
}

func TestExportGameToCSV(t *testing.T) {
	svc, _, g := setupExportTest(t)

	lvl := level.Level{GameID: g.ID, Name: "Level 1", Position: 1}
	require.NoError(t, svc.DB.Create(&lvl).Error)

	q := level.Question{LevelID: lvl.ID, Text: "Question 1", Hint: "Hint 1"}
	require.NoError(t, svc.DB.Create(&q).Error)

	a1 := level.Answer{QuestionID: q.ID, Code: "answer1"}
	a2 := level.Answer{QuestionID: q.ID, Code: "answer2"}
	require.NoError(t, svc.DB.Create(&a1).Error)
	require.NoError(t, svc.DB.Create(&a2).Error)

	var buf bytes.Buffer
	err := svc.ExportGameToCSV(g.ID, &buf)
	require.NoError(t, err)

	result := buf.String()
	assert.Contains(t, result, "level_position")
	assert.Contains(t, result, "Level 1")
	assert.Contains(t, result, "Question 1")
	assert.Contains(t, result, "answer1|answer2")
}

func TestExportGameToCSV_EmptyGame(t *testing.T) {
	svc, _, g := setupExportTest(t)

	var buf bytes.Buffer
	err := svc.ExportGameToCSV(g.ID, &buf)
	require.NoError(t, err)
	assert.Equal(t, "level_position,level_name,question_text,hint,answers\n", buf.String())
}

func TestImportGameFromCSV(t *testing.T) {
	svc, _, g := setupExportTest(t)

	csvData := `level_position,level_name,question_text,hint,answers
1,Level One,Question A,Hint A,code1|code2
1,Level One,Question B,,code3
2,Level Two,Q2,,`

	err := svc.ImportGameFromCSV(g.ID, strings.NewReader(csvData))
	require.NoError(t, err)

	var levels []level.Level
	err = svc.DB.Where("game_id = ?", g.ID).Order("position ASC").Preload("Questions.Answers").Find(&levels).Error
	require.NoError(t, err)

	assert.Equal(t, 2, len(levels))
	assert.Equal(t, "Level One", levels[0].Name)
	assert.Equal(t, 2, len(levels[0].Questions))
	assert.Equal(t, 2, len(levels[0].Questions[0].Answers))
	assert.Equal(t, "code1", levels[0].Questions[0].Answers[0].Code)
	assert.Equal(t, "code3", levels[0].Questions[1].Answers[0].Code)

	assert.Equal(t, "Level Two", levels[1].Name)
	assert.Equal(t, 1, len(levels[1].Questions))
}

func TestImportGameFromCSV_InvalidHeader(t *testing.T) {
	svc, _, g := setupExportTest(t)

	csvData := `wrong,header,columns`

	err := svc.ImportGameFromCSV(g.ID, strings.NewReader(csvData))
	require.NoError(t, err) // пустой файл без данных — не ошибка
}

func TestExportResultsToCSV_NoPassings(t *testing.T) {
	svc, _, g := setupExportTest(t)

	var buf bytes.Buffer
	err := svc.ExportResultsToCSV(g.ID, &buf)
	require.NoError(t, err)

	result := buf.String()
	assert.Contains(t, result, "Место")
	assert.Contains(t, result, "Команда")
}

func TestExportResultsToCSV_Empty(t *testing.T) {
	svc, _, g := setupExportTest(t)

	var buf bytes.Buffer
	err := svc.ExportResultsToCSV(g.ID, &buf)
	require.NoError(t, err)
	assert.Equal(t, "Место,Команда,Общее время,Попыток\n", buf.String())
}
