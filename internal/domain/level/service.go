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
	"gorm.io/gorm/clause"
)

// ErrPositionTaken возвращается, когда позиция уровня уже занята.
var ErrPositionTaken = errors.New("уровень с такой позицией уже существует")

// Sentinel-ошибки уровня (A-M4, pass 34).
var (
	ErrLevelNotFound        = errors.New("уровень не найден")
	ErrNoPermission         = errors.New("недостаточно прав")
	ErrInvalidMoveDirection = errors.New("неверное направление перемещения")
	ErrCannotDeleteActive   = errors.New("нельзя удалить уровень с активными прохождениями")
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

// GetGameName возвращает название игры (для хлебных крошек и заголовков).
func (s *LevelService) GetGameName(ctx context.Context, gameID uint) (string, error) {
	return s.levelRepo.GetGameName(ctx, gameID)
}

func (s *LevelService) Create(ctx context.Context, gameID uint, level *Level, userID uint) error {
	ok, err := s.authorizer.IsUserManager(ctx, gameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoPermission
	}

	if level.Position == 0 {
		maxPos, maxPosErr := s.levelRepo.GetMaxPosition(ctx, gameID)
		if maxPosErr != nil {
			return maxPosErr
		}
		level.Position = maxPos + 1
	}

	level.GameID = gameID
	exists, err := s.levelRepo.ExistsByPosition(ctx, gameID, level.Position, 0)
	if err != nil {
		return err
	}
	if exists {
		return ErrPositionTaken
	}
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
		exists, err := s.levelRepo.ExistsByPosition(ctx, level.GameID, updated.Position, levelID)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("уровень с позицией %d уже существует в этой игре", updated.Position)
		}
	}

	level.Name = updated.Name
	level.Description = updated.Description
	if updated.Position != 0 {
		// Позиция 0 = «не менять» — сохраняем текущую, чтобы не сбрасывать уровень на 0.
		level.Position = updated.Position
	}
	if updated.Type != "" {
		// G6: пустой Type при частичном POST = «не менять» (как Position).
		// Раньше уровень с ветвлением (parallel_group/blackbox/...) сбрасывался
		// на пустую строку при обновлении только имени/описания.
		level.Type = updated.Type
	}
	// DEEP-REVIEW PASS-3 M5: nil/флаги = «не менять» — не сбрасываем ParentID/
	// GroupID/MinChildren/Lat/Lon при частичном POST (раньше разрушался граф
	// уровней: ветвления/группы/координаты обнулялись при update только имени).
	if updated.ParentID != nil {
		level.ParentID = updated.ParentID
	}
	if updated.GroupID != nil {
		level.GroupID = updated.GroupID
	}
	if updated.MinChildrenSet {
		level.MinChildren = updated.MinChildren
	}
	if updated.LocationSet {
		level.Latitude = updated.Latitude
		level.Longitude = updated.Longitude
	}
	// DEEP-REVIEW LOW #29 (pass 46): RequiresConfirmation применяем только при
	// явном изменении (RequiresConfirmationSet) — раньше частичный POST
	// сбрасывал поле в false.
	if updated.RequiresConfirmationSet {
		level.RequiresConfirmation = updated.RequiresConfirmation
	}
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

	// C-3: сериализуем сдвиг позиций — как в Move, иначе два параллельных
	// Duplicate дают коллизию position (unique constraint отловит при commit).
	if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(original.GameID)).Error; err != nil {
		return nil, err
	}

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

	// PF-8a (pass 29): батч-вставка вопросов одним Create вместо N по одному.
	// Ответы всё ещё требуют questionID — один батч на вопрос.
	newQuestions := make([]Question, 0, len(original.Questions))
	for _, q := range original.Questions {
		newQuestions = append(newQuestions, Question{
			LevelID: newLevel.ID,
			Text:    q.Text,
			Hint:    q.Hint,
		})
	}
	if len(newQuestions) > 0 {
		if err := tx.Create(&newQuestions).Error; err != nil {
			return nil, err
		}
	}
	// M8 (PASS-22): один батч ВСЕХ ответов вместо одного Create на вопрос.
	// Вопросы уже вставлены батчем выше; ID заполнены GORM.
	var allAnswers []Answer
	for i, q := range original.Questions {
		for _, a := range q.Answers {
			allAnswers = append(allAnswers, Answer{
				QuestionID: newQuestions[i].ID,
				Code:       a.Code,
			})
		}
	}
	if len(allAnswers) > 0 {
		if err := tx.Create(&allAnswers).Error; err != nil {
			return nil, err
		}
	}

	// G10: дублируем мини-игру уровня (cipher/puzzle/quiz) — иначе копия
	// уровня теряла бы мини-игру и была нерабочей.
	if original.MiniGame != nil {
		newMini := MiniGame{
			LevelID: newLevel.ID,
			Type:    original.MiniGame.Type,
			Answer:  original.MiniGame.Answer,
			Config:  original.MiniGame.Config,
		}
		if err := tx.Create(&newMini).Error; err != nil {
			return nil, err
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

	tx := s.levelRepo.BeginTransaction(ctx)
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback()

	// Сериализация перемещений уровней одной игры (B7): advisory lock на gameID
	// исключает гонку tempPos между параллельными Move.
	if lockErr := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(level.GameID)).Error; lockErr != nil {
		return lockErr
	}

	// Перечитываем уровень ВНУТРИ транзакции — позиция могла измениться до lock.
	var lockedLevel Level
	if lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedLevel, levelID).Error; lockErr != nil {
		return lockErr
	}

	// Поиск соседа внутри транзакции (после блокировок) — не по устаревшим данным.
	var sibling Level
	switch direction {
	case "up":
		err = tx.Where("game_id = ? AND position < ?", lockedLevel.GameID, lockedLevel.Position).
			Order("position DESC").First(&sibling).Error
	case "down":
		err = tx.Where("game_id = ? AND position > ?", lockedLevel.GameID, lockedLevel.Position).
			Order("position ASC").First(&sibling).Error
	default:
		return ErrInvalidMoveDirection
	}
	if err != nil {
		// «Нет соседа» (нет куда двигать) — пользовательская ошибка; реальные
		// ошибки БД пробрасываем как 500 (C-M4).
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("некуда двигать")
		}
		return err
	}

	// Блокируем соседа.
	if lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&Level{}, sibling.ID).Error; lockErr != nil {
		return lockErr
	}

	// tempPos безопасен: advisory lock сериализует перемещения этой игры.
	maxPos, err := s.levelRepo.GetMaxPositionForTransaction(ctx, tx, lockedLevel.GameID)
	if err != nil {
		return err
	}
	tempPos := maxPos + 1

	oldLevelPos := lockedLevel.Position
	oldSiblingPos := sibling.Position

	if err := tx.Model(&Level{}).Where("id = ?", lockedLevel.ID).Update("position", tempPos).Error; err != nil {
		return err
	}
	if err := tx.Model(&Level{}).Where("id = ?", sibling.ID).Update("position", oldLevelPos).Error; err != nil {
		return err
	}
	if err := tx.Model(&Level{}).Where("id = ?", lockedLevel.ID).Update("position", oldSiblingPos).Error; err != nil {
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
	return s.questionRepo.GetByID(ctx, questionID)
}

