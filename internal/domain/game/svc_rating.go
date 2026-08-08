// internal/domain/game/rating_service.go
package game

import (
	"context"
	"fmt"
	"time"

	"gengine-0/internal/pkg/cache"

	"github.com/lib/pq"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RatingService struct {
	DB    *gorm.DB
	repo  RatingRepository
	cache cache.CacheStore
}

func NewRatingService(db *gorm.DB, c cache.CacheStore) *RatingService {
	if c == nil {
		c = &cache.NoopCache{}
	}
	return &RatingService{DB: db, cache: c}
}

// WithRepository устанавливает репозиторий чтения рейтинга (A-2, pass 31).
func (s *RatingService) WithRepository(repo RatingRepository) *RatingService {
	s.repo = repo
	return s
}

// repoOrDefault возвращает репозиторий или создаёт дефолтный на DB.
func (s *RatingService) repoOrDefault() RatingRepository {
	if s.repo == nil {
		return NewGormRatingRepo(s.DB)
	}
	return s.repo
}

func (s *RatingService) UpdateRatingsForGame(ctx context.Context, gameID uint) error {
	now := time.Now()

	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Advisory xact lock: сериализуем начисление по конкретной игре.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(gameID)).Error; err != nil {
			return fmt.Errorf("pg_advisory_xact_lock: %w", err)
		}

		var g Game
		if err := tx.Select("author_id").First(&g, gameID).Error; err != nil {
			return err
		}

		// Атомарный guard (B3): только первый вызов для игры начисляет очки.
		// RowsAffected==0 → уже начислено, повторный вызов ничего не делает.
		res := tx.Model(&Game{}).Where("id = ? AND rating_scored = false", gameID).Update("rating_scored", true)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}

		// Авторские очки начисляются один раз за игру (за создание).
		// Ошибка откатывает транзакцию вместе с rating_scored (C-H1) —
		// иначе игра останется помеченной без начисленных очков.
		if err := s.awardPointsWith(tx, g.AuthorID, 5, now); err != nil {
			log.Error().Err(err).Uint("user_id", g.AuthorID).Msg("failed to award author points")
			return err
		}

		var passings []GamePassing
		if err := tx.Where("game_id = ? AND status = ?", gameID, StatusFinished).Find(&passings).Error; err != nil {
			log.Error().Err(err).Uint("game", gameID).Msg("UpdateRatingsForGame: failed to load passings")
			return err
		}
		if len(passings) == 0 {
			s.cache.DeleteByPrefixWithCtx(ctx, "leaderboard")
			return nil
		}

		// Пункты за команду: место → базовые очки.
		teamIDs := make([]uint, 0, len(passings))
		teamPoints := make(map[uint]int, len(passings))
		for _, p := range passings {
			teamIDs = append(teamIDs, p.TeamID)
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
			teamPoints[p.TeamID] = basePoints
		}

		// Один запрос на всех участников всех команд вместо N+1.
		type memberResult struct {
			UserID    uint
			TeamID    uint
			CaptainID uint
		}
		var members []memberResult
		if err := tx.Table("team_members").
			Select("team_members.user_id, team_members.team_id, teams.captain_id").
			Joins("JOIN teams ON teams.id = team_members.team_id").
			Where("team_members.team_id IN ?", teamIDs).
			Scan(&members).Error; err != nil {
			log.Error().Err(err).Uint("game", gameID).Msg("UpdateRatingsForGame: team_members query failed")
			return err
		}

		// Капитаны одним запросом (команды могут не иметь строк в team_members).
		var teamRows []struct {
			ID        uint
			CaptainID uint
		}
		if err := tx.Table("teams").Select("id, captain_id").Where("id IN ?", teamIDs).Scan(&teamRows).Error; err != nil {
			log.Error().Err(err).Uint("game", gameID).Msg("UpdateRatingsForGame: teams query failed")
			return err
		}
		captainByTeam := make(map[uint]uint, len(teamRows))
		for _, t := range teamRows {
			captainByTeam[t.ID] = t.CaptainID
		}

		seen := make(map[uint]bool)
		var userIDs []uint
		var points []int
		addUser := func(uid uint, pts int) {
			if uid == 0 || seen[uid] {
				return
			}
			seen[uid] = true
			userIDs = append(userIDs, uid)
			points = append(points, pts)
		}

		for _, m := range members {
			addUser(m.UserID, teamPoints[m.TeamID])
		}
		for _, p := range passings {
			addUser(captainByTeam[p.TeamID], teamPoints[p.TeamID])
		}

		// Batch upsert одним запросом (unnest) вместо N отдельных INSERT (P6).
		if len(userIDs) > 0 {
			ts := make([]time.Time, len(userIDs))
			for i := range ts {
				ts[i] = now
			}
			if err := tx.Exec(`
				INSERT INTO player_ratings (user_id, score, updated_at)
				SELECT t.user_id, t.score, t.ts
				FROM unnest(?::bigint[], ?::int[], ?::timestamptz[]) AS t(user_id, score, ts)
				ON CONFLICT (user_id) DO UPDATE SET
					score = player_ratings.score + EXCLUDED.score,
					updated_at = EXCLUDED.updated_at
			`, pq.Array(userIDs), pq.Array(points), pq.Array(ts)).Error; err != nil {
				log.Error().Err(err).Uint("game", gameID).Msg("UpdateRatingsForGame: batch upsert failed")
				return err
			}
		}

		s.cache.DeleteByPrefixWithCtx(ctx, "leaderboard")
		return nil
	})
}

