// internal/domain/export/service.go
package export

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/pkg/util"

	"github.com/jung-kurt/gofpdf"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

// ExportService содержит логику экспорта и импорта данных игры.
type ExportService struct {
	exportRepo         ExportRepository
	dejaVuSansFont     []byte
	dejaVuSansBoldFont []byte
}

// NewExportService создаёт новый экземпляр ExportService.
func NewExportService(
	exportRepo ExportRepository,
	normalFont, boldFont []byte,
) (*ExportService, error) {
	if len(normalFont) == 0 || len(boldFont) == 0 {
		return nil, fmt.Errorf("не удалось загрузить один или оба встроенных шрифта DejaVuSans. " +
			"Проверьте, что файлы DejaVuSans.ttf и DejaVuSans-Bold.ttf существуют " +
			"и правильно добавлены в embed.go")
	}
	return &ExportService{
		exportRepo:         exportRepo,
		dejaVuSansFont:     normalFont,
		dejaVuSansBoldFont: boldFont,
	}, nil
}

// ExportGameToCSV записывает все уровни, вопросы и ответы игры в CSV-формате.
func (s *ExportService) ExportGameToCSV(ctx context.Context, gameID uint, w io.Writer) error {
	_, levels, err := s.exportRepo.GetGameWithLevels(ctx, gameID)
	if err != nil {
		return err
	}

	csvWriter := csv.NewWriter(w)
	defer csvWriter.Flush()

	_ = csvWriter.Write([]string{"level_position", "level_name", "question_text", "hint", "answers"})

	for _, lvl := range levels {
		for _, q := range lvl.Questions {
			var answerCodes []string
			for _, a := range q.Answers {
				answerCodes = append(answerCodes, a.Code)
			}
			_ = csvWriter.Write([]string{
				strconv.Itoa(lvl.Position),
				lvl.Name,
				q.Text,
				q.Hint,
				strings.Join(answerCodes, "|"),
			})
		}
	}
	return nil
}

// ExportTeamResultsToCSV экспортирует результаты конкретной команды в CSV.
func (s *ExportService) ExportTeamResultsToCSV(ctx context.Context, gameID, teamID uint, w io.Writer) error {
	db := s.exportRepo.DB(ctx)

	// Получаем passing для команды
	var passing game.GamePassing
	if err := db.Where("game_id = ? AND team_id = ?", gameID, teamID).First(&passing).Error; err != nil {
		return fmt.Errorf("прохождение не найдено: %w", err)
	}

	// Запрашиваем прогресс
	var progress []game.LevelProgress
	if err := db.Where("game_passing_id = ?", passing.ID).
		Order("created_at ASC").
		Find(&progress).Error; err != nil {
		return err
	}

	// Запрашиваем уровни
	var levels []level.Level
	if err := db.Where("game_id = ?", gameID).Order("position ASC").Find(&levels).Error; err != nil {
		return err
	}

	levelMap := make(map[uint]*level.Level)
	for i := range levels {
		levelMap[levels[i].ID] = &levels[i]
	}

	type TeamResult struct {
		LevelName  string
		Status     string
		StartedAt  string
		FinishedAt string
		Attempts   int
		Penalty    int
	}

	var results []TeamResult

	for _, p := range progress {
		lvl := levelMap[p.LevelID]
		if lvl == nil {
			continue
		}

		result := TeamResult{
			LevelName: lvl.Name,
			Status:    "finished",
		}

		if !p.StartedAt.IsZero() {
			result.StartedAt = p.StartedAt.Format("2006-01-02 15:04:05")
		}
		if p.FinishedAt != nil && !p.FinishedAt.IsZero() {
			result.FinishedAt = p.FinishedAt.Format("2006-01-02 15:04:05")
		}

		result.Penalty = int(p.PenaltySeconds)

		// Подсчитываем attempts
		var attempts []game.Attempt
		db.Where("level_progress_id = ?", p.ID).Find(&attempts)
		result.Attempts = len(attempts)

		results = append(results, result)
	}

	// Записываем в CSV
	csvWriter := csv.NewWriter(w)
	defer csvWriter.Flush()

	_ = csvWriter.Write([]string{"Уровень", "Статус", "Начало", "Завершение", "Попытки", "Штраф (сек)"})

	for _, r := range results {
		_ = csvWriter.Write([]string{
			r.LevelName,
			r.Status,
			r.StartedAt,
			r.FinishedAt,
			strconv.Itoa(r.Attempts),
			strconv.Itoa(r.Penalty),
		})
	}

	return nil
}

