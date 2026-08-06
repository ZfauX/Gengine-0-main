// Package user — repository interfaces for user domain.
package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"gengine-0/internal/pkg/sqlutil"

	"gorm.io/gorm"
)

// UserSearchResult — лёгкий результат поиска пользователей (id/name/email).
type UserSearchResult struct {
	ID    uint
	Name  string
	Email string
}

// Dashboard query rows (C1 — без *gorm.DB в сервисе).
type DashboardGameRow struct {
	ID      uint
	Name    string
	IsDraft bool
}

type DashboardTeamRow struct {
	TeamID        uint
	TeamName      string
	CaptainID     uint
	PassingID     uint
	GameID        uint
	PassingStatus string
	GameName      string
}

type DashboardInvitationRow struct {
	ID       uint
	TeamID   uint
	TeamName string
	Status   string
}

// UserRepository определяет контракт для работы с пользователями.
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id uint) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetPublicProfile(ctx context.Context, id uint) (*User, error)
	GetByIDWithAchievementsAndSubscriptions(ctx context.Context, id uint) (*User, error)
	SearchUsersLight(ctx context.Context, query string, limit int) ([]UserSearchResult, error)
	// Dashboard-запросы (C1 — без прямого *gorm.DB в сервисе).
	DashboardAuthoredGames(ctx context.Context, userID uint) ([]DashboardGameRow, error)
	DashboardTeams(ctx context.Context, userID uint) ([]DashboardTeamRow, error)
	DashboardInvitations(ctx context.Context, userID uint) ([]DashboardInvitationRow, error)
	Update(ctx context.Context, id uint, fields map[string]any) error
	GetByRole(ctx context.Context, role string) ([]User, error)
	GetUserRole(ctx context.Context, id uint) (string, error)
	// GetGamesView возвращает предпочтение вида списка игр (U-3: server-side,
	// чтобы не было FOUC при рендере списка).
	GetGamesView(ctx context.Context, userID uint) (string, error)

	// Методы для админки с пагинацией
	Count(ctx context.Context) (int64, error)
	CountByRole(ctx context.Context, role string) (int64, error)
	CountSearch(ctx context.Context, query, role string) (int64, error)
	List(ctx context.Context, role string) ([]User, error)
	ListPaginated(ctx context.Context, role string, offset, limit int) ([]User, error)
	SearchPaginated(ctx context.Context, query, role string, offset, limit int) ([]User, error)
	Delete(ctx context.Context, id uint) error

	// AtomicIncrementFailedAttempts атомарно инкрементирует failed_login_attempts
	// и возвращает новое значение.
	AtomicIncrementFailedAttempts(ctx context.Context, userID uint) (int, error)
}

// AchievementRepository определяет контракт для работы с достижениями.
type AchievementRepository interface {
	Award(ctx context.Context, userID uint, achievement *Achievement) error
	GetByUserID(ctx context.Context, userID uint) ([]Achievement, error)
	Seed(ctx context.Context) error
	FirstOrCreate(ctx context.Context, achievement *Achievement) error
}

// PasswordResetRepository — контракт для сброса пароля.
type PasswordResetRepository interface {
	CreateToken(ctx context.Context, token *PasswordResetToken) error
	GetToken(ctx context.Context, tokenStr string) (*PasswordResetToken, error)
	GetTokenByResetCode(ctx context.Context, code string) (*PasswordResetToken, error)
	DeleteToken(ctx context.Context, token *PasswordResetToken) error
	MarkTokenUsed(ctx context.Context, id uint, usedAt time.Time) error
}

// EmailVerificationRepository — контракт для верификации email.
type EmailVerificationRepository interface {
	CreateToken(ctx context.Context, token *EmailVerificationToken) error
	GetToken(ctx context.Context, tokenStr string) (*EmailVerificationToken, error)
	GetTokenByCode(ctx context.Context, code string) (*EmailVerificationToken, error)
	DeleteToken(ctx context.Context, token *EmailVerificationToken) error
	DeleteByTokenHash(ctx context.Context, tokenHash string) error
	DeleteByUserID(ctx context.Context, userID uint) error
}

// ExternalLoginRepository — контракт для OAuth-привязок.
type ExternalLoginRepository interface {
	FindOrCreate(ctx context.Context, login *ExternalLogin) error
}

