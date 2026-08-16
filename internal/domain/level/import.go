// internal/domain/level/import.go
// F-1 (pass 45): импорт игры с уровнями, вопросами, ответами и подсказками.
package level

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"gorm.io/gorm"
)

// ImportLevel — уровень в формате импорта.
type ImportLevel struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Position    int              `json:"position"`
	Type        string           `json:"type"`
	Latitude    float64          `json:"latitude"`
	Longitude   float64          `json:"longitude"`
	Questions   []ImportQuestion `json:"questions"`
}

// ImportQuestion — вопрос уровня в формате импорта.
type ImportQuestion struct {
	Text    string         `json:"text"`
	Hint    string         `json:"hint"`
	Answers []ImportAnswer `json:"answers"`
}

// ImportAnswer — правильный ответ (код) в формате импорта.
type ImportAnswer struct {
	Code string `json:"code"`
}

// ImportGame — полная игра в формате импорта.
type ImportGame struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Levels      []ImportLevel `json:"levels"`
}

// Лимиты импорта (DEEP-REVIEW PASS-5 M6) — несимметричность с CSV-импортом:
// раньше 5MB JSON мог создать тысячи строк в одной транзакции.
const (
	maxImportLevels             = 5000
	maxImportQuestionsPerLevel  = 200
	maxImportAnswersPerQuestion = 100
	maxImportPosition           = 10000 // M9: верхняя граница позиции (как CSV)
)

// validLevelType — allowlist типов уровня (M6).
func validLevelType(t string) bool {
	switch t {
	case "single", "checkpoint", "parallel_group", "blackbox", "file_upload":
		return true
	}
	return false
}

// GameAuthorizer — минимальный контракт проверки прав (автор/контент-менеджер).
type GameAuthorizer interface {
	IsUserManager(ctx context.Context, gameID, userID uint) (bool, error)
}

// ImportService импортирует игры из JSON (F-1, pass 45).
type ImportService struct {
	db         *gorm.DB
	authorizer GameAuthorizer
}

func NewImportService(db *gorm.DB, authorizer GameAuthorizer) *ImportService {
	return &ImportService{db: db, authorizer: authorizer}
}

