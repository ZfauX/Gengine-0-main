// internal/domain/game/rating_service.go
package game

import (
	"context"
	"fmt"
	"time"

	"gengine-0/internal/pkg/cache"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RatingService struct {
	DB    *gorm.DB
	cache cache.CacheStore
}

func NewRatingService(db *gorm.DB, c cache.CacheStore) *RatingService {
	return &RatingService{DB: db, cache: c}
}

func (s *RatingService) UpdateRatingsForGame(ctx context.Context, gameID uint) error {
	now := time.Now()

	var g Game
	if err := s.DB.Select("author_id").First(&g, gameID).Error; err != nil {
		return err
	}
	if err := s.awardPoints(g.AuthorID, 5, now); err != nil {
		log.Error().Err(err).Uint("user_id", g.AuthorID).Msg("failed to award author points")
	}

	var passings []GamePassing
	if err := s.DB.Where("game_id = ? AND status = ?", gameID, StatusFinished).Find(&passings).Error; err != nil {
		log.Error().Err(err).Uint("game", gameID).Msg("UpdateRatingsForGame: failed to load passings")
		return nil
	}

	for _, p := range passings {
		type memberResult struct {
			UserID    uint
			CaptainID uint
		}
		var members []memberResult
		s.DB.Table("team_members").
			Select("team_members.user_id, teams.captain_id").
			Joins("JOIN teams ON teams.id = team_members.team_id").
			Where("team_members.team_id = ?", p.TeamID).
			Scan(&members)

		seen := make(map[uint]bool)
		var userIDs []uint
		for _, m := range members {
			if !seen[m.UserID] {
				seen[m.UserID] = true
				userIDs = append(userIDs, m.UserID)
			}
		}
		if len(members) > 0 && !seen[members[0].CaptainID] {
			userIDs = append(userIDs, members[0].CaptainID)
		}

		basePoints := 2
		if p.Place != nil {
			switch *p.Place {
			case 1:
				basePoints = 10
			case 2:
				basePoints = 7
			case 3:
				basePoints = 5
			}
		}
		for _, uid := range userIDs {
			if err := s.awardPoints(uid, basePoints, now); err != nil {
				log.Error().Err(err).Uint("user_id", uid).Msg("failed to award participant points")
			}
		}
	}

	// Инвалидируем кэш рейтинга после обновления
	if s.cache != nil {
		s.cache.DeleteByPrefixWithCtx(ctx, "leaderboard:")
	}
	return nil
}

func (s *RatingService) awardPoints(userID uint, points int, now time.Time) error {
	return s.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"score":      gorm.Expr("player_ratings.score + ?", points),
			"updated_at": now,
		}),
	}).Create(&PlayerRating{UserID: userID, Score: points}).Error
}

// GetLeaderboard возвращает топ игроков с кэшированием.
func (s *RatingService) GetLeaderboard(ctx context.Context, limit int) ([]PlayerRating, error) {
	cacheKey := fmt.Sprintf("leaderboard:limit:%d", limit)

	if s.cache != nil {
		if cached, ok := s.cache.GetWithCtx(ctx, cacheKey); ok {
			if ratings, ok := cached.([]PlayerRating); ok {
				log.Debug().Msg("GetLeaderboard: cache hit")
				return ratings, nil
			}
		}
	}

	var ratings []PlayerRating
	err := s.DB.Preload("User").Order("score DESC").Limit(limit).Find(&ratings).Error
	if err != nil {
		return nil, err
	}

	if s.cache != nil && len(ratings) > 0 {
		s.cache.SetWithCtx(ctx, cacheKey, ratings, 5*time.Minute)
	}

	return ratings, nil
}
