// internal/domain/export/service.go
package export

import (
	"context"
	"encoding/csv"
	stderrors "errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/util"

	"github.com/go-pdf/fpdf"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

// ExportService содержит логику экспорта и импорта данных игры.
type ExportService struct {
	exportRepo         ExportRepository
	db                 *gorm.DB
	dejaVuSansFont     []byte
	dejaVuSansBoldFont []byte
}

// NewExportService создаёт новый экземпляр ExportService.
func NewExportService(
	exportRepo ExportRepository,
	db *gorm.DB,
	normalFont, boldFont []byte,
) (*ExportService, error) {
	if len(normalFont) == 0 || len(boldFont) == 0 {
		return nil, fmt.Errorf("не удалось загрузить один или оба встроенных шрифта DejaVuSans. " +
			"Проверьте, что файлы DejaVuSans.ttf и DejaVuSans-Bold.ttf существуют " +
			"и правильно добавлены в embed.go")
	}
	return &ExportService{
		exportRepo:         exportRepo,
		db:                 db,
		dejaVuSansFont:     normalFont,
		dejaVuSansBoldFont: boldFont,
	}, nil
}

// csvSafe нейтрализует CSV/Excel formula injection (S-3, pass 36): значения,
// начинающиеся с =, +, -, @, \t, \r (даже после ведущих пробелов — L-1, pass 37),
// интерпретируются Excel/LibreOffice как формулы. Апостроф-префикс запрещает
// вычисление.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	// L-1 (pass 37): Excel обрабатывает " =2+2" как формулу — смотрим первый
	// не-пробельный символ.
	trimmed := strings.TrimLeft(s, " \t")
	if trimmed == "" {
		return s
	}
	switch trimmed[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// unescapeCSVAnswer (DEEP-REVIEW PASS-4 M1): снимает экранирующий ' добавленный
// csvSafe при экспорте. Без этого backup/restore менял код ответа "=42" на "'=42".
func unescapeCSVAnswer(s string) string {
	if s == "" || s[0] != '\'' {
		return s
	}
	rest := strings.TrimLeft(s[1:], " \t")
	if rest == "" {
		return s
	}
	switch rest[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return s[1:]
	}
	return s
}

// ExportGameToCSV записывает все уровни, вопросы и ответы игры в CSV-формате.
func (s *ExportService) ExportGameToCSV(ctx context.Context, gameID uint, w io.Writer) error {
	_, levels, err := s.exportRepo.GetGameWithLevels(ctx, gameID)
	if err != nil {
		return err
	}

	csvWriter := csv.NewWriter(w)

	if err := csvWriter.Write([]string{"level_position", "level_name", "question_text", "hint", "answers"}); err != nil {
		return fmt.Errorf("ошибка записи CSV-заголовка: %w", err)
	}

	for _, lvl := range levels {
		for _, q := range lvl.Questions {
			var answerCodes []string
			for _, a := range q.Answers {
				answerCodes = append(answerCodes, a.Code)
			}
			if err := csvWriter.Write([]string{
				strconv.Itoa(lvl.Position),
				csvSafe(lvl.Name),
				csvSafe(q.Text),
				csvSafe(q.Hint),
				csvSafe(strings.Join(answerCodes, "|")),
			}); err != nil {
				return fmt.Errorf("ошибка записи CSV-строки: %w", err)
			}
		}
	}
	// DEEP-REVIEW PASS-2 (#17): проверяем ошибку Flush — раньше она глоталась
	// (частичный CSV выглядел как успех).
	csvWriter.Flush()
	return csvWriter.Error()
}

// ExportTeamResultsToCSV экспортирует результаты конкретной команды в CSV.
func (s *ExportService) ExportTeamResultsToCSV(ctx context.Context, gameID, teamID uint, w io.Writer) error {
	// A-3 (pass 35): типизированные read-методы вместо s.exportRepo.DB(ctx).
	passing, err := s.exportRepo.GetPassingByGameAndTeam(ctx, gameID, teamID)
	if err != nil {
		return fmt.Errorf("прохождение не найдено: %w", err)
	}

	progress, err := s.exportRepo.GetProgressesByPassing(ctx, passing.ID)
	if err != nil {
		return err
	}

	levels, err := s.exportRepo.GetLevelsByGame(ctx, gameID)
	if err != nil {
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

	progressIDs := make([]uint, len(progress))
	for i, p := range progress {
		progressIDs[i] = p.ID
	}
	allAttempts, err := s.exportRepo.GetAttemptsByProgressIDs(ctx, progressIDs)
	if err != nil {
		return err
	}
	attemptsMap := make(map[uint]int)
	for _, a := range allAttempts {
		attemptsMap[a.LevelProgressID]++
	}

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

		result.Penalty = p.PenaltySeconds
		result.Attempts = attemptsMap[p.ID]

		results = append(results, result)
	}

	// Записываем в CSV
	csvWriter := csv.NewWriter(w)
	defer csvWriter.Flush()

	if err := csvWriter.Write([]string{"Уровень", "Статус", "Начало", "Завершение", "Попытки", "Штраф (сек)"}); err != nil {
		return fmt.Errorf("ошибка записи CSV-заголовка: %w", err)
	}

	for _, r := range results {
		if err := csvWriter.Write([]string{
			csvSafe(r.LevelName),
			r.Status,
			r.StartedAt,
			r.FinishedAt,
			strconv.Itoa(r.Attempts),
			strconv.Itoa(r.Penalty),
		}); err != nil {
			return fmt.Errorf("ошибка записи CSV-строки: %w", err)
		}
	}

	return nil
}

// ImportGameFromCSV парсит CSV и создаёт уровни/вопросы/ответы для указанной игры.
// DEEP-REVIEW PASS-4 M2: лимит записей (5000) и валидация позиций (1..10000) —
// раньше произвольный CSV мог создать тысячи уровней в одной транзакции
// (долгий lock) и обойти лимиты обычного flow создания.
func (s *ExportService) ImportGameFromCSV(ctx context.Context, gameID uint, r io.Reader) error {
	const (
		maxImportRecords  = 5000
		maxImportPosition = 10000
	)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		reader := csv.NewReader(r)

		if _, err := reader.Read(); err != nil {
			return fmt.Errorf("не удалось прочитать заголовок: %w", err)
		}

		var g game.Game
		if err := tx.First(&g, gameID).Error; err != nil {
			return fmt.Errorf("игра не найдена: %w", err)
		}

		levelMap := make(map[int]*level.Level)

		records := 0
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
			records++
			if records > maxImportRecords {
				return fmt.Errorf("слишком много записей в CSV (максимум %d)", maxImportRecords)
			}

			pos, err := strconv.Atoi(record[0])
			if err != nil {
				return fmt.Errorf("неверная позиция уровня: %s", record[0])
			}
			// M2: валидация диапазона — отрицательные/нулевые/огромные позиции
			// не должны создавать уровни в обход обычного flow.
			if pos < 1 || pos > maxImportPosition {
				return fmt.Errorf("недопустимая позиция уровня: %d", pos)
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
					// E3: re-import уровня — заменяем вопросы/ответы, а не
					// дописываем дубликаты (backup/restore итеративная правка).
					// Unscoped: DB-каскад answers→questions сработает только на
					// физическом удалении, soft-delete оставил бы сироты.
					if delErr := tx.Unscoped().Where("level_id = ?", lvl.ID).Delete(&level.Question{}).Error; delErr != nil {
						return fmt.Errorf("не удалось удалить старые вопросы уровня: %w", delErr)
					}
				} else if stderrors.Is(err, gorm.ErrRecordNotFound) {
					newLevel := level.Level{
						GameID:   gameID,
						Name:     levelName,
						Position: pos,
					}
					if createErr := tx.Create(&newLevel).Error; createErr != nil {
						return fmt.Errorf("не удалось создать уровень: %w", createErr)
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
					// M1 (PASS-4): снять экранирующий ' из csvSafe (экспорт
					// ставит '=42, чтобы Excel не считал формулой). Иначе
					// backup/restore молча меняет код ответа на '=42.
					code = unescapeCSVAnswer(code)
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

	if err := csvWriter.Write([]string{"Место", "Команда", "Общее время", "Попыток"}); err != nil {
		return fmt.Errorf("ошибка записи CSV-заголовка: %w", err)
	}

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
		if err := csvWriter.Write([]string{
			place,
			csvSafe(p.Team.Name),
			timeStr,
			strconv.Itoa(attempts),
		}); err != nil {
			return fmt.Errorf("ошибка записи CSV-строки: %w", err)
		}
	}
	return nil
}

// ExportGameToPDF генерирует PDF-файл со всеми уровнями, вопросами и ответами игры.
func (s *ExportService) ExportGameToPDF(ctx context.Context, gameID uint, w io.Writer) error {
	g, levels, err := s.exportRepo.GetGameWithLevels(ctx, gameID)
	if err != nil {
		return fmt.Errorf("игра не найдена: %w", err)
	}

	pdf := fpdf.New("P", "mm", "A4", "")
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

	pdf := fpdf.New("P", "mm", "A4", "")
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
	if deleteErr := f.DeleteSheet("Sheet1"); deleteErr != nil {
		return fmt.Errorf("ошибка удаления листа: %w", deleteErr)
	}

	sheetName := "Уровни"
	index, newSheetErr := f.NewSheet(sheetName)
	if newSheetErr != nil {
		return newSheetErr
	}

	headers := []string{"Позиция", "Название", "Описание", "Тип", "Вопрос", "Подсказка", "Ответы"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if setErr := f.SetCellValue(sheetName, cell, h); setErr != nil {
			return fmt.Errorf("ошибка записи заголовка Excel: %w", setErr)
		}
	}

	style, styleErr := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	if styleErr == nil {
		endCol, _ := excelize.ColumnNumberToName(len(headers))
		errors.LogSilently(f.SetCellStyle(sheetName, "A1", endCol+"1", style), "Export: failed to set header style")
	}

	row := 2
	for _, lvl := range levels {
		if len(lvl.Questions) == 0 {
			if setErr := f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), lvl.Position); setErr != nil {
				return fmt.Errorf("ошибка записи уровня в Excel: %w", setErr)
			}
			if setErr := f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), csvSafe(lvl.Name)); setErr != nil {
				return fmt.Errorf("ошибка записи уровня в Excel: %w", setErr)
			}
			if setErr := f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), csvSafe(lvl.Description)); setErr != nil {
				return fmt.Errorf("ошибка записи уровня в Excel: %w", setErr)
			}
			if setErr := f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), lvl.Type); setErr != nil {
				return fmt.Errorf("ошибка записи уровня в Excel: %w", setErr)
			}
			row++
		} else {
			for _, q := range lvl.Questions {
				var answerCodes []string
				for _, a := range q.Answers {
					answerCodes = append(answerCodes, a.Code)
				}
				answersStr := strings.Join(answerCodes, ", ")
				if setErr := f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), lvl.Position); setErr != nil {
					return fmt.Errorf("ошибка записи уровня в Excel: %w", setErr)
				}
				if setErr := f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), csvSafe(lvl.Name)); setErr != nil {
					return fmt.Errorf("ошибка записи уровня в Excel: %w", setErr)
				}
				if setErr := f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), csvSafe(lvl.Description)); setErr != nil {
					return fmt.Errorf("ошибка записи уровня в Excel: %w", setErr)
				}
				if setErr := f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), lvl.Type); setErr != nil {
					return fmt.Errorf("ошибка записи уровня в Excel: %w", setErr)
				}
				if setErr := f.SetCellValue(sheetName, fmt.Sprintf("E%d", row), csvSafe(q.Text)); setErr != nil {
					return fmt.Errorf("ошибка записи вопроса в Excel: %w", setErr)
				}
				if setErr := f.SetCellValue(sheetName, fmt.Sprintf("F%d", row), csvSafe(q.Hint)); setErr != nil {
					return fmt.Errorf("ошибка записи подсказки в Excel: %w", setErr)
				}
				if setErr := f.SetCellValue(sheetName, fmt.Sprintf("G%d", row), csvSafe(answersStr)); setErr != nil {
					return fmt.Errorf("ошибка записи ответов в Excel: %w", setErr)
				}
				row++
			}
		}
	}

	for i := 1; i <= len(headers); i++ {
		col, _ := excelize.ColumnNumberToName(i)
		if setErr := f.SetColWidth(sheetName, col, col, 25); setErr != nil {
			return fmt.Errorf("ошибка настройки ширины столбца: %w", setErr)
		}
	}

	f.SetActiveSheet(index)
	return f.Write(w)
}