// ImportGameFromCSV парсит CSV и создаёт уровни/вопросы/ответы для указанной игры.
func (s *ExportService) ImportGameFromCSV(db *gorm.DB, gameID uint, r io.Reader) error {
	return db.Transaction(func(tx *gorm.DB) error {
		reader := csv.NewReader(r)

		if _, err := reader.Read(); err != nil {
			return fmt.Errorf("не удалось прочитать заголовок: %w", err)
		}

		var g game.Game
		if err := tx.First(&g, gameID).Error; err != nil {
			return fmt.Errorf("игра не найдена: %w", err)
		}

		levelMap := make(map[int]*level.Level)

		for {
			record, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("ошибка чтения строки: %w", err)
			}
			if len(record) < 5 {
				continue
			}

			pos, err := strconv.Atoi(record[0])
			if err != nil {
				return fmt.Errorf("неверная позиция уровня: %s", record[0])
			}
			levelName := record[1]
			questionText := record[2]
			hint := record[3]
			answersStr := record[4]

			lvl, exists := levelMap[pos]
			if !exists {
				var existing level.Level
				err := tx.Where("game_id = ? AND position = ?", gameID, pos).First(&existing).Error
				if err == nil {
					lvl = &existing
				} else if errors.Is(err, gorm.ErrRecordNotFound) {
					newLevel := level.Level{
						GameID:   gameID,
						Name:     levelName,
						Position: pos,
					}
					if err := tx.Create(&newLevel).Error; err != nil {
						return fmt.Errorf("не удалось создать уровень: %w", err)
					}
					lvl = &newLevel
				} else {
					return fmt.Errorf("не удалось найти уровень: %w", err)
				}
				levelMap[pos] = lvl
			}

			question := level.Question{
				LevelID: lvl.ID,
				Text:    questionText,
				Hint:    hint,
			}
			if err := tx.Create(&question).Error; err != nil {
				return fmt.Errorf("не удалось создать вопрос: %w", err)
			}

			if answersStr != "" {
				codes := strings.Split(answersStr, "|")
				for _, code := range codes {
					code = strings.TrimSpace(code)
					if code == "" {
						continue
					}
					answer := level.Answer{
						QuestionID: question.ID,
						Code:       code,
					}
					if err := tx.Create(&answer).Error; err != nil {
						return fmt.Errorf("не удалось создать ответ: %w", err)
					}
				}
			}
		}
		return nil
	})
}

// ExportResultsToCSV записывает итоговую таблицу результатов игры в CSV.
func (s *ExportService) ExportResultsToCSV(ctx context.Context, gameID uint, w io.Writer) error {
	passings, err := s.exportRepo.GetFinishedPassingsWithDetails(ctx, gameID)
	if err != nil {
		return err
	}

	csvWriter := csv.NewWriter(w)
	defer csvWriter.Flush()

	_ = csvWriter.Write([]string{"Место", "Команда", "Общее время", "Попыток"})

	for _, p := range passings {
		place := ""
		if p.Place != nil {
			place = strconv.Itoa(*p.Place)
		}
		timeStr := ""
		if p.ResultDuration != nil {
			timeStr = util.FormatDuration(*p.ResultDuration)
		}
		attempts := 0
		for _, lp := range p.Progresses {
			attempts += len(lp.Attempts)
		}
		_ = csvWriter.Write([]string{
			place,
			p.Team.Name,
			timeStr,
			strconv.Itoa(attempts),
		})
	}
	return nil
}

