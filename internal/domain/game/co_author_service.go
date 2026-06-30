package game

import (
	"errors"

	"gorm.io/gorm"
)

// Роли соавторов (константы для защиты от опечаток)
const (
	RoleContentEditor = "content_editor"
	RoleModerator     = "moderator"
	RoleObserver      = "observer"
)

type CoAuthorService struct {
	DB *gorm.DB
}

func NewCoAuthorService(db *gorm.DB) *CoAuthorService {
	return &CoAuthorService{DB: db}
}

// IsUserManager проверяет, является ли пользователь автором или соавтором игры.
func (s *CoAuthorService) IsUserManager(gameID, userID uint) (bool, error) {
	var game Game
	if err := s.DB.First(&game, gameID).Error; err != nil {
		return false, err
	}
	if game.AuthorID == userID {
		return true, nil
	}
	var count int64
	if err := s.DB.Model(&CoAuthor{}).Where("game_id = ? AND user_id = ?", gameID, userID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// HasPermission проверяет наличие у пользователя конкретной роли в игре.
// Если пользователь — автор игры, всегда возвращает true.
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
	case RoleContentEditor:
		return co.Role == RoleContentEditor || co.Role == RoleModerator, nil
	case RoleModerator:
		return co.Role == RoleModerator, nil
	case RoleObserver:
		return true, nil
	}
	return false, nil
}

// CanModerateGame — удобный метод для проверки права на модерацию игры.
func (s *CoAuthorService) CanModerateGame(gameID, userID uint) (bool, error) {
	return s.HasPermission(gameID, userID, RoleModerator)
}

// CanEditContent — удобный метод для проверки права на редактирование контента.
func (s *CoAuthorService) CanEditContent(gameID, userID uint) (bool, error) {
	return s.HasPermission(gameID, userID, RoleContentEditor)
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
	co := CoAuthor{GameID: gameID, UserID: newCoAuthorID, Role: RoleContentEditor}
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
