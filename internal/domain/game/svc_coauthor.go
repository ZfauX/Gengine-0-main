// internal/domain/game/co_author_service.go
package game

import (
	"context"
	"errors"

	"gengine-0/internal/pkg/rolecache"

	"gorm.io/gorm"
)

// Роли соавторов (пресеты — совместимость)
const (
	RoleContentEditor = "content_editor"
	RoleModerator     = "moderator"
	RoleObserver      = "observer"
	// RoleUploadMedia — псевдо-роль для проверки права на загрузку медиа (A-1).
	RoleUploadMedia = "upload_media_role"
)

// A-1 (pass 45): выборочные права соавторов.
const (
	PermRead        = "read"         // чтение (базовое, всегда у соавтора)
	PermEditContent = "edit_content" // уровни, вопросы, ответы, подсказки
	PermUploadMedia = "upload_media" // фото и видео материалы
	PermModerate    = "moderate"     // модерация (удаление контента, управление)
)

// PresetPermissions возвращает набор прав для пресета роли.
func PresetPermissions(role string) []string {
	switch role {
	case RoleModerator:
		return []string{PermRead, PermEditContent, PermUploadMedia, PermModerate}
	case RoleContentEditor:
		return []string{PermRead, PermEditContent, PermUploadMedia}
	case RoleObserver:
		return []string{PermRead}
	default:
		return []string{PermRead}
	}
}

// ErrNotOwner — действие доступно только владельцу игры (S-2, pass 33).
var ErrNotOwner = errors.New("только владелец может управлять соавторами")

// CoAuthorService — сервис соавторов. A-1 (pass 39): поле db удалено — все
// операции идут через CoAuthorRepository (HasPermission через repo, N-2 pass 38),
// а транзакционные варианты принимают tx параметром.
// P0-3 (pass 45): userRepo — для проверки супер-админа инсталляции (роль
// "admin" bypass'ит проверки прав).
// DEEP-REVIEW PASS-3 M9: роль кэшируется в ОБЩЕМ rolecache.Cache (один на
// приложение, тот же, что в middleware) — раньше два независимых кэша с разными
// TTL и раздельной инвалидацией давали окно неконсистентных прав.
type CoAuthorService struct {
	repo     CoAuthorRepository
	userRepo userRepository
	roleCache *rolecache.Cache
}

// userRepository — минимальный контракт, нужный для проверки роли (избегаем
// циклической зависимости game→user).
type userRepository interface {
	GetUserRole(ctx context.Context, id uint) (string, error)
}

