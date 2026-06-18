// internal/domain/game/rating_service.go
package game

import (
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RatingService struct {
	DB *gorm.DB
}

func NewRatingService(db *gorm.DB) *RatingService {
	return &RatingService{DB: db}
}

func (s *RatingService) UpdateRatingsForGame(gameID uint) error {
	now := time.Now()

	var g Game
	if err := s.DB.Select("author_id").First(&g, gameID).Error; err != nil {
		return err
	}
	if err := s.awardPoints(g.AuthorID, 5, now); err != nil {
		log.Error().Err(err).Uint("user_id", g.AuthorID).Msg("failed to award author points")
	}

	var passings []GamePassing
	s.DB.Where("game_id = ? AND status = ?", gameID, StatusFinished).Find(&passings)

	for _, p := range passings {
		var userIDs []uint
		s.DB.Table("team_members").Where("team_id = ?", p.TeamID).Pluck("user_id", &userIDs)
		var captainID uint
		s.DB.Table("teams").Select("captain_id").Where("id = ?", p.TeamID).Scan(&captainID)
		userIDs = append(userIDs, captainID)
		userIDs = uniqueUintSlice(userIDs)

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
	return nil
}

func (s *RatingService) awardPoints(userID uint, points int, now time.Time) error {
	return s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"score":      gorm.Expr("player_ratings.score + ?", points),
			"updated_at": now,
		}),
	}).Create(&PlayerRating{UserID: userID, Score: points}).Error
}

func (s *RatingService) GetLeaderboard(limit int) ([]PlayerRating, error) {
	var ratings []PlayerRating
	err := s.DB.Preload("User").Order("score DESC").Limit(limit).Find(&ratings).Error
	return ratings, err
}

func uniqueUintSlice(input []uint) []uint {
	u := make([]uint, 0, len(input))
	m := make(map[uint]bool)
	for _, val := range input {
		if _, ok := m[val]; !ok {
			m[val] = true
			u = append(u, val)
		}
	}
	return u
}
