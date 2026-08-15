// internal/domain/export/service.go
package export

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/util"

	"github.com/go-pdf/fpdf"
	"github.com/rs/zerolog/log"
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

// EscapeAnswerCodesForTest / UnescapeAnswerCodesForTest — экспортируемые
// обёртки для тестов round-trip (M5, PASS-5).
func EscapeAnswerCodesForTest(codes []string) string { return escapeAnswerCodes(codes) }
func UnescapeAnswerCodesForTest(s string) []string   { return unescapeAnswerCodes(s) }

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

// escapeAnswerCodes (DEEP-REVIEW PASS-5 M5+L2): экранирует разделитель "|",
// backslash и РЕАЛЬНЫЕ апострофы (удвоением) внутри кодов, чтобы round-trip
// не ломал: "a|b" → два ответа; "'=42" (реальный апостроф) не путался с
// экранированием csvSafe. Формат: "\|", "\\", "”".
func escapeAnswerCodes(codes []string) string {
	escaped := make([]string, len(codes))
	for i, c := range codes {
		c = strings.ReplaceAll(c, `\`, `\\`)
		c = strings.ReplaceAll(c, "|", `\|`)
		c = strings.ReplaceAll(c, "'", `''`)
		escaped[i] = c
	}
	return strings.Join(escaped, "|")
}

// unescapeAnswerCodes (M5+L2): обратное преобразование — снимает один
// csvSafe-' (формульный), затем разворачивает "”"→"'", "\|"→"|", "\\"→"\".
func unescapeAnswerCodes(s string) []string {
	if s == "" {
		return nil
	}
	parts := []string{}
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '|':
			cur.WriteByte('|')
			i++
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '\\':
			cur.WriteByte('\\')
			i++
		case s[i] == '|':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	parts = append(parts, cur.String())

	result := make([]string, 0, len(parts))
	for _, p := range parts {
		// L8 (PASS-6): TrimSpace терял значимые хвостовые пробелы в кодах
		// ответов ("=42  " → "=42"). Убираем только ведущие (артефакты
		// csvSafe), хвостовые сохраняем.
		p = strings.TrimLeft(p, " \t")
		if p == "" {
			continue
		}
		// Снимаем ОДИН csvSafe-апостроф (добавлен к формульным символам).
		if strings.HasPrefix(p, "'") && len(p) > 1 {
			rest := strings.TrimLeft(p[1:], " \t")
			if rest != "" {
				switch rest[0] {
				case '=', '+', '-', '@', '\t', '\r':
					p = p[1:]
				}
			}
		}
		// Разворачиваем удвоенные апострофы обратно в реальные.
		p = strings.ReplaceAll(p, "''", "'")
		result = append(result, p)
	}
	return result
}

// ExportGameToCSV записывает все уровни, вопросы и ответы игры в CSV-формате.
func (s *ExportService) ExportGameToCSV(ctx context.Context, gameID uint, w io.Writer) error {
	_, levels, err := s.exportRepo.GetGameWithLevels(ctx, gameID)
	if err != nil {
		return err
	}

	csvWriter := csv.NewWriter(w)

	if err := csvWriter.Write([]string{"level_position", "level_name", "level_type", "level_description", "question_text", "hint", "answers"}); err != nil {
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
				csvSafe(lvl.Type),
				// LOW #12 (PASS-13): раньше Description/Type уровня не экспортировались —
				// CSV-экспорт позиционировался как «резервная копия», но round-trip
				// терял эти поля. Теперь колонки добавлены (импорт читает 7 полей).
				csvSafe(lvl.Description),
				csvSafe(q.Text),
				csvSafe(q.Hint),
				// M5 (PASS-5): экранируем "|" внутри кодов — round-trip не ломает
				// ответы с разделителем; csvSafe защищает от formula-injection.
				csvSafe(escapeAnswerCodes(answerCodes)),
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

	// H2 (PASS-8): проверяем ошибку Flush (единый паттерн с ExportGameToCSV) —
	// раньше defer-флуш глотал ошибку и клиент получал «успех» с неполным файлом.
	csvWriter.Flush()
	return csvWriter.Error()
}

// ensurePDFSpace (L6, PASS-9): если до нижнего поля страницы осталось меньше
// need мм — добавляет новую страницу (длинный MultiCell не обрезается на стыке).
func ensurePDFSpace(pdf *fpdf.Fpdf, need float64) {
	_, pageH := pdf.GetPageSize()
	y := pdf.GetY()
	if pageH-y < need {
		pdf.AddPage()
	}
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

		// MEDIUM #5 (PASS-13): предзагружаем ВСЕ уровни игры одним запросом —
		// раньше tx.Where на каждую новую позицию давал N+1 (до ~10k round-trip
		// на файл в 5000 строк) и держал долгий lock. levelMap заполняется
		// существующими уровнями; новые добавляются в цикле.
		levelMap := make(map[int]*level.Level)
		var existingLevels []level.Level
		if err := tx.Where("game_id = ?", gameID).Find(&existingLevels).Error; err != nil {
			return fmt.Errorf("не удалось загрузить уровни игры: %w", err)
		}
		for i := range existingLevels {
			levelMap[existingLevels[i].Position] = &existingLevels[i]
		}

		records := 0
		// M7 (PASS-17): собираем вопросы и ответы в слайсы, вставляем батчем
		// ПОСЛЕ цикла — раньше tx.Create(&question) на каждую строку (до 5000
		// INSERT в одной транзакции = 5000 round-trip). Ответы привязаны к
		// вопросу по индексу; ID вопроса присвоится GORM при CreateInBatches.
		var pendingQuestions []level.Question
		var pendingAnswers [][]level.Answer
		for {
			record, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("ошибка чтения строки: %w", err)
			}
			// L7 (PASS-5): строка с <5 полями — не молчаливый пропуск (данные
			// терялись незаметно), а явная ошибка.
			// LOW #12 (PASS-13): импорт принимает и старые 5-полевые файлы, и
			// новые 7-полевые (level_type, level_description добавлены).
			if len(record) < 5 {
				return fmt.Errorf("недостаточно полей в строке %d (нужно минимум 5)", records+1)
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
			levelType := ""
			levelDesc := ""
			if len(record) >= 7 {
				levelType = record[2]
				levelDesc = record[3]
			}
			var questionText, hint, answersStr string
			if len(record) >= 7 {
				questionText = record[4]
				hint = record[5]
				answersStr = record[6]
			} else {
				questionText = record[2]
				hint = record[3]
				answersStr = record[4]
			}

			lvl, exists := levelMap[pos]
			if !exists {
				// MEDIUM #5 (PASS-13): уровень не был предзагружен (новый) —
				// создаём. Существующие уже в levelMap из предзагрузки.
				newLevel := level.Level{
					GameID:      gameID,
					Name:        levelName,
					Position:    pos,
					Description: levelDesc,
					Type:        levelType,
				}
				if createErr := tx.Create(&newLevel).Error; createErr != nil {
					return fmt.Errorf("не удалось создать уровень: %w", createErr)
				}
				lvl = &newLevel
				levelMap[pos] = lvl
			} else {
				// E3: re-import уровня — заменяем вопросы/ответы, а не
				// дописываем дубликаты (backup/restore итеративная правка).
				// Unscoped: DB-каскад answers→questions сработает только на
				// физическом удалении, soft-delete оставил бы сироты.
				if delErr := tx.Unscoped().Where("level_id = ?", lvl.ID).Delete(&level.Question{}).Error; delErr != nil {
					return fmt.Errorf("не удалось удалить старые вопросы уровня: %w", delErr)
				}
				// LOW #12 (PASS-13): обновляем type/description при re-import
				// (новые 7-полевые файлы; для 5-полевых значения пустые — не трогаем).
				if len(record) >= 7 {
					if updErr := tx.Model(lvl).Updates(map[string]any{
						"type":        levelType,
						"description": levelDesc,
					}).Error; updErr != nil {
						return fmt.Errorf("не удалось обновить уровень: %w", updErr)
					}
					lvl.Type = levelType
					lvl.Description = levelDesc
				}
			}

			// P2 (PASS-5) / M7 (PASS-17): собираем вопросы и ответы, батч после цикла.
			question := level.Question{
				LevelID: lvl.ID,
				Text:    questionText,
				Hint:    hint,
			}
			var answers []level.Answer
			if answersStr != "" {
				// M5 (PASS-5): разэкранирование "|" и "\\" + снятие csvSafe-' —
				// раньше Split("|") ломал коды с разделителем, а unescapeCSVAnswer
				// портил реальный апостроф "'=42".
				codes := unescapeAnswerCodes(answersStr)
				if len(codes) > 0 {
					answers = make([]level.Answer, 0, len(codes))
					for _, code := range codes {
						answers = append(answers, level.Answer{Code: code})
					}
				}
			}
			// QuestionID заполнится после CreateInBatches; ответы храним по индексу.
			pendingQuestions = append(pendingQuestions, question)
			pendingAnswers = append(pendingAnswers, answers)
		}

		// M7 (PASS-17): батч-вставка всех вопросов (ID присвоится GORM),
		// затем батч ответов с проставленным QuestionID.
		if len(pendingQuestions) > 0 {
			if err := tx.CreateInBatches(pendingQuestions, 200).Error; err != nil {
				return fmt.Errorf("не удалось создать вопросы: %w", err)
			}
			for i := range pendingQuestions {
				ans := pendingAnswers[i]
				for j := range ans {
					ans[j].QuestionID = pendingQuestions[i].ID
				}
				if len(ans) > 0 {
					if err := tx.CreateInBatches(ans, 200).Error; err != nil {
						return fmt.Errorf("не удалось создать ответы: %w", err)
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
	// H2 (PASS-8): проверяем ошибку Flush (единый паттерн с ExportGameToCSV).
	csvWriter.Flush()
	return csvWriter.Error()
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
			// L6 (PASS-9): MultiCell переносит длинные тексты (Cell — обрезает);
			// ensurePDFSpace добавляет страницу, если текст не помещается.
			pdf.MultiCell(0, 7, fmt.Sprintf("Вопрос: %s", q.Text), "", "L", false)
			pdf.Ln(3)

			if q.Hint != "" {
				pdf.SetFont("DejaVu", "", 10)
				ensurePDFSpace(pdf, 15)
				pdf.MultiCell(0, 6, fmt.Sprintf("Подсказка: %s", q.Hint), "", "L", false)
				pdf.Ln(2)
			}

			if len(q.Answers) > 0 {
				pdf.SetFont("DejaVu", "", 10)
				codes := make([]string, len(q.Answers))
				for i, a := range q.Answers {
					codes[i] = a.Code
				}
				ensurePDFSpace(pdf, 12)
				pdf.MultiCell(0, 6, fmt.Sprintf("Ответы: %s", strings.Join(codes, ", ")), "", "L", false)
				pdf.Ln(3)
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
			// L4 (PASS-4): guard на нулевой StartedAt — иначе FinishedAt.Sub(zero)
			// даёт ~17000 лет (0001-01-01) в отчёте.
			if lp.FinishedAt != nil && !lp.StartedAt.IsZero() {
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
	// LOW #10 (PASS-13): закрываем excelize-файл.
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			log.Warn().Err(closeErr).Msg("ExportGameToExcel: excelize close failed")
		}
	}()
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
	// LOW #10 (PASS-13): закрываем excelize-файл.
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			log.Warn().Err(closeErr).Msg("ExportResultsToExcel: excelize close failed")
		}
	}()
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
