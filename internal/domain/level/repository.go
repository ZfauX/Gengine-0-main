// internal/domain/level/repository.go
package level

import (
	"context"

	"gorm.io/gorm"
)

type LevelRepository interface {
	Create(ctx context.Context, level *Level) error
	GetByID(ctx context.Context, id uint) (*Level, error)
	GetByIDWithQuestions(ctx context.Context, id uint) (*Level, error)
	GetByGameID(ctx context.Context, gameID uint) ([]Level, error)
	ExistsByPosition(ctx context.Context, gameID uint, position int, excludeID uint) (bool, error)
	Update(ctx context.Context, level *Level) error
	Delete(ctx context.Context, id uint) error
	GetMaxPosition(ctx context.Context, gameID uint) (int, error)
	GetFullLevel(ctx context.Context, id uint) (*Level, error)
	ListByGameOrdered(ctx context.Context, gameID uint) ([]Level, error)
	ListWithQuestions(ctx context.Context, gameID uint) ([]Level, error)
	FindPrevLevel(ctx context.Context, gameID uint, position int) (*Level, error)
	FindNextLevel(ctx context.Context, gameID uint, position int) (*Level, error)
	GetGameName(ctx context.Context, gameID uint) (string, error)
	BeginTransaction(ctx context.Context) *gorm.DB
	ShiftPositions(ctx context.Context, gameID uint, fromPos int, delta int) error
	GetMaxPositionForTransaction(ctx context.Context, tx *gorm.DB, gameID uint) (int, error)
}

type QuestionRepository interface {
	Create(ctx context.Context, question *Question) error
	GetByID(ctx context.Context, id uint) (*Question, error)
	GetByIDWithAnswers(ctx context.Context, id uint) (*Question, error)
	GetQuestionChain(ctx context.Context, questionID uint) (*QuestionChain, error) // JOIN-оптимизация: question→level→game
	Update(ctx context.Context, question *Question) error
	Delete(ctx context.Context, id uint) error
	ListByLevelID(ctx context.Context, levelID uint) ([]Question, error)
}

type AnswerRepository interface {
	Create(ctx context.Context, answer *Answer) error
	GetByID(ctx context.Context, id uint) (*Answer, error)                   // добавлен метод
	GetAnswerChain(ctx context.Context, answerID uint) (*AnswerChain, error) // JOIN-оптимизация: answer→question→level→game
	Delete(ctx context.Context, id uint) error
	ListByQuestionID(ctx context.Context, questionID uint) ([]Answer, error)
	CountByQuestionID(ctx context.Context, questionID uint) (int64, error)
}

type gormLevelRepo struct{ db *gorm.DB }

func NewGormLevelRepo(db *gorm.DB) LevelRepository { return &gormLevelRepo{db} }

