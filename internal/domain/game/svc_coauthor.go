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
// P0-3 (pass 45): userRepo — для проверки супер-админа инсталляции (роль
// "admin" bypass'ит проверки прав).
type CoAuthorService struct {
	repo     CoAuthorRepository
	userRepo userRepository
}

// userRepository — минимальный контракт, нужный для проверки роли (избегаем
// циклической зависимости game→user).
type userRepository interface {
	GetUserRole(ctx context.Context, id uint) (string, error)
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

// WithUserRepository внедряет репозиторий пользователей для проверки
// супер-админа инсталляции (P0-3, pass 45).
func (s *CoAuthorService) WithUserRepository(repo userRepository) *CoAuthorService {
	s.userRepo = repo
	return s
}

// isSuperAdmin проверяет, что пользователь — админ инсталляции (роль "admin").
// Супер-админ имеет права на всё (P0-3).
func (s *CoAuthorService) isSuperAdmin(ctx context.Context, userID uint) bool {
	if s.userRepo == nil {
		return false
	}
	role, err := s.userRepo.GetUserRole(ctx, userID)
	return err == nil && role == "admin"
}

// IsUserManager проверяет, является ли пользователь автором или соавтором игры.
// Оптимизация: использует один запрос с UNION вместо двух отдельных запросов.
// P0-3 (pass 45): супер-админ всегда менеджер.
func (s *CoAuthorService) IsUserManager(ctx context.Context, gameID, userID uint) (bool, error) {
	if s.isSuperAdmin(ctx, userID) {
		return true, nil
	}
	return s.repo.IsUserManager(ctx, gameID, userID)
}

// HasPermission проверяет наличие у пользователя конкретной роли в игре.
// N-2 (pass 38): через репозиторий, а не raw s.db — единый путь с
// IsUserManager (раньше было два разных SQL-пути для одной проверки прав).
// P-44-4 (pass 45): один запрос (автор ИЛИ соавтор с допустимой ролью) вместо
// GetGameAuthorID + FindByGameAndUser.
func (s *CoAuthorService) HasPermission(ctx context.Context, gameID, userID uint, requiredRole string) (bool, error) {
	// P0-3 (pass 45): супер-админ инсталляции имеет права на всё.
	if s.isSuperAdmin(ctx, userID) {
		return true, nil
	}
	return s.repo.HasPermissionRole(ctx, gameID, userID, rolesForRequired(requiredRole))
}

// rolesForRequired возвращает роли соавторов, покрывающие требуемую роль
// (пустой список = любой соавтор, RoleObserver). Роль автора покрывается
// отдельной веткой запроса HasPermissionRole.
func rolesForRequired(requiredRole string) []string {
	switch requiredRole {
	case RoleContentEditor:
		return []string{RoleContentEditor, RoleModerator}
	case RoleModerator:
		return []string{RoleModerator}
	default: // RoleObserver — любой соавтор
		return nil
	}
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
// C1 (pass 40): ctx прокидывается из вызовов (раньше context.Background —
// запросы прав не отменялись при disconnect).
func (s *CoAuthorService) HasPermissionTx(ctx context.Context, tx *gorm.DB, gameID, userID uint, requiredRole string) (bool, error) {
	// P0-3 (pass 45): супер-админ инсталляции имеет права на всё.
	if s.isSuperAdmin(ctx, userID) {
		return true, nil
	}
	authorID, err := s.repo.GetGameAuthorIDWithTx(ctx, tx, gameID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, gorm.ErrRecordNotFound
		}
		return false, err
	}
	if authorID == userID {
		return true, nil
	}
	co, err := s.repo.FindByGameAndUserWithTx(ctx, tx, gameID, userID)
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