// ExportGameToPDF генерирует PDF-файл со всеми уровнями, вопросами и ответами игры.
func (s *ExportService) ExportGameToPDF(ctx context.Context, gameID uint, w io.Writer) error {
	g, levels, err := s.exportRepo.GetGameWithLevels(ctx, gameID)
	if err != nil {
		return fmt.Errorf("игра не найдена: %w", err)
	}

	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("DejaVu", "", s.dejaVuSansFont)
	pdf.AddUTF8FontFromBytes("DejaVu", "B", s.dejaVuSansBoldFont)
	pdf.AddPage()

	pdf.SetFont("DejaVu", "B", 18)
	pdf.CellFormat(0, 10, fmt.Sprintf("Игра: %s", g.Name), "", 1, "C", false, 0, "")
	pdf.SetFont("DejaVu", "", 12)
	pdf.CellFormat(0, 10, fmt.Sprintf("Автор: %s", g.Author.Name), "", 1, "C", false, 0, "")
	pdf.Ln(5)

	for _, lvl := range levels {
		pdf.SetFont("DejaVu", "B", 14)
		pdf.Cell(0, 10, fmt.Sprintf("Уровень %d: %s", lvl.Position, lvl.Name))
		pdf.Ln(8)

		if lvl.Description != "" {
			pdf.SetFont("DejaVu", "", 11)
			pdf.MultiCell(0, 6, lvl.Description, "", "L", false)
			pdf.Ln(3)
		}

		for _, q := range lvl.Questions {
			pdf.SetFont("DejaVu", "B", 11)
			pdf.Cell(0, 7, fmt.Sprintf("Вопрос: %s", q.Text))
			pdf.Ln(6)

			if q.Hint != "" {
				pdf.SetFont("DejaVu", "", 10)
				pdf.Cell(0, 6, fmt.Sprintf("Подсказка: %s", q.Hint))
				pdf.Ln(5)
			}

			if len(q.Answers) > 0 {
				pdf.SetFont("DejaVu", "", 10)
				codes := make([]string, len(q.Answers))
				for i, a := range q.Answers {
					codes[i] = a.Code
				}
				pdf.Cell(0, 6, fmt.Sprintf("Ответы: %s", strings.Join(codes, ", ")))
				pdf.Ln(6)
			}
		}
		pdf.Ln(3)
	}

	return pdf.Output(w)
}

// ExportStatisticsToPDF генерирует PDF-отчёт с расширенной статистикой игры.
func (s *ExportService) ExportStatisticsToPDF(ctx context.Context, gameID uint, w io.Writer) error {
	g, _, err := s.exportRepo.GetGameWithLevels(ctx, gameID)
	if err != nil {
		return fmt.Errorf("игра не найдена: %w", err)
	}

	passings, err := s.exportRepo.GetFinishedPassingsWithDetails(ctx, gameID)
	if err != nil {
		return err
	}

	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("DejaVu", "", s.dejaVuSansFont)
	pdf.AddUTF8FontFromBytes("DejaVu", "B", s.dejaVuSansBoldFont)
	pdf.AddPage()

	pdf.SetFont("DejaVu", "B", 16)
	pdf.CellFormat(0, 10, fmt.Sprintf("Статистика игры: %s", g.Name), "", 1, "C", false, 0, "")
	pdf.Ln(8)

	for i, p := range passings {
		pdf.SetFont("DejaVu", "B", 13)
		place := fmt.Sprintf("%d", i+1)
		if p.Place != nil {
			place = strconv.Itoa(*p.Place)
		}
		pdf.Cell(0, 8, fmt.Sprintf("%s место – %s", place, p.Team.Name))
		pdf.Ln(7)

		duration := ""
		if p.ResultDuration != nil {
			duration = util.FormatDuration(*p.ResultDuration)
		}
		pdf.SetFont("DejaVu", "", 11)
		pdf.Cell(0, 6, fmt.Sprintf("Общее время: %s", duration))
		pdf.Ln(6)

		for _, lp := range p.Progresses {
			levelTime := ""
			if lp.FinishedAt != nil {
				d := lp.FinishedAt.Sub(lp.StartedAt)
				levelTime = util.FormatDuration(d)
			}
			attempts := len(lp.Attempts)
			pdf.Cell(10, 6, "")
			pdf.Cell(0, 6, fmt.Sprintf("%s – время: %s, попыток: %d", lp.Level.Name, levelTime, attempts))
			pdf.Ln(5)
		}
		pdf.Ln(4)
	}

	return pdf.Output(w)
}

