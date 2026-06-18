// internal/domain/level/service.go
package level

import (
	"errors"
	"fmt"

	"gengine-0/internal/pkg/middleware"

	"gorm.io/gorm"
)

// ActiveGameManager – интерфейс для операций, затрагивающих активную игру.
type ActiveGameManager interface {
	DeleteLevelFromActiveGame(gameID, levelID, userID uint) error
}

type LevelService struct {
	DB                *gorm.DB
	authorizer        middleware.GameAuthorizer
	activeGameManager ActiveGameManager
}

func NewLevelService(
	db *gorm.DB,
	authorizer middleware.GameAuthorizer,
	agm ActiveGameManager,
) *LevelService {
	return &LevelService{
		DB:                db,
		authorizer:        authorizer,
		activeGameManager: agm,
	}
}

// ListByGame возвращает все уровни игры, отсортированные по позиции.
func (s *LevelService) ListByGame(gameID uint) ([]Level, error) {
	var levels []Level
	err := s.DB.Where("game_id = ?", gameID).Order("position ASC").Find(&levels).Error
	return levels, err
}

// GetByID возвращает уровень с вопросами и ответами.
func (s *LevelService) GetByID(levelID uint) (*Level, error) {
	var level Level
	err := s.DB.Preload("Questions.Answers").First(&level, levelID).Error
	return &level, err
}

// Create создаёт новый уровень.
func (s *LevelService) Create(gameID uint, level *Level, userID uint) error {
	ok, err := s.authorizer.IsUserManager(gameID, userID)
	if err != nil {
		return errors.New("ошибка проверки прав")
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может создавать уровни")
	}

	if level.Position == 0 {
		var maxPos int
		s.DB.Model(&Level{}).Where("game_id = ?", gameID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos)
		level.Position = maxPos + 1
	}

	var count int64
	if err := s.DB.Model(&Level{}).Where("game_id = ? AND position = ?", gameID, level.Position).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("уровень с позицией %d уже существует в этой игре", level.Position)
	}

	level.GameID = gameID
	return s.DB.Create(level).Error
}

// Update обновляет уровень.
func (s *LevelService) Update(levelID uint, updated *Level, userID uint) error {
	var level Level
	if err := s.DB.First(&level, levelID).Error; err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		return errors.New("ошибка проверки прав")
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может обновлять уровни")
	}

	if updated.Position != 0 && updated.Position != level.Position {
		var count int64
		if err := s.DB.Model(&Level{}).Where("game_id = ? AND position = ? AND id != ?", level.GameID, updated.Position, levelID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("уровень с позицией %d уже существует в этой игре", updated.Position)
		}
	}

	level.Name = updated.Name
	level.Description = updated.Description
	level.Position = updated.Position
	level.Type = updated.Type
	level.ParentID = updated.ParentID
	level.GroupID = updated.GroupID
	level.MinChildren = updated.MinChildren
	level.Latitude = updated.Latitude
	level.Longitude = updated.Longitude
	level.RequiresConfirmation = updated.RequiresConfirmation
	return s.DB.Save(&level).Error
}

// DeleteFromActiveGame удаляет уровень, делегируя реализацию в game.
func (s *LevelService) DeleteFromActiveGame(gameID, levelID, userID uint) error {
	return s.activeGameManager.DeleteLevelFromActiveGame(gameID, levelID, userID)
}

// Duplicate создаёт копию уровня.
func (s *LevelService) Duplicate(levelID, userID uint) (*Level, error) {
	var original Level
	if err := s.DB.Preload("Questions.Answers").First(&original, levelID).Error; err != nil {
		return nil, err
	}
	ok, err := s.authorizer.IsUserManager(original.GameID, userID)
	if err != nil {
		return nil, errors.New("ошибка проверки прав")
	}
	if !ok {
		return nil, errors.New("недостаточно прав")
	}

	targetPos := original.Position + 1
	if err := s.DB.Model(&Level{}).Where("game_id = ? AND position >= ?", original.GameID, targetPos).
		Update("position", gorm.Expr("position + 1")).Error; err != nil {
		return nil, err
	}

	newLevel := Level{
		GameID:               original.GameID,
		Name:                 original.Name + " (копия)",
		Description:          original.Description,
		Position:             targetPos,
		Type:                 original.Type,
		ParentID:             original.ParentID,
		GroupID:              original.GroupID,
		MinChildren:          original.MinChildren,
		RequiresConfirmation: original.RequiresConfirmation,
		Latitude:             original.Latitude,
		Longitude:            original.Longitude,
	}
	if err := s.DB.Create(&newLevel).Error; err != nil {
		return nil, err
	}

	for _, q := range original.Questions {
		newQ := Question{
			LevelID: newLevel.ID,
			Text:    q.Text,
			Hint:    q.Hint,
		}
		if err := s.DB.Create(&newQ).Error; err != nil {
			return nil, err
		}
		for _, a := range q.Answers {
			newA := Answer{
				QuestionID: newQ.ID,
				Code:       a.Code,
			}
			if err := s.DB.Create(&newA).Error; err != nil {
				return nil, err
			}
		}
	}
	return &newLevel, nil
}

