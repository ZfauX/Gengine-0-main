// internal/domain/game/co_author_service.go
package game

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// Роли соавторов
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
func (s *CoAuthorService) HasPermission(gameID, userID uint, requiredRole string) (bool, error) {
	return s.HasPermissionTx(s.DB, gameID, userID, requiredRole)
}

// HasPermissionTx — версия HasPermission с передачей транзакции.
func (s *CoAuthorService) HasPermissionTx(tx *gorm.DB, gameID, userID uint, requiredRole string) (bool, error) {
	var game Game
	if err := tx.First(&game, gameID).Error; err != nil {
		return false, err
	}
	if game.AuthorID == userID {
		return true, nil
	}
	var co CoAuthor
	err := tx.Where("game_id = ? AND user_id = ?", gameID, userID).First(&co).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	switch requiredRole {
	case RoleContentEditor:
		return co.Role == RoleContentEditor || co.Role == RoleModerator, nil
	case RoleModerator:
		return co.Role == RoleModerator, nil
	case RoleObserver:
		return true, nil
	default:
		return false, fmt.Errorf("неизвестная роль: %s", requiredRole)
	}
}

// CanModerateGame — удобный метод для проверки права на модерацию игры.
func (s *CoAuthorService) CanModerateGame(gameID, userID uint) (bool, error) {
	return s.HasPermission(gameID, userID, RoleModerator)
}

// CanEditContent — удобный метод для проверки права на редактирование контента.
func (s *CoAuthorService) CanEditContent(gameID, userID uint) (bool, error) {
	return s.HasPermission(gameID, userID, RoleContentEditor)
}

// Add добавляет нового соавтора или восстанавливает удалённого.
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

	// Проверяем, есть ли запись (включая мягко удалённые)
	var co CoAuthor
	err := s.DB.Unscoped().Where("game_id = ? AND user_id = ?", gameID, newCoAuthorID).First(&co).Error
	if err == nil {
		if co.DeletedAt.Valid {
			// Восстанавливаем мягко удалённую запись
			co.DeletedAt = gorm.DeletedAt{}
			if err := s.DB.Save(&co).Error; err != nil {
				return err
			}
			return nil
		}
		return errors.New("этот пользователь уже соавтор")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	// Нет записи — создаём новую
	co = CoAuthor{GameID: gameID, UserID: newCoAuthorID, Role: RoleContentEditor}
	return s.DB.Create(&co).Error
}

// Remove мягко удаляет соавтора (устанавливает deleted_at).
func (s *CoAuthorService) Remove(gameID, coAuthorUserID, ownerID uint) error {
	var game Game
	if err := s.DB.First(&game, gameID).Error; err != nil {
		return err
	}
	if game.AuthorID != ownerID {
		return errors.New("только владелец может управлять соавторами")
	}
	// Используем Delete, который в GORM v2 автоматически устанавливает deleted_at
	return s.DB.Where("game_id = ? AND user_id = ?", gameID, coAuthorUserID).Delete(&CoAuthor{}).Error
}

// List возвращает список соавторов игры.
func (s *CoAuthorService) List(gameID uint) ([]CoAuthor, error) {
	var coAuthors []CoAuthor
	err := s.DB.Preload("User").Where("game_id = ?", gameID).Find(&coAuthors).Error
	return coAuthors, err
}
