// internal/domain/game/co_author_service.go
package game

import (
	"context"
	"errors"

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

// CoAuthorService — сервис соавторов. A-1 (pass 39): поле db удалено — все
// операции идут через CoAuthorRepository (HasPermission через repo, N-2 pass 38),
// а транзакционные варианты принимают tx параметром.
type CoAuthorService struct {
	repo CoAuthorRepository
}

func NewCoAuthorService() *CoAuthorService {
	return &CoAuthorService{}
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
// N-2 (pass 38): через репозиторий, а не raw s.db — единый путь с
// IsUserManager (раньше было два разных SQL-пути для одной проверки прав).
func (s *CoAuthorService) HasPermission(ctx context.Context, gameID, userID uint, requiredRole string) (bool, error) {
	authorID, err := s.repo.GetGameAuthorID(ctx, gameID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, gorm.ErrRecordNotFound
		}
		return false, err
	}
	if authorID == userID {
		return true, nil
	}
	co, err := s.repo.FindByGameAndUser(ctx, gameID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return hasCoAuthorRole(co.Role, requiredRole), nil
}

// hasCoAuthorRole проверяет, что роль соавтора покрывает требуемую.
func hasCoAuthorRole(role, requiredRole string) bool {
	switch requiredRole {
	case RoleContentEditor:
		return role == RoleContentEditor || role == RoleModerator
	case RoleModerator:
		return role == RoleModerator
	case RoleObserver:
		return true
	default:
		return false
	}
}

// HasPermissionTx — версия HasPermission с передачей транзакции.
// A-2 (pass 39): через репозиторий (GetGameAuthorIDWithTx + FindByGameAndUserWithTx) —
// единый путь к данным, raw SQL убран из сервиса.
func (s *CoAuthorService) HasPermissionTx(tx *gorm.DB, gameID, userID uint, requiredRole string) (bool, error) {
	authorID, err := s.repo.GetGameAuthorIDWithTx(context.Background(), tx, gameID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, gorm.ErrRecordNotFound
		}
		return false, err
	}
	if authorID == userID {
		return true, nil
	}
	co, err := s.repo.FindByGameAndUserWithTx(context.Background(), tx, gameID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return hasCoAuthorRole(co.Role, requiredRole), nil
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