// RefreshTokenRepository — контракт для работы с refresh-токенами (добавлен).
type RefreshTokenRepository interface {
	Create(ctx context.Context, token *RefreshToken) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	// GetByTokenHashIncludingRevoked ищет токен независимо от статуса revoked_at
	// (нужен для детекции reuse отозванного токена).
	GetByTokenHashIncludingRevoked(ctx context.Context, tokenHash string) (*RefreshToken, error)
	// ClaimForRefresh атомарно отзывает токен, если он ещё не отозван
	// (UPDATE ... WHERE id=? AND revoked_at IS NULL). Возвращает true, если
	// токен был потреблён этим вызовом. RowsAffected==0 означает, что другой
	// запрос уже использовал тот же токен — детект reuse без гонки (S4).
	ClaimForRefresh(ctx context.Context, id uint) (bool, error)
	// ClaimAndCreate атомарно отзывает старый токен и сохраняет новый в одной
	// транзакции (C-2): сбой создания не оставляет клиента без refresh-токена.
	ClaimAndCreate(ctx context.Context, id uint, newToken *RefreshToken) (bool, error)
	Revoke(ctx context.Context, id uint) error
	// RevokeAllByFamily отзывает всю семью refresh-токенов (при детекции кражи).
	RevokeAllByFamily(ctx context.Context, familyID string) error
	RevokeAllForUser(ctx context.Context, userID uint) error
	DeleteExpired(ctx context.Context) error
}

// PushSubscriptionRepository — контракт для работы с push-подписками.
type PushSubscriptionRepository interface {
	FindByEndpoint(ctx context.Context, endpoint string) (*PushSubscription, error)
	Update(ctx context.Context, sub *PushSubscription) error
	Create(ctx context.Context, sub *PushSubscription) error
	DeleteByEndpointAndUser(ctx context.Context, endpoint string, userID uint) error
}

// ---------- GORM implementations ----------

var _ UserRepository = (*gormUserRepo)(nil)
var _ PushSubscriptionRepository = (*gormPushSubscriptionRepo)(nil)

type gormUserRepo struct{ db *gorm.DB }

func NewGormUserRepo(db *gorm.DB) UserRepository { return &gormUserRepo{db} }

type gormPushSubscriptionRepo struct{ db *gorm.DB }

func NewGormPushSubscriptionRepo(db *gorm.DB) PushSubscriptionRepository {
	return &gormPushSubscriptionRepo{db}
}