// awardPointsWith начисляет очки в рамках переданного tx/db.
func (s *RatingService) awardPointsWith(db *gorm.DB, userID uint, points int, now time.Time) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"score":      gorm.Expr("player_ratings.score + ?", points),
			"updated_at": now,
		}),
	}).Create(&PlayerRating{UserID: userID, Score: points}).Error
}

// LeaderboardEntry represents a player on the leaderboard with user details.
type LeaderboardEntry struct {
	UserID     uint
	Score      int
	Name       string
	AvatarPath string
}

// GetLeaderboard возвращает топ игроков с кэшированием.
func (s *RatingService) GetLeaderboard(ctx context.Context, limit int) ([]LeaderboardEntry, error) {
	cacheKey := fmt.Sprintf("leaderboard:limit:%d", limit)

	var cached []LeaderboardEntry
	if cacheGetJSON(s.cache, ctx, cacheKey, &cached) {
		log.Debug().Msg("GetLeaderboard: cache hit")
		return cached, nil
	}

	var entries []LeaderboardEntry
	entries, err := s.repoOrDefault().GetLeaderboard(ctx, limit)
	if err != nil {
		return nil, err
	}

	if len(entries) > 0 {
		s.cache.SetWithCtx(ctx, cacheKey, entries, 5*time.Minute)
	}

	return entries, nil
}

type avgRatingResult struct {
	AvgRating float64
	Count     int64
}

// GetAverageRating возвращает средний рейтинг и количество отзывов для игры.
// Кэшируется на 5 минут (rating:game:%d) — вызывается из GetStats/Show и статового API.
func (s *RatingService) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	cacheKey := fmt.Sprintf("rating:game:%d", gameID)
	if avg, count, ok := cacheGetRating(s.cache, ctx, cacheKey); ok {
		return avg, count, nil
	}

	var result avgRatingResult
	var err error
	result.AvgRating, result.Count, err = s.repoOrDefault().GetAverageRating(ctx, gameID)
	if err != nil {
		return 0, 0, err
	}
	s.cache.SetWithCtx(ctx, cacheKey, map[string]any{
		"avg":   result.AvgRating,
		"count": result.Count,
	}, 5*time.Minute)

	return result.AvgRating, result.Count, nil
}
