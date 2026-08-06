// internal/domain/user/profile_service.go
package user

import (
	"context"
	"errors"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

// ErrEmailTaken — email уже занят другим пользователем.
var ErrEmailTaken = errors.New("email уже используется другим пользователем")

// UserStats содержит статистику для публичного профиля.
type UserStats struct {
	GamesCreated int64
	GamesPlayed  int64
	Wins         int64
	Rating       int
}

// RecentGame содержит данные о последней игре.
type RecentGame struct {
	ID        uint
	Name      string
	IsDraft   bool
	CoverPath string
	CreatedAt string
}

// ProfileService отвечает за статистику и данные публичного профиля.
type ProfileService struct {
	db *gorm.DB
}

// NewProfileService создаёт новый ProfileService.
func NewProfileService(db *gorm.DB) *ProfileService {
	return &ProfileService{db: db}
}

// GetPublicProfileStats загружает статистику пользователя.
func (s *ProfileService) GetPublicProfileStats(ctx context.Context, userID uint) (*UserStats, error) {
	var stats UserStats

	// Игры, созданные пользователем
	if err := s.db.WithContext(ctx).Table("games").
		Where("author_id = ? AND deleted_at IS NULL", userID).
		Count(&stats.GamesCreated).Error; err != nil {
		return nil, err
	}

	// Прохождения (через team_members)
	if err := s.db.WithContext(ctx).Table("game_passings").
		Joins("JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.status = ? AND team_members.user_id = ?", "finished", userID).
		Count(&stats.GamesPlayed).Error; err != nil {
		return nil, err
	}

	// Победы
	if err := s.db.WithContext(ctx).Table("game_passings").
		Joins("JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.status = ? AND game_passings.place = 1 AND team_members.user_id = ?", "finished", userID).
		Count(&stats.Wins).Error; err != nil {
		return nil, err
	}

	// Рейтинг
	if err := s.db.WithContext(ctx).Table("player_ratings").
		Where("user_id = ?", userID).
		Select("score").
		Scan(&stats.Rating).Error; err != nil {
		return nil, err
	}

	return &stats, nil
}

// IsFollowing проверяет, подписан ли пользователь на другого.
func (s *ProfileService) IsFollowing(ctx context.Context, followerID, authorID uint) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Table("follows").
		Where("follower_id = ? AND author_id = ?", followerID, authorID).
		Count(&count).Error
	return count > 0, err
}

// GetRecentGames загружает последние игры автора.
func (s *ProfileService) GetRecentGames(ctx context.Context, authorID uint) ([]RecentGame, error) {
	var games []RecentGame
	err := s.db.WithContext(ctx).Table("games").
		Select("id, name, is_draft, cover_path, created_at").
		Where("author_id = ? AND is_draft = false AND deleted_at IS NULL", authorID).
		Order("created_at DESC").
		Limit(6).
		Find(&games).Error
	return games, err
}

// UpdateProfile обновляет имя и email пользователя.
func (s *ProfileService) UpdateProfile(ctx context.Context, userID uint, name, email string) error {
	// Проверяем уникальность email у другого пользователя (кроме текущего).
	var count int64
	if err := s.db.WithContext(ctx).Model(&User{}).
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
	if err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Select("email").Scan(&currentEmail).Error; err == nil && currentEmail != email {
		fields["email_verified"] = false
	}
	err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Updates(fields).Error
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

// GetThemeSettings возвращает настройки темы пользователя (с дефолтами при пустой записи).
func (s *ProfileService) GetThemeSettings(ctx context.Context, userID uint) (ThemeSettings, error) {
	var raw string
	err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Select("theme_settings").Scan(&raw).Error
	if err != nil {
		// Записи нет — это не ошибка, возвращаем дефолты без ошибки.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DefaultThemeSettings(), nil
		}
		return DefaultThemeSettings(), err
	}
	return ParseThemeSettings(raw)
}

// SaveThemeSettings сохраняет настройки темы пользователя.
func (s *ProfileService) SaveThemeSettings(ctx context.Context, userID uint, ts ThemeSettings) error {
	jsonStr, err := MarshalThemeSettings(ts)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Update("theme_settings", jsonStr).Error
}

// GetUserThemeSettings загружает настройки темы пользователя из БД.
// Удобная обёртка для middleware (не требует создания сервиса на вызывающей стороне).
func GetUserThemeSettings(ctx context.Context, db *gorm.DB, userID uint) (ThemeSettings, error) {
	return NewProfileService(db).GetThemeSettings(ctx, userID)
}

// GetGamesView возвращает сохранённое предпочтение вида списка игр.
func (s *ProfileService) GetGamesView(ctx context.Context, userID uint) (string, error) {
	var v string
	err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Select("games_view").Scan(&v).Error
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

// SaveGamesView сохраняет предпочтение вида списка игр.
func (s *ProfileService) SaveGamesView(ctx context.Context, userID uint, view string) error {
	if view != "cards" {
		view = "table"
	}
	return s.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).Update("games_view", view).Error
}
