// internal/domain/game/game_listing_service.go
package game

import (
	"context"
	"strings"
	"time"
)

// GameListingService отвечает за списки игр, фильтрацию и сортировку.
type GameListingService struct {
	gameRepo GameRepository
}

// NewGameListingService создаёт новый сервис списков.
func NewGameListingService(gameRepo GameRepository) *GameListingService {
	return &GameListingService{gameRepo: gameRepo}
}

// ListFilteredPaginated возвращает список игр с фильтрацией и пагинацией.
func (s *GameListingService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	query := s.gameRepo.Model(ctx).Preload("Author")
	query = query.Where("(visibility = 'public' OR author_id = ?) AND (is_draft = false OR author_id = ?)", filter.ViewerID, filter.ViewerID)

	switch filter.Status {
	case filterDraft:
		query = query.Where("is_draft = true AND author_id = ?", filter.ViewerID)
	case filterPublished:
		query = query.Where("is_draft = false")
	}

	if filter.Search != "" {
		query = query.Where("name ILIKE ?", "%"+filter.Search+"%")
	}
	if filter.DateFrom != "" {
		if dateFrom, err := time.Parse("2006-01-02", filter.DateFrom); err == nil {
			query = query.Where("starts_at >= ?", dateFrom)
		}
	}
	if filter.DateTo != "" {
		if dateTo, err := time.Parse("2006-01-02", filter.DateTo); err == nil {
			query = query.Where("starts_at < ?", dateTo.Add(24*time.Hour))
		}
	}
	if filter.AuthorID != nil {
		query = query.Where("author_id = ?", *filter.AuthorID)
	}

	total, err := s.gameRepo.Count(ctx, query)
	if err != nil {
		return nil, 0, err
	}

	orderClause := "games.created_at DESC"
	if sort != nil {
		field := sort.Field
		if !allowedSortFields[field] {
			field = "created_at"
		}

		sortDir := strings.ToUpper(string(sort.Order))
		if sortDir != "ASC" && sortDir != "DESC" {
			sortDir = "DESC"
		}

		switch field {
		case "name":
			orderClause = "games.name " + sortDir
		case "starts_at":
			orderClause = "games.starts_at " + sortDir
		case "rating":
			// Оптимизация: используем LEFT JOIN вместо подзапроса в ORDER BY
			query = query.Joins("LEFT JOIN (SELECT game_id, COALESCE(AVG(rating), 0) as avg_rating FROM reviews GROUP BY game_id) ratings ON ratings.game_id = games.id")
			orderClause = "ratings.avg_rating " + sortDir
		case "participants":
			// Оптимизация: используем LEFT JOIN вместо подзапроса в ORDER BY
			query = query.Joins("LEFT JOIN (SELECT game_id, COUNT(DISTINCT team_id) as participant_count FROM game_passings WHERE status IN ('accepted','started','finished') GROUP BY game_id) participants ON participants.game_id = games.id")
			orderClause = "participants.participant_count " + sortDir
		default:
			orderClause = "games.created_at " + sortDir
		}
	}
	query = query.Order(orderClause)

	offset := (page - 1) * perPage
	games, err := s.gameRepo.ListFiltered(ctx, query, offset, perPage)
	if err != nil {
		return nil, 0, err
	}

	return games, total, nil
}

// ListByDateRange возвращает опубликованные публичные игры за указанный период (для календаря).
func (s *GameListingService) ListByDateRange(ctx context.Context, from, to time.Time) ([]Game, error) {
	return s.gameRepo.ListByDateRange(ctx, from, to)
}
