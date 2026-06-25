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
	"gengine-0/internal/pkg/util"

	"github.com/jung-kurt/gofpdf"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// ExportService содержит логику экспорта и импорта данных игры.
type ExportService struct {
	exportRepo         ExportRepository
	dejaVuSansFont     []byte
	dejaVuSansBoldFont []byte
}

// NewExportService создаёт новый экземпляр ExportService.
// normalFont — байты обычного Unicode-шрифта,
// boldFont — байты жирного Unicode-шрифта.
func NewExportService(
	exportRepo ExportRepository,
	normalFont, boldFont []byte,
) *ExportService {
	if len(normalFont) == 0 || len(boldFont) == 0 {
		log.Fatal().Msg("ExportService: не удалось загрузить один или оба встроенных шрифта DejaVuSans. " +
			"Проверьте, что файлы DejaVuSans.ttf и DejaVuSans-Bold.ttf существуют " +
			"и правильно добавлены в embed.go.")
	}
	return &ExportService{
		exportRepo:         exportRepo,
		dejaVuSansFont:     normalFont,
		dejaVuSansBoldFont: boldFont,
	}
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

// ImportGameFromCSV парсит CSV и создаёт уровни/вопросы/ответы для указанной игры.
// Этот метод работает напрямую с БД через транзакцию, поэтому требует *gorm.DB.
// Чтобы сохранить чистоту, мы оставляем прямой доступ к БД через переданный db.
// Альтернативно можно создать метод в репозитории для импорта, но пока оставим так.
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
				} else {
					newLevel := level.Level{
						GameID:   gameID,
						Name:     levelName,
						Position: pos,
					}
					if err := tx.Create(&newLevel).Error; err != nil {
						return fmt.Errorf("не удалось создать уровень: %w", err)
					}
					lvl = &newLevel
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
