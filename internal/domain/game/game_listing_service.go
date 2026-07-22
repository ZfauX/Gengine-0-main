// internal/domain/game/game_listing_service.go
package game

import (
	"context"
	"strings"
	"time"
)

// escapeLike экранирует спецсимволы LIKE (% и _) для корректного поиска.
func escapeLike(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 10)
	for _, char := range s {
		switch char {
		case '%', '_':
			b.WriteByte(92)
			b.WriteRune(char)
		default:
			b.WriteRune(char)
		}
	}
	return b.String()
}

// GameListingService отвечает за списки игр, фильтрацию и сортировку.
type GameListingService struct {
	gameRepo GameRepository
}

// NewGameListingService создаёт новый сервис списков.
func NewGameListingService(gameRepo GameRepository) *GameListingService {
	return &GameListingService{gameRepo: gameRepo}
}

// ListFilteredPaginated возвращает список игр с фильтрацией и пагинацией.
// Использует один SQL-запрос с оконной функцией для total_count.
func (s *GameListingService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	// Формируем базовый SQL с оконной функцией
	sql := `
		SELECT games.*,
			(COALESCE(ratings.avg_rating, 0)) as rating_value,
			COALESCE(participants.participant_count, 0) as participant_count
		FROM games
		LEFT JOIN (
			SELECT game_id, COALESCE(AVG(rating), 0) as avg_rating
			FROM reviews GROUP BY game_id
		) ratings ON ratings.game_id = games.id
		LEFT JOIN (
			SELECT game_id, COUNT(DISTINCT team_id) as participant_count
			FROM game_passings WHERE status IN ('accepted','started','finished')
			GROUP BY game_id
		) participants ON participants.game_id = games.id
		WHERE (visibility = 'public' OR author_id = ?) AND (is_draft = false OR author_id = ?)`

	args := []any{filter.ViewerID, filter.ViewerID}

	switch filter.Status {
	case filterDraft:
		sql += " AND is_draft = true AND author_id = ?"
		args = append(args, filter.ViewerID)
	case filterPublished:
		sql += " AND is_draft = false"
	}

	if filter.Search != "" {
		escapedSearch := escapeLike(filter.Search)
		sql += " AND name ILIKE ?"
		args = append(args, "%"+escapedSearch+"%")
	}
	if filter.DateFrom != "" {
		if dateFrom, err := time.Parse("2006-01-02", filter.DateFrom); err == nil {
			sql += " AND starts_at >= ?"
			args = append(args, dateFrom)
		}
	}
	if filter.DateTo != "" {
		if dateTo, err := time.Parse("2006-01-02", filter.DateTo); err == nil {
			sql += " AND starts_at < ?"
			args = append(args, dateTo.Add(24*time.Hour))
		}
	}
	if filter.AuthorID != nil {
		sql += " AND author_id = ?"
		args = append(args, *filter.AuthorID)
	}

	// Определяем ORDER BY
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
			orderClause = "games.name " + sortDir + ", games.created_at DESC"
		case "starts_at":
			orderClause = "games.starts_at " + sortDir + ", games.created_at DESC"
		case "rating":
			orderClause = "rating_value " + sortDir + ", games.created_at DESC"
		case "participants":
			orderClause = "participant_count " + sortDir + ", games.created_at DESC"
		default:
			orderClause = "games.created_at " + sortDir
		}
	}

	sql += " ORDER BY " + orderClause

	// Один запрос с оконной функцией COUNT(*) OVER() для получения total и данных
	offset := (page - 1) * perPage
	sql += " LIMIT ? OFFSET ?"
	args = append(args, perPage, offset)

	// Используем подзапрос с оконной функцией для получения total_count
	windowSQL := `SELECT sub.*, COUNT(*) OVER() AS total_count FROM (` + sql + `) sub`

	type gameRow struct {
		Game
		TotalCount int64
	}
	var rows []gameRow
	if err := s.gameRepo.Model(ctx).Raw(windowSQL, args...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}

	var total int64
	var games []Game
	for _, row := range rows {
		if total == 0 {
			total = row.TotalCount
		}
		games = append(games, row.Game)
	}

	return games, total, nil
}

// ListByDateRange возвращает опубликованные публичные игры за указанный период (для календаря).
func (s *GameListingService) ListByDateRange(ctx context.Context, from, to time.Time) ([]Game, error) {
	return s.gameRepo.ListByDateRange(ctx, from, to)
}