func (r *gormLevelRepo) Create(ctx context.Context, level *Level) error {
	return r.db.WithContext(ctx).Create(level).Error
}
func (r *gormLevelRepo) GetByID(ctx context.Context, id uint) (*Level, error) {
	var l Level
	err := r.db.WithContext(ctx).First(&l, id).Error
	if err != nil {
		return nil, err
	}
	return &l, nil
}
func (r *gormLevelRepo) GetByIDWithQuestions(ctx context.Context, id uint) (*Level, error) {
	var l Level
	err := r.db.WithContext(ctx).Preload("Questions.Answers").First(&l, id).Error
	if err != nil {
		return nil, err
	}
	return &l, nil
}
func (r *gormLevelRepo) GetByGameID(ctx context.Context, gameID uint) ([]Level, error) {
	var levels []Level
	err := r.db.WithContext(ctx).Where("game_id = ?", gameID).Order("position ASC").Find(&levels).Error
	return levels, err
}
func (r *gormLevelRepo) Update(ctx context.Context, level *Level) error {
	return r.db.WithContext(ctx).Save(level).Error
}
func (r *gormLevelRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Level{}, id).Error
}
func (r *gormLevelRepo) ExistsByPosition(ctx context.Context, gameID uint, position int, excludeID uint) (bool, error) {
	var count int64
	query := r.db.WithContext(ctx).Model(&Level{}).Where("game_id = ? AND position = ?", gameID, position)
	if excludeID > 0 {
		query = query.Where("id != ?", excludeID)
	}
	err := query.Count(&count).Error
	return count > 0, err
}
func (r *gormLevelRepo) GetMaxPosition(ctx context.Context, gameID uint) (int, error) {
	var maxPos int
	err := r.db.WithContext(ctx).Model(&Level{}).Where("game_id = ?", gameID).Select("COALESCE(MAX(position), 0)").Scan(&maxPos).Error
	return maxPos, err
}
func (r *gormLevelRepo) GetFullLevel(ctx context.Context, id uint) (*Level, error) {
	var l Level
	err := r.db.WithContext(ctx).Preload("Questions.Answers").Preload("MiniGame").First(&l, id).Error
	if err != nil {
		return nil, err
	}
	return &l, nil
}
func (r *gormLevelRepo) ListByGameOrdered(ctx context.Context, gameID uint) ([]Level, error) {
	var levels []Level
	err := r.db.WithContext(ctx).Preload("Questions.Answers").
		Where("game_id = ?", gameID).
		Order("position ASC").
		Find(&levels).Error
	return levels, err
}
func (r *gormLevelRepo) ListWithQuestions(ctx context.Context, gameID uint) ([]Level, error) {
	var levels []Level
	err := r.db.WithContext(ctx).Preload("Questions.Answers").
		Where("game_id = ?", gameID).
		Order("position ASC").
		Find(&levels).Error
	return levels, err
}

func (r *gormLevelRepo) FindPrevLevel(ctx context.Context, gameID uint, position int) (*Level, error) {
	var l Level
	err := r.db.WithContext(ctx).Where("game_id = ? AND position < ?", gameID, position).
		Order("position DESC").First(&l).Error
	if err != nil {
		return nil, err
	}
	return &l, nil
}
func (r *gormLevelRepo) FindNextLevel(ctx context.Context, gameID uint, position int) (*Level, error) {
	var l Level
	err := r.db.WithContext(ctx).Where("game_id = ? AND position > ?", gameID, position).
		Order("position ASC").First(&l).Error
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// GetGameName возвращает название игры (кросс-доменный read для хлебных крошек).
func (r *gormLevelRepo) GetGameName(ctx context.Context, gameID uint) (string, error) {
	var name string
	err := r.db.WithContext(ctx).Table("games").Select("name").Where("id = ?", gameID).Scan(&name).Error
	return name, err
}
func (r *gormLevelRepo) BeginTransaction(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).Begin()
}
func (r *gormLevelRepo) ShiftPositions(ctx context.Context, gameID uint, fromPos int, delta int) error {
	return r.db.WithContext(ctx).Model(&Level{}).
		Where("game_id = ? AND position >= ?", gameID, fromPos).
		Update("position", gorm.Expr("position + ?", delta)).Error
}
func (r *gormLevelRepo) GetMaxPositionForTransaction(ctx context.Context, tx *gorm.DB, gameID uint) (int, error) {
	var maxPos int
	err := tx.Model(&Level{}).
		Where("game_id = ?", gameID).
		Select("COALESCE(MAX(position), 0)").
		Scan(&maxPos).Error
	return maxPos, err
}

// QuestionChain — результат JOIN-запроса для цепочки question→level→game.
type QuestionChain struct {
	QuestionID uint   `gorm:"column:question_id"`
	LevelID    uint   `gorm:"column:level_id"`
	GameID     uint   `gorm:"column:game_id"`
	Text       string `gorm:"column:text"`
	Hint       string `gorm:"column:hint"`
}

type gormQuestionRepo struct{ db *gorm.DB }

func NewGormQuestionRepo(db *gorm.DB) QuestionRepository { return &gormQuestionRepo{db} }

