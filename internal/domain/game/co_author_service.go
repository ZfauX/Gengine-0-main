package game

import (
	"errors"
	"gorm.io/gorm"
)

type CoAuthorService struct {
	DB *gorm.DB
}

func NewCoAuthorService(db *gorm.DB) *CoAuthorService {
	return &CoAuthorService{DB: db}
}

func (s *CoAuthorService) IsUserManager(gameID, userID uint) (bool, error) {
	var game Game
	if err := s.DB.First(&game, gameID).Error; err != nil {
		return false, err
	}
	if game.AuthorID == userID {
		return true, nil
	}
	var count int64
	s.DB.Model(&CoAuthor{}).Where("game_id = ? AND user_id = ?", gameID, userID).Count(&count)
	return count > 0, nil
}

func (s *CoAuthorService) HasPermission(gameID, userID uint, requiredRole string) (bool, error) {
	var game Game
	if err := s.DB.First(&game, gameID).Error; err != nil {
		return false, err
	}
	if game.AuthorID == userID {
		return true, nil
	}
	var co CoAuthor
	err := s.DB.Where("game_id = ? AND user_id = ?", gameID, userID).First(&co).Error
	if err != nil {
		return false, nil
	}
	switch requiredRole {
	case "content":
		return co.Role == "content_editor" || co.Role == "moderator", nil
	case "moderator":
		return co.Role == "moderator", nil
	case "observer":
		return true, nil
	}
	return false, nil
}

func (s *CoAuthorService) Add(gameID, newCoAuthorID, ownerID uint) error {
	var game Game
	if err := s.DB.First(&game, gameID).Error; err != nil {
		return err
	}
	if game.AuthorID != ownerID {
		return errors.New("только владелец может управлять соавторами")
	}
	if game.AuthorID == newCoAuthorID {
		return errors.New("владелец уже имеет полный доступ")
	}
	var existing CoAuthor
	if err := s.DB.Where("game_id = ? AND user_id = ?", gameID, newCoAuthorID).First(&existing).Error; err == nil {
		return errors.New("этот пользователь уже соавтор")
	}
	co := CoAuthor{GameID: gameID, UserID: newCoAuthorID, Role: "content_editor"}
	return s.DB.Create(&co).Error
}

func (s *CoAuthorService) Remove(gameID, coAuthorUserID, ownerID uint) error {
	var game Game
	if err := s.DB.First(&game, gameID).Error; err != nil {
		return err
	}
	if game.AuthorID != ownerID {
		return errors.New("только владелец может управлять соавторами")
	}
	return s.DB.Where("game_id = ? AND user_id = ?", gameID, coAuthorUserID).Delete(&CoAuthor{}).Error
}

func (s *CoAuthorService) List(gameID uint) ([]CoAuthor, error) {
	var coAuthors []CoAuthor
	err := s.DB.Preload("User").Where("game_id = ?", gameID).Find(&coAuthors).Error
	return coAuthors, err
}