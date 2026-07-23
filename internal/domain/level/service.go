// internal/domain/level/service.go
//
//go:generate go run go.uber.org/mock/mockgen -source=service.go -destination=mock_service.go -package=level
package level

import (
	"context"
	"errors"
	"fmt"

	"gengine-0/internal/pkg/middleware"

	"gorm.io/gorm"
)

// ActiveGameManager определяет контракт для операций, влияющих на активную игру.
type ActiveGameManager interface {
	DeleteLevelFromActiveGame(ctx context.Context, gameID, levelID, userID uint) error
}

type LevelService struct {
	levelRepo     LevelRepository
	questionRepo  QuestionRepository
	answerRepo    AnswerRepository
	authorizer    middleware.GameAuthorizer
	activeGameMgr ActiveGameManager
}

func NewLevelService(
	levelRepo LevelRepository,
	questionRepo QuestionRepository,
	answerRepo AnswerRepository,
	authorizer middleware.GameAuthorizer,
	agm ActiveGameManager,
) *LevelService {
	return &LevelService{
		levelRepo:     levelRepo,
		questionRepo:  questionRepo,
		answerRepo:    answerRepo,
		authorizer:    authorizer,
		activeGameMgr: agm,
	}
}

func (s *LevelService) ListByGame(ctx context.Context, gameID uint) ([]Level, error) {
	return s.levelRepo.ListByGameOrdered(ctx, gameID)
}

func (s *LevelService) ListWithQuestions(ctx context.Context, gameID uint) ([]Level, error) {
	return s.levelRepo.ListWithQuestions(ctx, gameID)
}

func (s *LevelService) GetByID(ctx context.Context, levelID uint) (*Level, error) {
	return s.levelRepo.GetByIDWithQuestions(ctx, levelID)
}

func (s *LevelService) Create(ctx context.Context, gameID uint, level *Level, userID uint) error {
	ok, err := s.authorizer.IsUserManager(ctx, gameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может создавать уровни")
	}

	if level.Position == 0 {
		maxPos, maxPosErr := s.levelRepo.GetMaxPosition(ctx, gameID)
		if maxPosErr != nil {
			return maxPosErr
		}
		level.Position = maxPos + 1
	}

	existing, err := s.levelRepo.GetByGameID(ctx, gameID)
	if err != nil {
		return err
	}
	for _, l := range existing {
		if l.Position == level.Position {
			return fmt.Errorf("уровень с позицией %d уже существует в этой игре", level.Position)
		}
	}

	level.GameID = gameID
	return s.levelRepo.Create(ctx, level)
}