func (r *gormQuestionRepo) GetQuestionChain(ctx context.Context, questionID uint) (*QuestionChain, error) {
	var chain QuestionChain
	err := r.db.WithContext(ctx).
		Table("questions").
		Select("questions.id as question_id, questions.level_id, questions.text, questions.hint, levels.id as level_id, levels.game_id").
		Joins("JOIN levels ON levels.id = questions.level_id AND levels.deleted_at IS NULL").
		Where("questions.id = ? AND questions.deleted_at IS NULL", questionID).
		Scan(&chain).Error
	if err != nil {
		return nil, err
	}
	return &chain, nil
}

func (r *gormQuestionRepo) Create(ctx context.Context, question *Question) error {
	return r.db.WithContext(ctx).Create(question).Error
}
func (r *gormQuestionRepo) GetByID(ctx context.Context, id uint) (*Question, error) {
	var q Question
	err := r.db.WithContext(ctx).First(&q, id).Error
	if err != nil {
		return nil, err
	}
	return &q, nil
}
func (r *gormQuestionRepo) GetByIDWithAnswers(ctx context.Context, id uint) (*Question, error) {
	var q Question
	err := r.db.WithContext(ctx).Preload("Answers").First(&q, id).Error
	if err != nil {
		return nil, err
	}
	return &q, nil
}
func (r *gormQuestionRepo) Update(ctx context.Context, question *Question) error {
	return r.db.WithContext(ctx).Save(question).Error
}
func (r *gormQuestionRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Question{}, id).Error
}
func (r *gormQuestionRepo) ListByLevelID(ctx context.Context, levelID uint) ([]Question, error) {
	var questions []Question
	err := r.db.WithContext(ctx).Where("level_id = ?", levelID).Find(&questions).Error
	return questions, err
}

// AnswerChain — результат JOIN-запроса для цепочки answer→question→level→game.
type AnswerChain struct {
	AnswerID   uint   `gorm:"column:answer_id"`
	QuestionID uint   `gorm:"column:question_id"`
	Code       string `gorm:"column:code"`
	LevelID    uint   `gorm:"column:level_id"`
	GameID     uint   `gorm:"column:game_id"`
}

type gormAnswerRepo struct{ db *gorm.DB }

func NewGormAnswerRepo(db *gorm.DB) AnswerRepository { return &gormAnswerRepo{db} }

func (r *gormAnswerRepo) Create(ctx context.Context, answer *Answer) error {
	return r.db.WithContext(ctx).Create(answer).Error
}
func (r *gormAnswerRepo) GetByID(ctx context.Context, id uint) (*Answer, error) {
	var a Answer
	err := r.db.WithContext(ctx).First(&a, id).Error
	if err != nil {
		return nil, err
	}
	return &a, nil
}
func (r *gormAnswerRepo) GetAnswerChain(ctx context.Context, answerID uint) (*AnswerChain, error) {
	var chain AnswerChain
	err := r.db.WithContext(ctx).
		Table("answers").
		Select("answers.id as answer_id, answers.question_id, answers.code, questions.id as question_id, levels.id as level_id, levels.game_id").
		Joins("JOIN questions ON questions.id = answers.question_id AND questions.deleted_at IS NULL").
		Joins("JOIN levels ON levels.id = questions.level_id AND levels.deleted_at IS NULL").
		Where("answers.id = ? AND answers.deleted_at IS NULL", answerID).
		Scan(&chain).Error
	if err != nil {
		return nil, err
	}
	return &chain, nil
}
func (r *gormAnswerRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Answer{}, id).Error
}
func (r *gormAnswerRepo) ListByQuestionID(ctx context.Context, questionID uint) ([]Answer, error) {
	var answers []Answer
	err := r.db.WithContext(ctx).Where("question_id = ?", questionID).Find(&answers).Error
	return answers, err
}
func (r *gormAnswerRepo) CountByQuestionID(ctx context.Context, questionID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Answer{}).Where("question_id = ?", questionID).Count(&count).Error
	return count, err
}
