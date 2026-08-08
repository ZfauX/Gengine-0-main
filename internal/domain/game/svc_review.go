// internal/domain/game/review_service.go
package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/sanitize"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ReviewService struct {
	DB *gorm.DB
	// Cache опционален — используется для инвалидации кэша рейтинга при новых отзывах.
	Cache cache.CacheStore
}

func NewReviewService(db *gorm.DB) *ReviewService {
	return &ReviewService{DB: db}
}

// WithCache устанавливает кэш для инвалидации рейтинга при создании отзыва.
func (s *ReviewService) WithCache(c cache.CacheStore) *ReviewService {
	s.Cache = c
	return s
}

func (s *ReviewService) CanReview(gameID, userID uint) (bool, error) {
	var count int64
	err := s.DB.Model(&GamePassing{}).
		Joins("JOIN teams ON teams.id = game_passings.team_id").
		Where("game_passings.game_id = ? AND game_passings.status = ?", gameID, StatusFinished).
		Where("(teams.captain_id = ? OR EXISTS (SELECT 1 FROM team_members WHERE team_members.team_id = game_passings.team_id AND team_members.user_id = ?))", userID, userID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	var reviewCount int64
	if err := s.DB.Model(&Review{}).Where("game_id = ? AND user_id = ?", gameID, userID).Count(&reviewCount).Error; err != nil {
		return false, err
	}
	return reviewCount == 0, nil
}

// Create создаёт отзыв. ctx — контекст запроса (A-10, pass 31).
func (s *ReviewService) Create(ctx context.Context, gameID, userID uint, rating int, comment string) error {
	// C-2: максимальный рейтинг 5 — согласовано с хендлером и UI.
	if rating < 1 || rating > 5 {
		return errors.New("рейтинг должен быть от 1 до 5")
	}
	can, err := s.CanReview(gameID, userID)
	if err != nil {
		return err
	}
	if !can {
		return errors.New("вы не можете оставить отзыв")
	}
	// Санитизация HTML-тегов в комментарии
	cleanComment := sanitize.StripHTML(comment)
	review := Review{GameID: gameID, UserID: userID, Rating: rating, Comment: cleanComment}
	// ON CONFLICT (idx_reviews_game_user) — защита от гонки параллельных POST (C-3).
	res := s.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "game_id"}, {Name: "user_id"}},
		DoNothing: true,
	}).Create(&review)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("вы уже оставили отзыв")
	}
	// Инвалидируем кэш рейтинга, отзывов и карточки игры —
	// иначе Game.RatingValue остаётся устаревшим до 5 мин (pass 26).
	// M13 (pass 30): НЕ сбрасываем глобальную версию листинга (games:list:version) —
	// при активном потоке отзывов каждый отзыв делал 30с-кэш анонимного листинга
	// бесполезным. Рейтинг в листинге обновится по своему TTL (30с) — приемлемая
	// задержка вместо полного отказа от кэша.
	if s.Cache != nil {
		s.Cache.DeleteWithCtx(ctx, fmt.Sprintf("rating:game:%d", gameID))
		s.Cache.DeleteWithCtx(ctx, fmt.Sprintf("reviews:game:%d", gameID))
		s.Cache.DeleteWithCtx(ctx, fmt.Sprintf("game:%d", gameID))
	}
	return nil
}

// ListByGame возвращает отзывы игры (с кэшем 5 мин; инвалидируется при Create).
func (s *ReviewService) ListByGame(ctx context.Context, gameID uint) ([]Review, error) {
	cacheKey := fmt.Sprintf("reviews:game:%d", gameID)
	if s.Cache != nil {
		var cached []Review
		if cacheGetJSON(s.Cache, ctx, cacheKey, &cached) {
			return cached, nil
		}
	}

	var reviews []Review
	err := s.DB.WithContext(ctx).Preload("User", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, name, avatar_path")
	}).Where("game_id = ?", gameID).Order("created_at DESC").Find(&reviews).Error
	if err != nil {
		return nil, err
	}

	if s.Cache != nil {
		s.Cache.SetWithCtx(ctx, cacheKey, reviews, 5*time.Minute)
	}
	return reviews, nil
}

func (s *ReviewService) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	var result struct {
		Avg   float64
		Count int64
	}
	err := s.DB.WithContext(ctx).Model(&Review{}).
		Where("game_id = ?", gameID).
		Select("COALESCE(AVG(rating), 0) as avg, COUNT(*) as count").
		Scan(&result).Error
	return result.Avg, result.Count, err
}
