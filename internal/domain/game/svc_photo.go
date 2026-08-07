package game

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

type PhotoService struct {
	DB *gorm.DB
}

func NewPhotoService(db *gorm.DB) *PhotoService {
	return &PhotoService{DB: db}
}

func (s *PhotoService) List(ctx context.Context, gameID uint) ([]Photo, error) {
	var photos []Photo
	err := s.DB.WithContext(ctx).
		// C-11: не тянем полного пользователя (email и т.п.) в галерею.
		Preload("User", func(db *gorm.DB) *gorm.DB {
			return db.Select("id, name, avatar_path")
		}).
		Preload("Level").
		Where("game_id = ?", gameID).
		Order("created_at DESC").
		Find(&photos).Error
	return photos, err
}

func (s *PhotoService) Create(photo *Photo) error {
	return s.DB.Create(photo).Error
}

func (s *PhotoService) GetByID(photoID uint) (*Photo, error) {
	var photo Photo
	err := s.DB.First(&photo, photoID).Error
	return &photo, err
}

func (s *PhotoService) Delete(photoID, userID uint) error {
	var photo Photo
	if err := s.DB.First(&photo, photoID).Error; err != nil {
		return err
	}
	if photo.UserID != userID {
		// #2: автор игры может удалять чужие фото (в хендлере автор проходит
		// IsUserManager, но в co_authors строки у него нет).
		var game Game
		if err := s.DB.Select("author_id").First(&game, photo.GameID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return err
		}
		if game.AuthorID == userID {
			return s.DB.Delete(&photo).Error
		}

		var coAuthor CoAuthor
		err := s.DB.Where("game_id = ? AND user_id = ?", photo.GameID, userID).First(&coAuthor).Error
		if err != nil {
			// C-4: «нет соавтора» — 403; реальная ошибка БД — 500, а не «нет прав».
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("нет прав на удаление фото")
			}
			return err
		}
		// G5: observer не имеет права удалять чужие фото (defense-in-depth —
		// handler тоже проверяет роль, но сервис защищён и при прямом вызове).
		if coAuthor.Role == RoleObserver {
			return errors.New("нет прав на удаление фото")
		}
	}
	return s.DB.Delete(&photo).Error
}
