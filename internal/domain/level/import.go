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

	count := 0
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, il := range payload.Levels {
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
			if lvl.Position == 0 {
				var maxPos int
				if scanErr := tx.Table("levels").Where("game_id = ?", gameID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos).Error; scanErr != nil {
					return scanErr
				}
				lvl.Position = maxPos + 1
			}
			if createErr := tx.Create(lvl).Error; createErr != nil {
				return fmt.Errorf("создание уровня %q: %w", il.Name, createErr)
			}
			for _, iq := range il.Questions {
				q := &Question{LevelID: lvl.ID, Text: iq.Text, Hint: iq.Hint}
				if qErr := tx.Create(q).Error; qErr != nil {
					return fmt.Errorf("создание вопроса уровня %q: %w", il.Name, qErr)
				}
				for _, ia := range iq.Answers {
					if ia.Code == "" {
						continue
					}
					if aErr := tx.Create(&Answer{QuestionID: q.ID, Code: ia.Code}).Error; aErr != nil {
						return fmt.Errorf("создание ответа уровня %q: %w", il.Name, aErr)
					}
				}
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
