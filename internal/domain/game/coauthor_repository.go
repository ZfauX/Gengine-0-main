// internal/domain/game/coauthor_repository.go
// A-2 (pass 31): репозиторий соавторов — CoAuthorService не обращается к *gorm.DB
// для простых операций. Транзакционные варианты (HasPermissionTx) принимают tx.
package game

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// CoAuthorRepository — контракт для соавторов.
type CoAuthorRepository interface {
	IsUserManager(ctx context.Context, gameID, userID uint) (bool, error)
	GetGameAuthorID(ctx context.Context, gameID uint) (uint, error)
	FindByGameAndUser(ctx context.Context, gameID, userID uint) (*CoAuthor, error)
	FindUnscopedByGameAndUser(ctx context.Context, gameID, userID uint) (*CoAuthor, error)
	Save(ctx context.Context, co *CoAuthor) error
	Create(ctx context.Context, co *CoAuthor) error
	DeleteByGameAndUser(ctx context.Context, gameID, userID uint) error
	ListByGame(ctx context.Context, gameID uint) ([]CoAuthor, error)
}

type gormCoAuthorRepo struct{ db *gorm.DB }

func NewGormCoAuthorRepo(db *gorm.DB) CoAuthorRepository {
	return &gormCoAuthorRepo{db: db}
}

func (r *gormCoAuthorRepo) IsUserManager(ctx context.Context, gameID, userID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Raw(`
			SELECT COUNT(*) FROM (
				SELECT 1 FROM games WHERE id = ? AND author_id = ? AND deleted_at IS NULL
				UNION
				SELECT 1 FROM co_authors WHERE game_id = ? AND user_id = ? AND deleted_at IS NULL
			) sub
		`, gameID, userID, gameID, userID).
		Scan(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *gormCoAuthorRepo) GetGameAuthorID(ctx context.Context, gameID uint) (uint, error) {
	var game Game
	err := r.db.WithContext(ctx).First(&game, gameID).Error
	if err != nil {
		return 0, err
	}
	return game.AuthorID, nil
}

func (r *gormCoAuthorRepo) FindByGameAndUser(ctx context.Context, gameID, userID uint) (*CoAuthor, error) {
	var co CoAuthor
	err := r.db.WithContext(ctx).Where("game_id = ? AND user_id = ?", gameID, userID).First(&co).Error
	if err != nil {
		return nil, err
	}
	return &co, nil
}

func (r *gormCoAuthorRepo) FindUnscopedByGameAndUser(ctx context.Context, gameID, userID uint) (*CoAuthor, error) {
	var co CoAuthor
	err := r.db.WithContext(ctx).Unscoped().Where("game_id = ? AND user_id = ?", gameID, userID).First(&co).Error
	if err != nil {
		return nil, err
	}
	return &co, nil
}

func (r *gormCoAuthorRepo) Save(ctx context.Context, co *CoAuthor) error {
	return r.db.WithContext(ctx).Save(co).Error
}

func (r *gormCoAuthorRepo) Create(ctx context.Context, co *CoAuthor) error {
	return r.db.WithContext(ctx).Create(co).Error
}

func (r *gormCoAuthorRepo) DeleteByGameAndUser(ctx context.Context, gameID, userID uint) error {
	return r.db.WithContext(ctx).Where("game_id = ? AND user_id = ?", gameID, userID).Delete(&CoAuthor{}).Error
}

func (r *gormCoAuthorRepo) ListByGame(ctx context.Context, gameID uint) ([]CoAuthor, error) {
	var coAuthors []CoAuthor
	err := r.db.WithContext(ctx).Preload("User", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, name, avatar_path")
	}).Where("game_id = ?", gameID).Find(&coAuthors).Error
	return coAuthors, err
}

var _ CoAuthorRepository = (*gormCoAuthorRepo)(nil)

// ErrCoAuthorNotFound — соавтор не найден.
var ErrCoAuthorNotFound = errors.New("соавтор не найден")