// Import создаёт игру и её уровни/вопросы/ответы в одной транзакции.
// Игра должна существовать (передаётся gameID) — импортируются только уровни.
// DEEP-REVIEW PASS-5 M6: лимиты и валидация — раньше 5MB JSON создавал
// тысячи строк в одной транзакции (долгий lock), Position мог быть
// отрицательным/нулевым, Type — произвольной строкой (несимметрично CSV).
func (s *ImportService) Import(ctx context.Context, gameID, userID uint, r io.Reader) (int, error) {
	ok, err := s.authorizer.IsUserManager(ctx, gameID, userID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, errors.New("только автор или контент-менеджер может импортировать уровни")
	}

	var payload ImportGame
	if decodeErr := json.NewDecoder(io.LimitReader(r, 5*1024*1024)).Decode(&payload); decodeErr != nil {
		return 0, fmt.Errorf("неверный формат JSON: %w", decodeErr)
	}
	if len(payload.Levels) == 0 {
		return 0, errors.New("нет уровней для импорта")
	}
	// M6: лимит уровней (как CSV — 5000).
	if len(payload.Levels) > maxImportLevels {
		return 0, fmt.Errorf("слишком много уровней в JSON (максимум %d)", maxImportLevels)
	}

	count := 0
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// M2 (PASS-6): advisory lock на игру — автопозиция MAX+1 не должна
		// гоняться между параллельными импортами (иначе дубликаты позиций).
		if lockErr := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(gameID)).Error; lockErr != nil {
			return fmt.Errorf("pg_advisory_xact_lock: %w", lockErr)
		}

		// H3 (PASS-20): собираем уровни в слайс и вставляем батчем вместо
		// построчного tx.Create — иначе 5000 уровней × 200 вопросов × 100
		// ответов дают до ~1M отдельных INSERT при удержании advisory-lock.
		// Валидация выполняется ДО вставки (раньше — после создания уровня).
		levels := make([]*Level, 0, len(payload.Levels))
		seenPos := make(map[int]bool, len(payload.Levels))
		autoCount := 0
		maxExplicit := 0
		for _, il := range payload.Levels {
			// M6: валидация позиции — отрицательные запрещены; 0 = автопозиция
			// (max+1, как раньше); дубликаты позиций в рамках импорта запрещены.
			if il.Position < 0 {
				return fmt.Errorf("недопустимая позиция уровня: %d", il.Position)
			}
			// M9 (PASS-6): верхняя граница позиции — асимметрия с CSV-импортом,
			// где pos ≤ 10000. Позиция 2^31-1 ломала бы сортировку/пагинацию.
			if il.Position > maxImportPosition {
				return fmt.Errorf("недопустимая позиция уровня: %d (максимум %d)", il.Position, maxImportPosition)
			}
			if il.Position > 0 {
				if seenPos[il.Position] {
					return fmt.Errorf("дубликат позиции уровня: %d", il.Position)
				}
				seenPos[il.Position] = true
				if il.Position > maxExplicit {
					maxExplicit = il.Position
				}
			} else {
				autoCount++
			}

			// M6: allowlist типа уровня.
			if il.Type != "" && !validLevelType(il.Type) {
				return fmt.Errorf("недопустимый тип уровня: %q", il.Type)
			}

			// M6: лимиты вопросов/ответов — валидируем ДО вставки.
			if len(il.Questions) > maxImportQuestionsPerLevel {
				return fmt.Errorf("слишком много вопросов уровня %q (максимум %d)", il.Name, maxImportQuestionsPerLevel)
			}
			for _, iq := range il.Questions {
				if len(iq.Answers) > maxImportAnswersPerQuestion {
					return fmt.Errorf("слишком много ответов вопроса уровня %q (максимум %d)", il.Name, maxImportAnswersPerQuestion)
				}
			}

			lvl := &Level{
				Name:        il.Name,
				Description: il.Description,
				Position:    il.Position,
				Type:        il.Type,
				Latitude:    il.Latitude,
				Longitude:   il.Longitude,
				GameID:      gameID,
			}
			if lvl.Type == "" {
				lvl.Type = "single"
			}
			levels = append(levels, lvl)
		}

		// Автопозиция: один запрос MAX + локальный инкремент. Учитываем как
		// существующие в БД, так и явные позиции из payload (H3).
		if autoCount > 0 {
			var maxPos int
			if scanErr := tx.Table("levels").Where("game_id = ?", gameID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos).Error; scanErr != nil {
				return scanErr
			}
			base := maxPos
			if maxExplicit > base {
				base = maxExplicit
			}
			// L5 (PASS-7): автопозиция не должна превышать верхнюю границу.
			if base+autoCount > maxImportPosition {
				return fmt.Errorf("нельзя автоматически назначить позицию: в игре уже %d уровней", base)
			}
			next := base + 1
			for _, lvl := range levels {
				if lvl.Position == 0 {
					lvl.Position = next
					next++
				}
			}
		}

		// Вставка уровней батчем.
		if err = tx.CreateInBatches(levels, 200).Error; err != nil {
			return fmt.Errorf("создание уровней: %w", err)
		}

		// Сбор вопросов (с LevelID) и вставка батчем.
		var questions []*Question
		for i, lvl := range levels {
			for _, iq := range payload.Levels[i].Questions {
				questions = append(questions, &Question{LevelID: lvl.ID, Text: iq.Text, Hint: iq.Hint})
			}
		}
		if len(questions) > 0 {
			if err = tx.CreateInBatches(questions, 200).Error; err != nil {
				return fmt.Errorf("создание вопросов: %w", err)
			}
		}

		// Сбор ответов (с QuestionID) и вставка батчем.
		var answers []*Answer
		qi := 0
		for i := range levels {
			for _, iq := range payload.Levels[i].Questions {
				q := questions[qi]
				qi++
				for _, ia := range iq.Answers {
					if ia.Code == "" {
						continue
					}
					answers = append(answers, &Answer{QuestionID: q.ID, Code: ia.Code})
				}
			}
		}
		if len(answers) > 0 {
			if err = tx.CreateInBatches(answers, 200).Error; err != nil {
				return fmt.Errorf("создание ответов: %w", err)
			}
		}

		count = len(levels)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