func (r *gormPushSubscriptionRepo) FindByEndpoint(ctx context.Context, endpoint string) (*PushSubscription, error) {
	var sub PushSubscription
	err := r.db.WithContext(ctx).Where("endpoint = ?", endpoint).First(&sub).Error
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (r *gormPushSubscriptionRepo) Update(ctx context.Context, sub *PushSubscription) error {
	return r.db.WithContext(ctx).Model(sub).Updates(map[string]any{
		"auth":   sub.Auth,
		"p256dh": sub.P256dh,
	}).Error
}

func (r *gormPushSubscriptionRepo) Create(ctx context.Context, sub *PushSubscription) error {
	return r.db.WithContext(ctx).Create(sub).Error
}

func (r *gormPushSubscriptionRepo) DeleteByEndpointAndUser(ctx context.Context, endpoint string, userID uint) error {
	return r.db.WithContext(ctx).Where("endpoint = ? AND user_id = ?", endpoint, userID).Delete(&PushSubscription{}).Error
}

func (r *gormUserRepo) Create(ctx context.Context, user *User) error {
	return r.db.WithContext(ctx).Create(user).Error
}
func (r *gormUserRepo) GetByID(ctx context.Context, id uint) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).First(&u, id).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}
func (r *gormUserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&u).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}
func (r *gormUserRepo) GetPublicProfile(ctx context.Context, id uint) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).Preload("Achievements").First(&u, id).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}
func (r *gormUserRepo) GetByIDWithAchievementsAndSubscriptions(ctx context.Context, id uint) (*User, error) {
	var u User
	err := r.db.WithContext(ctx).Preload("Achievements").Preload("Subscriptions").First(&u, id).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// SearchUsersLight ищет пользователей по имени/email (только id, name, email) — без паролей.
func (r *gormUserRepo) SearchUsersLight(ctx context.Context, query string, limit int) ([]UserSearchResult, error) {
	items := []UserSearchResult{}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	escaped := sqlutil.EscapeLike(query)
	err := r.db.WithContext(ctx).Model(&User{}).
		Select("id, name, email").
		Where("name ILIKE ? OR email ILIKE ?", "%"+escaped+"%", "%"+escaped+"%").
		Limit(limit).
		Scan(&items).Error
	return items, err
}

func (r *gormUserRepo) DashboardAuthoredGames(ctx context.Context, userID uint) ([]DashboardGameRow, error) {
	rows := []DashboardGameRow{}
	err := r.db.WithContext(ctx).Table("games").
		Select("id, name, is_draft").
		Where("author_id = ? AND deleted_at IS NULL", userID).
		Find(&rows).Error
	return rows, err
}

func (r *gormUserRepo) DashboardTeams(ctx context.Context, userID uint) ([]DashboardTeamRow, error) {
	rows := []DashboardTeamRow{}
	err := r.db.WithContext(ctx).Raw(`
		SELECT t.id as team_id, t.name as team_name, t.captain_id,
		       COALESCE(gp.id, 0) as passing_id,
		       COALESCE(gp.game_id, 0) as game_id,
		       COALESCE(gp.status, '') as passing_status,
		       COALESCE(g.name, '') as game_name
		FROM teams t
		LEFT JOIN game_passings gp ON gp.team_id = t.id AND gp.status IN ('accepted', 'started', 'finished')
		LEFT JOIN games g ON g.id = gp.game_id AND g.deleted_at IS NULL
		WHERE t.id IN (
			SELECT id FROM teams WHERE captain_id = ?
			UNION
			SELECT t.id FROM teams t
			INNER JOIN team_members tm ON tm.team_id = t.id
			WHERE tm.user_id = ? AND t.captain_id != ?
		)
	`, userID, userID, userID).Scan(&rows).Error
	return rows, err
}

func (r *gormUserRepo) DashboardInvitations(ctx context.Context, userID uint) ([]DashboardInvitationRow, error) {
	rows := []DashboardInvitationRow{}
	err := r.db.WithContext(ctx).Table("invitations").
		Select("invitations.id, invitations.team_id, teams.name as team_name, invitations.status").
		Joins("JOIN teams ON teams.id = invitations.team_id").
		Where("invitations.user_id = ? AND invitations.status = ?", userID, "pending").
		Order("invitations.created_at DESC").
		Find(&rows).Error
	return rows, err
}
func (r *gormUserRepo) Update(ctx context.Context, id uint, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).Updates(fields).Error
}

// GetByRole returns multiple users by role.
func (r *gormUserRepo) GetByRole(ctx context.Context, role string) ([]User, error) {
	var users []User
	err := r.db.WithContext(ctx).Where("role = ?", role).Find(&users).Error
	return users, err
}

func (r *gormUserRepo) GetUserRole(ctx context.Context, id uint) (string, error) {
	var role string
	err := r.db.WithContext(ctx).Table("users").Select("role").Where("id = ?", id).Scan(&role).Error
	return role, err
}

func (r *gormUserRepo) GetGamesView(ctx context.Context, userID uint) (string, error) {
	var view string
	err := r.db.WithContext(ctx).Table("users").Select("games_view").Where("id = ?", userID).Scan(&view).Error
	if err != nil {
		return "table", err
	}
	if view == "" {
		return "table", nil
	}
	return view, nil
}

// --- Методы для админки ---
func (r *gormUserRepo) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&User{}).Count(&count).Error
	return count, err
}

func (r *gormUserRepo) CountByRole(ctx context.Context, role string) (int64, error) {
	var count int64
	query := r.db.WithContext(ctx).Model(&User{})
	if role != "" {
		query = query.Where("role = ?", role)
	}
	err := query.Count(&count).Error
	return count, err
}

func (r *gormUserRepo) List(ctx context.Context, role string) ([]User, error) {
	var users []User
	query := r.db.WithContext(ctx).Model(&User{})
	if role != "" {
		query = query.Where("role = ?", role)
	}
	err := query.Find(&users).Error
	return users, err
}

func (r *gormUserRepo) ListPaginated(ctx context.Context, role string, offset, limit int) ([]User, error) {
	var users []User
	query := r.db.WithContext(ctx).Model(&User{})
	if role != "" {
		query = query.Where("role = ?", role)
	}
	err := query.Offset(offset).Limit(limit).Find(&users).Error
	return users, err
}