// Move перемещает уровень вверх или вниз (атомарный обмен с временной позицией maxPos+1).
func (s *LevelService) Move(levelID uint, direction string, userID uint) error {
	var level Level
	if err := s.DB.First(&level, levelID).Error; err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		return errors.New("ошибка проверки прав")
	}
	if !ok {
		return errors.New("недостаточно прав")
	}

	var sibling Level
	switch direction {
	case "up":
		err := s.DB.Where("game_id = ? AND position < ?", level.GameID, level.Position).
			Order("position DESC").First(&sibling).Error
		if err != nil {
			return errors.New("некуда двигать")
		}
	case "down":
		err := s.DB.Where("game_id = ? AND position > ?", level.GameID, level.Position).
			Order("position ASC").First(&sibling).Error
		if err != nil {
			return errors.New("некуда двигать")
		}
	default:
		return errors.New("неверное направление")
	}

	tx := s.DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	// Вычисляем максимальную позицию в игре внутри транзакции
	var maxPos int
	if err := tx.Model(&Level{}).
		Where("game_id = ?", level.GameID).
		Select("COALESCE(MAX(position), 0)").
		Scan(&maxPos).Error; err != nil {
		tx.Rollback()
		return err
	}
	tempPos := maxPos + 1 // гарантированно свободная положительная позиция

	// 1) level → tempPos (освобождаем его место)
	if err := tx.Model(&level).Update("position", tempPos).Error; err != nil {
		tx.Rollback()
		return err
	}
	// 2) sibling → старая позиция level
	if err := tx.Model(&sibling).Update("position", level.Position).Error; err != nil {
		tx.Rollback()
		return err
	}
	// 3) level → старая позиция sibling
	if err := tx.Model(&level).Update("position", sibling.Position).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// ---------- QuestionService ----------

type QuestionService struct {
	DB         *gorm.DB
	authorizer middleware.GameAuthorizer
}

func NewQuestionService(db *gorm.DB, authorizer middleware.GameAuthorizer) *QuestionService {
	return &QuestionService{DB: db, authorizer: authorizer}
}

func (s *QuestionService) ListByLevel(levelID uint) ([]Question, error) {
	var questions []Question
	err := s.DB.Where("level_id = ?", levelID).Find(&questions).Error
	return questions, err
}

func (s *QuestionService) GetByID(questionID uint) (*Question, error) {
	var question Question
	err := s.DB.Preload("Answers").First(&question, questionID).Error
	return &question, err
}

func (s *QuestionService) Create(levelID uint, question *Question, userID uint) error {
	var level Level
	if err := s.DB.First(&level, levelID).Error; err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		return errors.New("ошибка проверки прав")
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может создавать вопросы")
	}
	question.LevelID = levelID
	return s.DB.Create(question).Error
}

func (s *QuestionService) Update(questionID uint, updated *Question, userID uint) error {
	var question Question
	if err := s.DB.First(&question, questionID).Error; err != nil {
		return err
	}
	var level Level
	if err := s.DB.First(&level, question.LevelID).Error; err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		return errors.New("ошибка проверки прав")
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может обновлять вопросы")
	}
	question.Text = updated.Text
	question.Hint = updated.Hint
	return s.DB.Save(&question).Error
}

func (s *QuestionService) Delete(questionID uint, userID uint) error {
	var question Question
	if err := s.DB.First(&question, questionID).Error; err != nil {
		return err
	}
	var level Level
	if err := s.DB.First(&level, question.LevelID).Error; err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		return errors.New("ошибка проверки прав")
	}
	if !ok {
		return errors.New("нет прав на удаление вопроса")
	}
	return s.DB.Delete(&question).Error
}

// ---------- AnswerService ----------

type AnswerService struct {
	DB         *gorm.DB
	authorizer middleware.GameAuthorizer
}

func NewAnswerService(db *gorm.DB, authorizer middleware.GameAuthorizer) *AnswerService {
	return &AnswerService{DB: db, authorizer: authorizer}
}

func (s *AnswerService) ListByQuestion(questionID uint) ([]Answer, error) {
	var answers []Answer
	err := s.DB.Where("question_id = ?", questionID).Find(&answers).Error
	return answers, err
}

func (s *AnswerService) Create(questionID uint, answer *Answer, userID uint) error {
	var question Question
	if err := s.DB.First(&question, questionID).Error; err != nil {
		return err
	}
	var level Level
	if err := s.DB.First(&level, question.LevelID).Error; err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		return errors.New("ошибка проверки прав")
	}
	if !ok {
		return errors.New("нет прав на создание ответа")
	}
	answer.QuestionID = questionID
	return s.DB.Create(answer).Error
}

func (s *AnswerService) Delete(answerID uint, userID uint) error {
	var answer Answer
	if err := s.DB.First(&answer, answerID).Error; err != nil {
		return err
	}
	var question Question
	if err := s.DB.First(&question, answer.QuestionID).Error; err != nil {
		return err
	}
	var level Level
	if err := s.DB.First(&level, question.LevelID).Error; err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(level.GameID, userID)
	if err != nil {
		return errors.New("ошибка проверки прав")
	}
	if !ok {
		return errors.New("нет прав на удаление ответа")
	}

	var count int64
	s.DB.Model(&Answer{}).Where("question_id = ?", answer.QuestionID).Count(&count)
	if count <= 1 {
		return errors.New("должен остаться хотя бы один вариант кода")
	}
	return s.DB.Delete(&answer).Error
}