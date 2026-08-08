// internal/domain/game/co_author_service.go
package game

import (
	"context"
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

// ErrNotOwner — действие доступно только владельцу игры (S-2, pass 33).
var ErrNotOwner = errors.New("только владелец может управлять соавторами")

type CoAuthorService struct {
	db   *gorm.DB
	repo CoAuthorRepository
}

func NewCoAuthorService(db *gorm.DB) *CoAuthorService {
	return &CoAuthorService{db: db}
}

// WithRepository внедряет репозиторий соавторов (A-1, pass 32): устраняет
// дублирование запросов между сервисом и репозиторием.
func (s *CoAuthorService) WithRepository(repo CoAuthorRepository) *CoAuthorService {
	s.repo = repo
	return s
}

// IsUserManager проверяет, является ли пользователь автором или соавтором игры.
// Оптимизация: использует один запрос с UNION вместо двух отдельных запросов.
func (s *CoAuthorService) IsUserManager(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.repo.IsUserManager(ctx, gameID, userID)
}

// HasPermission проверяет наличие у пользователя конкретной роли в игре.
func (s *CoAuthorService) HasPermission(ctx context.Context, gameID, userID uint, requiredRole string) (bool, error) {
	return s.HasPermissionTx(s.db.WithContext(ctx), gameID, userID, requiredRole)
}

// HasPermissionTx — версия HasPermission с передачей транзакции.
// M14 (pass 30): загружаем только author_id (Select+Scan) вместо полной
// строки Game — description и другие тяжёлые поля не читаются.
// First(&scalar) с Table() не работает в GORM ("model value required"),
// поэтому Scan + проверка RowsAffected для ErrRecordNotFound (pass 30).
func (s *CoAuthorService) HasPermissionTx(tx *gorm.DB, gameID, userID uint, requiredRole string) (bool, error) {
	var authorID uint
	res := tx.Model(&Game{}).Select("author_id").Where("id = ?", gameID).Scan(&authorID)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, gorm.ErrRecordNotFound
	}
	if authorID == userID {
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
func (s *CoAuthorService) CanModerateGame(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.HasPermission(ctx, gameID, userID, RoleModerator)
}

// CanEditContent — удобный метод для проверки права на редактирование контента.
func (s *CoAuthorService) CanEditContent(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.HasPermission(ctx, gameID, userID, RoleContentEditor)
}

// Add добавляет нового соавтора или восстанавливает удалённого.
func (s *CoAuthorService) Add(ctx context.Context, gameID, newCoAuthorID, ownerID uint) error {
	authorID, err := s.repo.GetGameAuthorID(ctx, gameID)
	if err != nil {
		return err
	}
	if authorID != ownerID {
		return ErrNotOwner
	}
	if authorID == newCoAuthorID {
		return errors.New("владелец уже имеет полный доступ")
	}

	// Проверяем, есть ли запись (включая мягко удалённые)
	co, findErr := s.repo.FindUnscopedByGameAndUser(ctx, gameID, newCoAuthorID)
	if findErr == nil {
		if co.DeletedAt.Valid {
			// Восстанавливаем мягко удалённую запись
			co.DeletedAt = gorm.DeletedAt{}
			if saveErr := s.repo.Save(ctx, co); saveErr != nil {
				return saveErr
			}
			return nil
		}
		return errors.New("этот пользователь уже соавтор")
	} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return findErr
	}

	// Нет записи — создаём новую
	co = &CoAuthor{GameID: gameID, UserID: newCoAuthorID, Role: RoleContentEditor}
	return s.repo.Create(ctx, co)
}

// Remove мягко удаляет соавтора (устанавливает deleted_at).
func (s *CoAuthorService) Remove(ctx context.Context, gameID, coAuthorUserID, ownerID uint) error {
	authorID, err := s.repo.GetGameAuthorID(ctx, gameID)
	if err != nil {
		return err
	}
	if authorID != ownerID {
		return ErrNotOwner
	}
	// Используем Delete, который в GORM v2 автоматически устанавливает deleted_at
	return s.repo.DeleteByGameAndUser(ctx, gameID, coAuthorUserID)
}

// List возвращает список соавторов игры.
func (s *CoAuthorService) List(ctx context.Context, gameID uint) ([]CoAuthor, error) {
	return s.repo.ListByGame(ctx, gameID)
}