func NewCoAuthorService() *CoAuthorService {
	return &CoAuthorService{roleCache: rolecache.New()}
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

// WithRoleCache внедряет ОБЩИЙ кэш ролей (DEEP-REVIEW PASS-3 M9): тот же
// инстанс, что использует middleware — инвалидация после смены роли в админке
// работает для обоих потребителей. Если не вызван — создаётся локальный кэш
// (совместимость с тестами).
func (s *CoAuthorService) WithRoleCache(c *rolecache.Cache) *CoAuthorService {
	if c != nil {
		s.roleCache = c
	}
	return s
}

// isSuperAdmin проверяет, что пользователь — админ инсталляции (роль "admin").
// Супер-админ имеет права на всё (P0-3). Роль кэшируется в общем rolecache.
func (s *CoAuthorService) isSuperAdmin(ctx context.Context, userID uint) bool {
	if s.userRepo == nil {
		return false
	}
	role, err := s.cachedRole(ctx, userID)
	return err == nil && role == "admin"
}

// cachedRole возвращает роль из БД с коротким TTL-кэшем (общий rolecache, M9).
func (s *CoAuthorService) cachedRole(ctx context.Context, userID uint) (string, error) {
	if s.roleCache == nil {
		s.roleCache = rolecache.New()
	}
	role, err := s.roleCache.Get(ctx, userID, s.userRepo.GetUserRole)
	if err != nil {
		return "", err
	}
	return role, nil
}

// InvalidateRoleCache сбрасывает кэш роли пользователя (вызывается после смены роли).
func (s *CoAuthorService) InvalidateRoleCache(userID uint) {
	if s.roleCache != nil {
		s.roleCache.Invalidate(userID)
	}
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

// HasPermission проверяет наличие у пользователя конкретной роли/права в игре.
// N-2 (pass 38): через репозиторий, а не raw s.db — единый путь с
// IsUserManager (раньше было два разных SQL-пути для одной проверки прав).
// A-1 (pass 45): учитывает выборочные Permissions (jsonb); для старых записей
// без Permissions — fallback на пресет роли.
func (s *CoAuthorService) HasPermission(ctx context.Context, gameID, userID uint, requiredRole string) (bool, error) {
	// P0-3 (pass 45): супер-админ инсталляции имеет права на всё.
	if s.isSuperAdmin(ctx, userID) {
		return true, nil
	}
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
	return coAuthorHasPermission(co, requiredRole), nil
}

// coAuthorHasPermission проверяет права соавтора на требуемую роль.
// Если Permissions заданы (jsonb непустой) — по пермишену; иначе по роли-пресету.
func coAuthorHasPermission(co *CoAuthor, requiredRole string) bool {
	required := roleToPermission(requiredRole)
	if len(co.Permissions) > 0 {
		for _, p := range co.Permissions {
			if p == required {
				return true
			}
		}
		return false
	}
	return hasCoAuthorRole(co.Role, requiredRole)
}

// roleToPermission маппит требуемую роль/псевдо-роль на конкретный пермишен (A-1).
func roleToPermission(requiredRole string) string {
	switch requiredRole {
	case RoleModerator:
		return PermModerate
	case RoleContentEditor:
		return PermEditContent
	case RoleUploadMedia:
		return PermUploadMedia
	default: // RoleObserver и любые другие
		return PermRead
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
	return coAuthorHasPermission(co, requiredRole), nil
}

// CanModerateGame — удобный метод для проверки права на модерацию игры.
func (s *CoAuthorService) CanModerateGame(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.HasPermission(ctx, gameID, userID, RoleModerator)
}

// CanEditContent — удобный метод для проверки права на редактирование контента.
func (s *CoAuthorService) CanEditContent(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.HasPermission(ctx, gameID, userID, RoleContentEditor)
}

// CanUploadMedia — удобный метод для проверки права на загрузку медиа
// (фото/обложки). Семантический слой гранулярных прав (DEEP-REVIEW IDEA-1):
// вызовы не должны ходить через строковые Role* константы напрямую.
func (s *CoAuthorService) CanUploadMedia(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.HasPermission(ctx, gameID, userID, RoleUploadMedia)
}

// CanModerateGameTx — транзакционный вариант CanModerateGame.
func (s *CoAuthorService) CanModerateGameTx(ctx context.Context, tx *gorm.DB, gameID, userID uint) (bool, error) {
	return s.HasPermissionTx(ctx, tx, gameID, userID, RoleModerator)
}

// CanEditContentTx — транзакционный вариант CanEditContent.
func (s *CoAuthorService) CanEditContentTx(ctx context.Context, tx *gorm.DB, gameID, userID uint) (bool, error) {
	return s.HasPermissionTx(ctx, tx, gameID, userID, RoleContentEditor)
}

// Add добавляет нового соавтора или восстанавливает удалённого.
// A-1 (pass 45): role — пресет, permissions — выборочные права (jsonb).
func (s *CoAuthorService) Add(ctx context.Context, gameID, newCoAuthorID, ownerID uint, role string, permissions []string) error {
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
	// Если permissions не заданы — берём пресет роли (A-1).
	if len(permissions) == 0 {
		permissions = PresetPermissions(role)
	}
	if role == "" {
		role = RoleContentEditor
	}

	// Проверяем, есть ли запись (включая мягко удалённые)
	co, findErr := s.repo.FindUnscopedByGameAndUser(ctx, gameID, newCoAuthorID)
	if findErr == nil {
		if co.DeletedAt.Valid {
			// Восстанавливаем мягко удалённую запись
			co.DeletedAt = gorm.DeletedAt{}
			co.Role = role
			co.Permissions = PermissionSlice(permissions)
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
	co = &CoAuthor{GameID: gameID, UserID: newCoAuthorID, Role: role, Permissions: PermissionSlice(permissions)}
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