// ExportResultsToExcel генерирует Excel-файл с таблицей результатов игры.
func (s *ExportService) ExportResultsToExcel(ctx context.Context, gameID uint, w io.Writer) error {
	passings, getErr := s.exportRepo.GetFinishedPassingsWithDetails(ctx, gameID)
	if getErr != nil {
		return getErr
	}

	f := excelize.NewFile()
	if deleteErr := f.DeleteSheet("Sheet1"); deleteErr != nil {
		return fmt.Errorf("ошибка удаления листа: %w", deleteErr)
	}
	sheetName := "Результаты"
	index, newSheetErr := f.NewSheet(sheetName)
	if newSheetErr != nil {
		return newSheetErr
	}

	headers := []string{"Место", "Команда", "Общее время", "Попыток"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if setErr := f.SetCellValue(sheetName, cell, h); setErr != nil {
			return fmt.Errorf("ошибка записи заголовка Excel: %w", setErr)
		}
	}

	style, styleErr := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	if styleErr == nil {
		endCol, _ := excelize.ColumnNumberToName(len(headers))
		errors.LogSilently(f.SetCellStyle(sheetName, "A1", endCol+"1", style), "Export: failed to set header style")
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
		if setErr := f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), place); setErr != nil {
			return fmt.Errorf("ошибка записи места в Excel: %w", setErr)
		}
		if setErr := f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), csvSafe(p.Team.Name)); setErr != nil {
			return fmt.Errorf("ошибка записи команды в Excel: %w", setErr)
		}
		if setErr := f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), timeStr); setErr != nil {
			return fmt.Errorf("ошибка записи времени в Excel: %w", setErr)
		}
		if setErr := f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), attempts); setErr != nil {
			return fmt.Errorf("ошибка записи попыток в Excel: %w", setErr)
		}
		row++
	}

	for i := 1; i <= len(headers); i++ {
		col, _ := excelize.ColumnNumberToName(i)
		if setErr := f.SetColWidth(sheetName, col, col, 20); setErr != nil {
			return fmt.Errorf("ошибка настройки ширины столбца: %w", setErr)
		}
	}

	f.SetActiveSheet(index)
	return f.Write(w)
}