func (s *LevelService) Update(ctx context.Context, levelID uint, updated *Level, userID uint) error {
	level, err := s.levelRepo.GetByID(ctx, levelID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, level.GameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может обновлять уровни")
	}

	if updated.Position != 0 && updated.Position != level.Position {
		existing, err := s.levelRepo.GetByGameID(ctx, level.GameID)
		if err != nil {
			return err
		}
		for _, l := range existing {
			if l.Position == updated.Position && l.ID != levelID {
				return fmt.Errorf("уровень с позицией %d уже существует в этой игре", updated.Position)
			}
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
	return s.levelRepo.Update(ctx, level)
}

func (s *LevelService) DeleteFromActiveGame(ctx context.Context, gameID, levelID, userID uint) error {
	return s.activeGameMgr.DeleteLevelFromActiveGame(ctx, gameID, levelID, userID)
}

func (s *LevelService) Duplicate(ctx context.Context, levelID, userID uint) (*Level, error) {
	original, err := s.levelRepo.GetFullLevel(ctx, levelID)
	if err != nil {
		return nil, err
	}
	ok, err := s.authorizer.IsUserManager(ctx, original.GameID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("недостаточно прав")
	}

	tx := s.levelRepo.BeginTransaction(ctx)
	defer tx.Rollback()

	targetPos := original.Position + 1
	if err := tx.Model(&Level{}).Where("game_id = ? AND position >= ?", original.GameID, targetPos).
		Update("position", gorm.Expr("position + 1")).Error; err != nil {
		return nil, err
	}

	newLevel := &Level{
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
	if err := tx.Create(newLevel).Error; err != nil {
		return nil, err
	}

	for _, q := range original.Questions {
		newQ := Question{
			LevelID: newLevel.ID,
			Text:    q.Text,
			Hint:    q.Hint,
		}
		if err := tx.Create(&newQ).Error; err != nil {
			return nil, err
		}
		for _, a := range q.Answers {
			newA := Answer{
				QuestionID: newQ.ID,
				Code:       a.Code,
			}
			if err := tx.Create(&newA).Error; err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return newLevel, nil
}

func (s *LevelService) Move(ctx context.Context, levelID uint, direction string, userID uint) error {
	level, err := s.levelRepo.GetByID(ctx, levelID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, level.GameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("недостаточно прав")
	}

	var sibling *Level
	switch direction {
	case "up":
		sibling, err = s.levelRepo.FindPrevLevel(ctx, level.GameID, level.Position)
		if err != nil {
			return errors.New("некуда двигать")
		}
	case "down":
		sibling, err = s.levelRepo.FindNextLevel(ctx, level.GameID, level.Position)
		if err != nil {
			return errors.New("некуда двигать")
		}
	default:
		return errors.New("неверное направление")
	}

	tx := s.levelRepo.BeginTransaction(ctx)
	if tx.Error != nil {
		return tx.Error
	}

	oldLevelPos := level.Position
	oldSiblingPos := sibling.Position

	maxPos, err := s.levelRepo.GetMaxPositionForTransaction(ctx, tx, level.GameID)
	if err != nil {
		tx.Rollback()
		return err
	}
	tempPos := maxPos + 1

	// Блокируем обе строки в фиксированном порядке (по ID) для предотвращения deadlock'а
	if level.ID < sibling.ID {
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&Level{}, level.ID).Error; err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&Level{}, sibling.ID).Error; err != nil {
			tx.Rollback()
			return err
		}
	} else {
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&Level{}, sibling.ID).Error; err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&Level{}, level.ID).Error; err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Model(level).Update("position", tempPos).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Model(sibling).Update("position", oldLevelPos).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Model(level).Update("position", oldSiblingPos).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// ---------- QuestionService ----------

type QuestionService struct {
	questionRepo QuestionRepository
	levelRepo    LevelRepository
	authorizer   middleware.GameAuthorizer
}

func NewQuestionService(
	questionRepo QuestionRepository,
	levelRepo LevelRepository,
	authorizer middleware.GameAuthorizer,
) *QuestionService {
	return &QuestionService{
		questionRepo: questionRepo,
		levelRepo:    levelRepo,
		authorizer:   authorizer,
	}
}

func (s *QuestionService) ListByLevel(ctx context.Context, levelID uint) ([]Question, error) {
	return s.questionRepo.ListByLevelID(ctx, levelID)
}

func (s *QuestionService) GetByID(ctx context.Context, questionID uint) (*Question, error) {
	return s.questionRepo.GetByIDWithAnswers(ctx, questionID)
}

func (s *QuestionService) Create(ctx context.Context, levelID uint, question *Question, userID uint) error {
	level, err := s.levelRepo.GetByID(ctx, levelID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, level.GameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может создавать вопросы")
	}
	question.LevelID = levelID
	return s.questionRepo.Create(ctx, question)
}

func (s *QuestionService) Update(ctx context.Context, questionID uint, updated *Question, userID uint) error {
	question, err := s.questionRepo.GetByID(ctx, questionID)
	if err != nil {
		return err
	}
	level, err := s.levelRepo.GetByID(ctx, question.LevelID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, level.GameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может обновлять вопросы")
	}
	question.Text = updated.Text
	question.Hint = updated.Hint
	return s.questionRepo.Update(ctx, question)
}

func (s *QuestionService) Delete(ctx context.Context, questionID uint, userID uint) error {
	question, err := s.questionRepo.GetByID(ctx, questionID)
	if err != nil {
		return err
	}
	level, err := s.levelRepo.GetByID(ctx, question.LevelID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, level.GameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("нет прав на удаление вопроса")
	}
	return s.questionRepo.Delete(ctx, questionID)
}

// ---------- AnswerService ----------

type AnswerService struct {
	answerRepo   AnswerRepository
	questionRepo QuestionRepository
	levelRepo    LevelRepository
	authorizer   middleware.GameAuthorizer
}

func NewAnswerService(
	answerRepo AnswerRepository,
	questionRepo QuestionRepository,
	levelRepo LevelRepository,
	authorizer middleware.GameAuthorizer,
) *AnswerService {
	return &AnswerService{
		answerRepo:   answerRepo,
		questionRepo: questionRepo,
		levelRepo:    levelRepo,
		authorizer:   authorizer,
	}
}

func (s *AnswerService) ListByQuestion(ctx context.Context, questionID uint) ([]Answer, error) {
	return s.answerRepo.ListByQuestionID(ctx, questionID)
}

func (s *AnswerService) Create(ctx context.Context, questionID uint, answer *Answer, userID uint) error {
	question, err := s.questionRepo.GetByID(ctx, questionID)
	if err != nil {
		return err
	}
	level, err := s.levelRepo.GetByID(ctx, question.LevelID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, level.GameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("нет прав на создание ответа")
	}
	answer.QuestionID = questionID
	return s.answerRepo.Create(ctx, answer)
}

func (s *AnswerService) Delete(ctx context.Context, answerID uint, userID uint) error {
	// Получаем ответ через репозиторий (добавим метод GetByID)
	answer, err := s.answerRepo.GetByID(ctx, answerID)
	if err != nil {
		return err
	}
	question, err := s.questionRepo.GetByID(ctx, answer.QuestionID)
	if err != nil {
		return err
	}
	level, err := s.levelRepo.GetByID(ctx, question.LevelID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, level.GameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("нет прав на удаление ответа")
	}

	count, err := s.answerRepo.CountByQuestionID(ctx, answer.QuestionID)
	if err != nil {
		return err
	}
	if count <= 1 {
		return errors.New("должен остаться хотя бы один вариант кода")
	}
	return s.answerRepo.Delete(ctx, answerID)
}
