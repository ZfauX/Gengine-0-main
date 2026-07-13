package game

import (
	"errors"

	"gorm.io/gorm"
)

type PhotoService struct {
	DB *gorm.DB
}

func NewPhotoService(db *gorm.DB) *PhotoService {
	return &PhotoService{DB: db}
}

func (s *PhotoService) List(gameID uint) ([]Photo, error) {
	var photos []Photo
	err := s.DB.Preload("User").Preload("Level").
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
		var coAuthor CoAuthor
		if err := s.DB.Where("game_id = ? AND user_id = ?", photo.GameID, userID).First(&coAuthor).Error; err != nil {
			return errors.New("нет прав на удаление фото")
		}
	}
	return s.DB.Delete(&photo).Error
}
