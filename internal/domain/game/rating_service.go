// internal/domain/game/rating_service.go
package game

import (
	"time"

	"gorm.io/gorm"
)

type RatingService struct {
	DB *gorm.DB
}

func NewRatingService(db *gorm.DB) *RatingService {
	return &RatingService{DB: db}
}

func (s *RatingService) UpdateRatingsForGame(gameID uint) error {
	now := time.Now()

	// Очки автору
	s.DB.Exec(`
		INSERT INTO player_ratings (user_id, score, updated_at)
		SELECT author_id, 5, ?
		FROM games
		WHERE id = ?
		ON CONFLICT (user_id) DO UPDATE SET score = player_ratings.score + 5, updated_at = ?
	`, now, gameID, now)

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
			s.DB.Exec(`
				INSERT INTO player_ratings (user_id, score, updated_at)
				VALUES (?, ?, ?)
				ON CONFLICT (user_id) DO UPDATE SET score = player_ratings.score + ?, updated_at = ?
			`, uid, basePoints, now, basePoints, now)
		}
	}
	return nil
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