// =============================================================================
// НОВЫЕ МЕТОДЫ ДЛЯ EXCEL
// =============================================================================

// ExportGameToExcel генерирует Excel-файл (.xlsx) со всеми уровнями, вопросами и ответами игры.
func (s *ExportService) ExportGameToExcel(ctx context.Context, gameID uint, w io.Writer) error {
	_, levels, err := s.exportRepo.GetGameWithLevels(ctx, gameID)
	if err != nil {
		return fmt.Errorf("игра не найдена: %w", err)
	}

	f := excelize.NewFile()
	_ = f.DeleteSheet("Sheet1")

	sheetName := "Уровни"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return err
	}

	headers := []string{"Позиция", "Название", "Описание", "Тип", "Вопрос", "Подсказка", "Ответы"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheetName, cell, h)
	}

	style, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	if err == nil {
		endCol, _ := excelize.ColumnNumberToName(len(headers))
		_ = f.SetCellStyle(sheetName, "A1", endCol+"1", style)
	}

	row := 2
	for _, lvl := range levels {
		if len(lvl.Questions) == 0 {
			_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), lvl.Position)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), lvl.Name)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), lvl.Description)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), lvl.Type)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("E%d", row), "")
			_ = f.SetCellValue(sheetName, fmt.Sprintf("F%d", row), "")
			_ = f.SetCellValue(sheetName, fmt.Sprintf("G%d", row), "")
			row++
		} else {
			for _, q := range lvl.Questions {
				var answerCodes []string
				for _, a := range q.Answers {
					answerCodes = append(answerCodes, a.Code)
				}
				answersStr := strings.Join(answerCodes, ", ")
				_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), lvl.Position)
				_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), lvl.Name)
				_ = f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), lvl.Description)
				_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), lvl.Type)
				_ = f.SetCellValue(sheetName, fmt.Sprintf("E%d", row), q.Text)
				_ = f.SetCellValue(sheetName, fmt.Sprintf("F%d", row), q.Hint)
				_ = f.SetCellValue(sheetName, fmt.Sprintf("G%d", row), answersStr)
				row++
			}
		}
	}

	for i := 1; i <= len(headers); i++ {
		col, _ := excelize.ColumnNumberToName(i)
		_ = f.SetColWidth(sheetName, col, col, 25)
	}

	f.SetActiveSheet(index)
	return f.Write(w)
}

// ExportResultsToExcel генерирует Excel-файл с таблицей результатов игры.
func (s *ExportService) ExportResultsToExcel(ctx context.Context, gameID uint, w io.Writer) error {
	passings, err := s.exportRepo.GetFinishedPassingsWithDetails(ctx, gameID)
	if err != nil {
		return err
	}

	f := excelize.NewFile()
	_ = f.DeleteSheet("Sheet1")
	sheetName := "Результаты"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return err
	}

	headers := []string{"Место", "Команда", "Общее время", "Попыток"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheetName, cell, h)
	}

	style, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	if err == nil {
		endCol, _ := excelize.ColumnNumberToName(len(headers))
		_ = f.SetCellStyle(sheetName, "A1", endCol+"1", style)
	}

	row := 2
	for i, p := range passings {
		place := fmt.Sprintf("%d", i+1)
		if p.Place != nil {
			place = fmt.Sprintf("%d", *p.Place)
		}
		timeStr := ""
		if p.ResultDuration != nil {
			timeStr = util.FormatDuration(*p.ResultDuration)
		}
		attempts := 0
		for _, lp := range p.Progresses {
			attempts += len(lp.Attempts)
		}
		_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), place)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), p.Team.Name)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), timeStr)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), attempts)
		row++
	}

	for i := 1; i <= len(headers); i++ {
		col, _ := excelize.ColumnNumberToName(i)
		_ = f.SetColWidth(sheetName, col, col, 20)
	}

	f.SetActiveSheet(index)
	return f.Write(w)
}