func (r *gormUserRepo) CountSearch(ctx context.Context, query, role string) (int64, error) {
	var count int64
	// C-12: экранируем wildcard LIKE, иначе %/_ в запросе матчат всё.
	esc := sqlutil.EscapeLike(query)
	q := r.db.WithContext(ctx).Model(&User{}).Where("name ILIKE ? OR email ILIKE ?", "%"+esc+"%", "%"+esc+"%")
	if role != "" {
		q = q.Where("role = ?", role)
	}
	err := q.Count(&count).Error
	return count, err
}

func (r *gormUserRepo) SearchPaginated(ctx context.Context, query, role string, offset, limit int) ([]User, error) {
	var users []User
	// C-12: экранируем wildcard LIKE.
	esc := sqlutil.EscapeLike(query)
	q := r.db.WithContext(ctx).Model(&User{}).Where("name ILIKE ? OR email ILIKE ?", "%"+esc+"%", "%"+esc+"%")
	if role != "" {
		q = q.Where("role = ?", role)
	}
	err := q.Offset(offset).Limit(limit).Find(&users).Error
	return users, err
}

// Delete удаляет пользователя полностью (hard delete) вместе с его зависимыми записями.
// Soft delete оставлял email занятым навсегда (uniqueIndex) — повторная регистрация
// была невозможна. Очистка выполняется только для существующих таблиц.
func (r *gormUserRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range []any{
			&NotificationSetting{},
			&PushSubscription{},
			&RefreshToken{},
			&ExternalLogin{},
			&EmailVerificationToken{},
			&PasswordResetToken{},
			&WebAuthnCredential{},
		} {
			if tx.Migrator().HasTable(model) {
				if err := tx.Where("user_id = ?", id).Delete(model).Error; err != nil {
					return err
				}
			}
		}
		// Жёсткое удаление самого пользователя (без soft-delete).
		return tx.Unscoped().Where("id = ?", id).Delete(&User{}).Error
	})
}

// AtomicIncrementFailedAttempts атомарно инкрементирует failed_login_attempts
// и возвращает новое значение.
func (r *gormUserRepo) AtomicIncrementFailedAttempts(ctx context.Context, userID uint) (int, error) {
	var attempts int
	err := r.db.WithContext(ctx).
		Raw("UPDATE users SET failed_login_attempts = failed_login_attempts + 1 WHERE id = ? RETURNING failed_login_attempts", userID).
		Scan(&attempts).Error
	return attempts, err
}

type gormAchievementRepo struct{ db *gorm.DB }

func NewGormAchievementRepo(db *gorm.DB) AchievementRepository { return &gormAchievementRepo{db} }

func (r *gormAchievementRepo) Award(ctx context.Context, userID uint, achievement *Achievement) error {
	return r.db.WithContext(ctx).Model(&User{Model: gorm.Model{ID: userID}}).
		Association("Achievements").Append(achievement)
}
func (r *gormAchievementRepo) GetByUserID(ctx context.Context, userID uint) ([]Achievement, error) {
	var a []Achievement
	err := r.db.WithContext(ctx).Joins("JOIN user_achievements ON user_achievements.achievement_id = achievements.id").
		Where("user_achievements.user_id = ?", userID).Find(&a).Error
	return a, err
}
func (r *gormAchievementRepo) Seed(ctx context.Context) error { return nil }
func (r *gormAchievementRepo) FirstOrCreate(ctx context.Context, achievement *Achievement) error {
	return r.db.WithContext(ctx).Where("code = ?", achievement.Code).FirstOrCreate(achievement).Error
}

type gormPasswordResetRepo struct{ db *gorm.DB }

