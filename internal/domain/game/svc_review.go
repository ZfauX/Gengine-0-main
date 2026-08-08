// internal/domain/game/review_service.go
package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/sanitize"
)

type ReviewService struct {
	repo ReviewRepository
	// Cache опционален — используется для инвалидации кэша рейтинга при новых отзывах.
	Cache cache.CacheStore
}

func NewReviewService(repo ReviewRepository) *ReviewService {
	return &ReviewService{repo: repo}
}

// WithCache устанавливает кэш для инвалидации рейтинга при создании отзыва.
func (s *ReviewService) WithCache(c cache.CacheStore) *ReviewService {
	s.Cache = c
	return s
}

func (s *ReviewService) CanReview(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.repo.CanReview(ctx, gameID, userID)
}

// Create создаёт отзыв. ctx — контекст запроса (A-10, pass 31).
func (s *ReviewService) Create(ctx context.Context, gameID, userID uint, rating int, comment string) error {
	// C-2: максимальный рейтинг 5 — согласовано с хендлером и UI.
	if rating < 1 || rating > 5 {
		return errors.New("рейтинг должен быть от 1 до 5")
	}
	can, err := s.CanReview(ctx, gameID, userID)
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
	created, err := s.repo.CreateIfNotExists(ctx, &review)
	if err != nil {
		return err
	}
	if !created {
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

	reviews, err := s.repo.ListByGame(ctx, gameID)
	if err != nil {
		return nil, err
	}

	if s.Cache != nil {
		s.Cache.SetWithCtx(ctx, cacheKey, reviews, 5*time.Minute)
	}
	return reviews, nil
}

func (s *ReviewService) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	return s.repo.GetAverageRating(ctx, gameID)
}
