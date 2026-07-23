// internal/domain/game/review_service.go
package game

import (
	"context"
	"errors"

	"gengine-0/internal/pkg/sanitize"

	"gorm.io/gorm"
)

type ReviewService struct {
	DB *gorm.DB
}

func NewReviewService(db *gorm.DB) *ReviewService {
	return &ReviewService{DB: db}
}

func (s *ReviewService) CanReview(gameID, userID uint) (bool, error) {
	var count int64
	err := s.DB.Model(&GamePassing{}).
		Joins("JOIN teams ON teams.id = game_passings.team_id").
		Joins("LEFT JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.game_id = ? AND game_passings.status = ?", gameID, StatusFinished).
		Where("(team_members.user_id = ? OR teams.captain_id = ?)", userID, userID).
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

func (s *ReviewService) Create(gameID, userID uint, rating int, comment string) error {
	if rating < 1 || rating > 10 {
		return errors.New("рейтинг должен быть от 1 до 10")
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
	return s.DB.Create(&review).Error
}

func (s *ReviewService) ListByGame(ctx context.Context, gameID uint) ([]Review, error) {
	var reviews []Review
	err := s.DB.WithContext(ctx).Preload("User").Where("game_id = ?", gameID).Order("created_at DESC").Find(&reviews).Error
	return reviews, err
}

func (s *ReviewService) GetAverageRating(gameID uint) (float64, int64, error) {
	var result struct {
		Avg   float64
		Count int64
	}
	err := s.DB.Model(&Review{}).
		Where("game_id = ?", gameID).
		Select("COALESCE(AVG(rating), 0) as avg, COUNT(*) as count").
		Scan(&result).Error
	return result.Avg, result.Count, err
}