// GetByIDWithGameID возвращает вопрос вместе с GameID через JOIN-запрос (оптимизация).
func (s *QuestionService) GetByIDWithGameID(ctx context.Context, questionID uint) (*QuestionChain, error) {
	return s.questionRepo.GetQuestionChain(ctx, questionID)
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
	// JOIN-оптимизация: question + levelID + gameID в 1 SQL-запросе
	chain, err := s.questionRepo.GetQuestionChain(ctx, questionID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, chain.GameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может обновлять вопросы")
	}
	// Only update text and hint — preserve the original question's LevelID
	updated.ID = questionID
	updated.LevelID = chain.LevelID
	return s.questionRepo.Update(ctx, updated)
}

func (s *QuestionService) Delete(ctx context.Context, questionID uint, userID uint) error {
	// JOIN-оптимизация: question + levelID + gameID в 1 SQL-запросе
	chain, err := s.questionRepo.GetQuestionChain(ctx, questionID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, chain.GameID, userID)
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
	// JOIN-оптимизация: question + levelID + gameID в 1 SQL-запросе
	chain, err := s.questionRepo.GetQuestionChain(ctx, questionID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, chain.GameID, userID)
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
	// JOIN-оптимизация: получаем answer + questionID + levelID + gameID в 1 SQL-запросе
	chain, err := s.answerRepo.GetAnswerChain(ctx, answerID)
	if err != nil {
		return err
	}
	ok, err := s.authorizer.IsUserManager(ctx, chain.GameID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("нет прав на удаление ответа")
	}

	count, err := s.answerRepo.CountByQuestionID(ctx, chain.QuestionID)
	if err != nil {
		return err
	}
	if count <= 1 {
		return errors.New("должен остаться хотя бы один вариант кода")
	}
	return s.answerRepo.Delete(ctx, answerID)
}
