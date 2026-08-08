// internal/domain/user/profile_repository.go
package user

import (
	"context"
	"errors"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

// gormProfileRepo — GORM-реализация ProfileRepository (A-2, pass 35).
// SQL перенесён из ProfileService — сервис больше не зависит от *gorm.DB.
type gormProfileRepo struct {
	db *gorm.DB
}

// NewGormProfileRepo создаёт репозиторий данных публичного профиля.
func NewGormProfileRepo(db *gorm.DB) ProfileRepository {
	return &gormProfileRepo{db: db}
}

func (r *gormProfileRepo) GetPublicProfileStats(ctx context.Context, userID uint) (*UserStats, error) {
	var stats UserStats
	err := r.db.WithContext(ctx).Table("users").
		Select(`
			(SELECT COUNT(*) FROM games WHERE author_id = ? AND deleted_at IS NULL) AS games_created,
			(SELECT COUNT(*) FROM game_passings
			   JOIN team_members ON team_members.team_id = game_passings.team_id
			  WHERE game_passings.status = 'finished' AND team_members.user_id = ?) AS games_played,
			(SELECT COUNT(*) FROM game_passings
			   JOIN team_members ON team_members.team_id = game_passings.team_id
			  WHERE game_passings.status = 'finished' AND game_passings.place = 1
			    AND team_members.user_id = ?) AS wins,
			COALESCE((SELECT score FROM player_ratings WHERE user_id = ?), 0) AS rating`,
			userID, userID, userID, userID).
		Where("id = ?", userID).
		Scan(&stats).Error
	if err != nil {
		return nil, err
	}
	return &stats, nil
}

func (r *gormProfileRepo) IsFollowing(ctx context.Context, followerID, authorID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("follows").
		Where("follower_id = ? AND author_id = ?", followerID, authorID).
		Count(&count).Error
	return count > 0, err
}

func (r *gormProfileRepo) GetRecentGames(ctx context.Context, authorID uint) ([]RecentGame, error) {
	var games []RecentGame
	err := r.db.WithContext(ctx).Table("games").
		Select("id, name, is_draft, cover_path, created_at").
		Where("author_id = ? AND is_draft = false AND deleted_at IS NULL", authorID).
		Order("created_at DESC").
		Limit(6).
		Find(&games).Error
	return games, err
}

func (r *gormProfileRepo) UpdateProfile(ctx context.Context, userID uint, name, email string) error {
	// Проверяем уникальность email у другого пользователя (кроме текущего).
	var count int64
	if err := r.db.WithContext(ctx).Model(&User{}).
		Where("email = ? AND id <> ?", email, userID).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrEmailTaken
	}
	// M-2: консистентно с UserService — смена email сбрасывает email_verified
	// (S-L3); #6: гонка на unique-индекс ловится как ErrEmailTaken.
	fields := map[string]any{"name": name, "email": email}
	var currentEmail string
	if err := r.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Select("email").Scan(&currentEmail).Error; err == nil && currentEmail != email {
		fields["email_verified"] = false
	}
	err := r.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Updates(fields).Error
	if err != nil {
		// #6: два параллельных сохранения на новый email — второй ловит 23505.
		var pgErr *pq.Error
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrEmailTaken
		}
		return err
	}
	return nil
}

func (r *gormProfileRepo) GetThemeSettings(ctx context.Context, userID uint) (ThemeSettings, error) {
	var raw string
	err := r.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Select("theme_settings").Scan(&raw).Error
	if err != nil {
		// Записи нет — это не ошибка, возвращаем дефолты без ошибки.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DefaultThemeSettings(), nil
		}
		return DefaultThemeSettings(), err
	}
	return ParseThemeSettings(raw)
}

func (r *gormProfileRepo) SaveThemeSettings(ctx context.Context, userID uint, ts ThemeSettings) error {
	jsonStr, err := MarshalThemeSettings(ts)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Update("theme_settings", jsonStr).Error
}

func (r *gormProfileRepo) GetGamesView(ctx context.Context, userID uint) (string, error) {
	var v string
	err := r.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Select("games_view").Scan(&v).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "table", nil
		}
		return "table", err
	}
	if v == "cards" {
		return "cards", nil
	}
	return "table", nil
}

func (r *gormProfileRepo) SaveGamesView(ctx context.Context, userID uint, view string) error {
	if view != "cards" {
		view = "table"
	}
	return r.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Update("games_view", view).Error
}