func NewGormPasswordResetRepo(db *gorm.DB) PasswordResetRepository { return &gormPasswordResetRepo{db} }
func (r *gormPasswordResetRepo) CreateToken(ctx context.Context, token *PasswordResetToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}
func (r *gormPasswordResetRepo) GetToken(ctx context.Context, tokenStr string) (*PasswordResetToken, error) {
	hash := sha256.Sum256([]byte(tokenStr))
	var t PasswordResetToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", hex.EncodeToString(hash[:])).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormPasswordResetRepo) GetTokenByResetCode(ctx context.Context, code string) (*PasswordResetToken, error) {
	var t PasswordResetToken
	err := r.db.WithContext(ctx).Where("reset_code = ?", code).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormPasswordResetRepo) DeleteToken(ctx context.Context, token *PasswordResetToken) error {
	return r.db.WithContext(ctx).Delete(token).Error
}
func (r *gormPasswordResetRepo) MarkTokenUsed(ctx context.Context, id uint, usedAt time.Time) error {
	// Атомарное потребление: conditional update по used_at IS NULL гарантирует,
	// что токен можно использовать ровно один раз даже при параллельных запросах.
	res := r.db.WithContext(ctx).Model(&PasswordResetToken{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", usedAt)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

type gormEmailVerificationRepo struct{ db *gorm.DB }

func NewGormEmailVerificationRepo(db *gorm.DB) EmailVerificationRepository {
	return &gormEmailVerificationRepo{db}
}
func (r *gormEmailVerificationRepo) CreateToken(ctx context.Context, token *EmailVerificationToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}
func (r *gormEmailVerificationRepo) GetToken(ctx context.Context, tokenStr string) (*EmailVerificationToken, error) {
	hash := sha256.Sum256([]byte(tokenStr))
	var t EmailVerificationToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", hex.EncodeToString(hash[:])).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormEmailVerificationRepo) DeleteToken(ctx context.Context, token *EmailVerificationToken) error {
	return r.db.WithContext(ctx).Unscoped().Delete(token).Error
}
func (r *gormEmailVerificationRepo) GetTokenByCode(ctx context.Context, code string) (*EmailVerificationToken, error) {
	var t EmailVerificationToken
	err := r.db.WithContext(ctx).Where("verification_code = ?", code).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormEmailVerificationRepo) DeleteByTokenHash(ctx context.Context, tokenHash string) error {
	return r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).Delete(&EmailVerificationToken{}).Error
}
func (r *gormEmailVerificationRepo) DeleteByUserID(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&EmailVerificationToken{}).Error
}

type gormExternalLoginRepo struct{ db *gorm.DB }

func NewGormExternalLoginRepo(db *gorm.DB) ExternalLoginRepository { return &gormExternalLoginRepo{db} }
func (r *gormExternalLoginRepo) FindOrCreate(ctx context.Context, login *ExternalLogin) error {
	return r.db.WithContext(ctx).Where("provider = ? AND external_id = ?", login.Provider, login.ExternalID).
		FirstOrCreate(login).Error
}

// ---------- GORM implementation for RefreshTokenRepository (добавлен) ----------

type gormRefreshTokenRepo struct{ db *gorm.DB }

func NewGormRefreshTokenRepo(db *gorm.DB) RefreshTokenRepository {
	return &gormRefreshTokenRepo{db: db}
}

func (r *gormRefreshTokenRepo) Create(ctx context.Context, token *RefreshToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

func (r *gormRefreshTokenRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	var token RefreshToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL", tokenHash).
		First(&token).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *gormRefreshTokenRepo) GetByTokenHashIncludingRevoked(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	var token RefreshToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ?", tokenHash).
		First(&token).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *gormRefreshTokenRepo) RevokeAllByFamily(ctx context.Context, familyID string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&RefreshToken{}).
		Where("family_id = ? AND revoked_at IS NULL", familyID).
		Update("revoked_at", now).Error
}

func (r *gormRefreshTokenRepo) ClaimForRefresh(ctx context.Context, id uint) (bool, error) {
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&RefreshToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", now)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (r *gormRefreshTokenRepo) ClaimAndCreate(ctx context.Context, id uint, newToken *RefreshToken) (bool, error) {
	claimed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		res := tx.Model(&RefreshToken{}).
			Where("id = ? AND revoked_at IS NULL", id).
			Update("revoked_at", now)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // другой запрос уже потреблял токен — не создаём наследника
		}
		claimed = true
		return tx.Create(newToken).Error
	})
	if err != nil {
		return false, err
	}
	return claimed, nil
}

func (r *gormRefreshTokenRepo) Revoke(ctx context.Context, id uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&RefreshToken{}).
		Where("id = ?", id).
		Update("revoked_at", now).Error
}

func (r *gormRefreshTokenRepo) RevokeAllForUser(ctx context.Context, userID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now).Error
}

func (r *gormRefreshTokenRepo) DeleteExpired(ctx context.Context) error {
	return r.db.WithContext(ctx).
		Where("expires_at < ?", time.Now()).
		Delete(&RefreshToken{}).Error
}
