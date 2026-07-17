// internal/domain/game/game_listing_service.go
package game

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
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
// Оптимизация: использует оконную функцию COUNT(*) OVER() для получения total в одном запросе.
func (s *GameListingService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	// Формируем базовый SQL с оконной функцией
	sql := `
		WITH filtered_games AS (
			SELECT games.*,
				COUNT(*) OVER() as total_count,
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

	args := []interface{}{filter.ViewerID, filter.ViewerID}

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

	sql += " ) SELECT *, total_count FROM filtered_games"

	// Определяем ORDER BY
	orderClause := "total_count DESC, games.created_at DESC"
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
			orderClause = "filtered_games.name " + sortDir + ", filtered_games.created_at DESC"
		case "starts_at":
			orderClause = "filtered_games.starts_at " + sortDir + ", filtered_games.created_at DESC"
		case "rating":
			orderClause = "filtered_games.rating_value " + sortDir + ", filtered_games.created_at DESC"
		case "participants":
			orderClause = "filtered_games.participant_count " + sortDir + ", filtered_games.created_at DESC"
		default:
			orderClause = "filtered_games.created_at " + sortDir
		}
	}

	sql += " ORDER BY " + orderClause

	// Пагинация
	offset := (page - 1) * perPage
	sql += fmt.Sprintf(" LIMIT %d OFFSET %d", perPage, offset)

	var games []Game
	var total int64

	// Выполняем один запрос с оконной функцией
	err := s.gameRepo.Model(ctx).Raw(sql, args...).Scan(&games).Error
	if err != nil {
		return nil, 0, err
	}

	// Total берём из первого результата (оконная функция)
	if len(games) > 0 {
		// Пытаемся получить total_count из сырых данных
		var result map[string]interface{}
		err = s.gameRepo.Model(ctx).Raw(sql, args...).Scan(&result).Error
		if err == nil {
			if tc, ok := result["total_count"]; ok {
				switch v := tc.(type) {
				case int64:
					total = v
				case float64:
					total = int64(v)
				}
			}
		}
	}

	// Fallback: если не удалось получить total из оконной функции, делаем отдельный COUNT
	if total == 0 && len(games) > 0 {
		countQuery := s.gameRepo.Model(ctx).Session(&gorm.Session{})
		countQuery = countQuery.Where("(visibility = 'public' OR author_id = ?) AND (is_draft = false OR author_id = ?)", filter.ViewerID, filter.ViewerID)
		switch filter.Status {
		case filterDraft:
			countQuery = countQuery.Where("is_draft = true AND author_id = ?", filter.ViewerID)
		case filterPublished:
			countQuery = countQuery.Where("is_draft = false")
		}
		if filter.Search != "" {
			escapedSearch := escapeLike(filter.Search)
			countQuery = countQuery.Where("name ILIKE ?", "%"+escapedSearch+"%")
		}
		if filter.DateFrom != "" {
			if dateFrom, err := time.Parse("2006-01-02", filter.DateFrom); err == nil {
				countQuery = countQuery.Where("starts_at >= ?", dateFrom)
			}
		}
		if filter.DateTo != "" {
			if dateTo, err := time.Parse("2006-01-02", filter.DateTo); err == nil {
				countQuery = countQuery.Where("starts_at < ?", dateTo.Add(24*time.Hour))
			}
		}
		if filter.AuthorID != nil {
			countQuery = countQuery.Where("author_id = ?", *filter.AuthorID)
		}
		countQuery.Count(&total)
	}

	return games, total, nil
}

// ListByDateRange возвращает опубликованные публичные игры за указанный период (для календаря).
func (s *GameListingService) ListByDateRange(ctx context.Context, from, to time.Time) ([]Game, error) {
	return s.gameRepo.ListByDateRange(ctx, from, to)
}
