// internal/domain/game/photo_repository.go
// A-2 (pass 31): репозиторий фотографий — PhotoService не обращается к *gorm.DB.
package game

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// PhotoRepository — контракт для фотографий.
type PhotoRepository interface {
	List(ctx context.Context, gameID uint) ([]Photo, error)
	GetByID(ctx context.Context, id uint) (*Photo, error)
	Create(ctx context.Context, photo *Photo) error
	Delete(ctx context.Context, photo *Photo) error
	GetGameAuthorID(ctx context.Context, gameID uint) (uint, error)
}

type gormPhotoRepo struct{ db *gorm.DB }

func NewGormPhotoRepo(db *gorm.DB) PhotoRepository {
	return &gormPhotoRepo{db: db}
}

func (r *gormPhotoRepo) List(ctx context.Context, gameID uint) ([]Photo, error) {
	var photos []Photo
	err := r.db.WithContext(ctx).
		Preload("User", func(db *gorm.DB) *gorm.DB {
			return db.Select("id, name, avatar_path")
		}).
		Preload("Level").
		Where("game_id = ?", gameID).
		Order("created_at DESC").
		Find(&photos).Error
	return photos, err
}

func (r *gormPhotoRepo) GetByID(ctx context.Context, id uint) (*Photo, error) {
	var photo Photo
	err := r.db.WithContext(ctx).First(&photo, id).Error
	if err != nil {
		return nil, err
	}
	return &photo, nil
}

func (r *gormPhotoRepo) Create(ctx context.Context, photo *Photo) error {
	return r.db.WithContext(ctx).Create(photo).Error
}

func (r *gormPhotoRepo) Delete(ctx context.Context, photo *Photo) error {
	return r.db.WithContext(ctx).Delete(photo).Error
}

// GetGameAuthorID возвращает author_id игры (для проверки прав на фото).
func (r *gormPhotoRepo) GetGameAuthorID(ctx context.Context, gameID uint) (uint, error) {
	var game Game
	err := r.db.WithContext(ctx).Select("author_id").First(&game, gameID).Error
	if err != nil {
		return 0, err
	}
	return game.AuthorID, nil
}

var _ PhotoRepository = (*gormPhotoRepo)(nil)

// ErrPhotoNotFound — фото не найдено.
var ErrPhotoNotFound = errors.New("фото не найдено")
