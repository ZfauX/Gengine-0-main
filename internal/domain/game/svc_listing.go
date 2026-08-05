// internal/domain/game/game_listing_service.go
package game

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/sqlutil"

	"github.com/rs/zerolog/log"
)

// GameListingService отвечает за списки игр, фильтрацию и сортировку.
type GameListingService struct {
	gameRepo            GameRepository
	searchVectorExists  bool
	searchVectorMu      sync.RWMutex
	searchVectorChecked bool
}

// NewGameListingService создаёт новый сервис списков.
func NewGameListingService(gameRepo GameRepository) *GameListingService {
	return &GameListingService{gameRepo: gameRepo}
}

// ListFilteredPaginated returns filtered games with pagination.
// Uses COUNT(*) OVER() window function to get total in a single query.
func (s *GameListingService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	var b strings.Builder
	b.Grow(1500)

	b.WriteString(`
		SELECT games.*,
			users.name as author__name,
			COUNT(*) OVER() AS total_count
		FROM games
		LEFT JOIN users ON users.id = games.author_id
		WHERE (games.visibility = 'public' OR games.author_id = ?) AND (games.is_draft = false OR games.author_id = ?)`)

	// Рейтинг и участники прекомпьютится в колонках games.rating_value /
	// games.participant_count (миграция 000027 + триггеры) — без агрегаций на каждый запрос (P3).
	args := []any{filter.ViewerID, filter.ViewerID}

	switch filter.Status {
	case filterDraft:
		b.WriteString(" AND games.is_draft = true AND games.author_id = ?")
		args = append(args, filter.ViewerID)
	case filterPublished:
		b.WriteString(" AND games.is_draft = false")
	default:
	}

	if filter.Search != "" {
		escapedSearch := sqlutil.EscapeLike(filter.Search)
		if s.useSearchVector(ctx) {
			b.WriteString(" AND (search_vector IS NOT NULL AND search_vector @@ plainto_tsquery('russian', ?) OR games.name ILIKE ? OR users.name ILIKE ?)")
			args = append(args, filter.Search, "%"+escapedSearch+"%", "%"+escapedSearch+"%")
		} else {
			b.WriteString(" AND (games.name ILIKE ? OR users.name ILIKE ?)")
			args = append(args, "%"+escapedSearch+"%", "%"+escapedSearch+"%")
		}
	}
	if filter.DateFrom != "" {
		if dateFrom, err := time.Parse("2006-01-02", filter.DateFrom); err == nil {
			b.WriteString(" AND games.starts_at >= ?")
			args = append(args, dateFrom)
		}
	}
	if filter.DateTo != "" {
		if dateTo, err := time.Parse("2006-01-02", filter.DateTo); err == nil {
			b.WriteString(" AND games.starts_at < ?")
			args = append(args, dateTo.Add(24*time.Hour))
		}
	}
	if filter.AuthorID != nil {
		b.WriteString(" AND games.author_id = ?")
		args = append(args, *filter.AuthorID)
	}

	// ORDER BY через белый список колонок (защита от SQL injection)
	orderClause := "games.created_at DESC"
	if sort != nil {
		sortDir := strings.ToUpper(string(sort.Order))
		if sortDir != "ASC" && sortDir != "DESC" {
			sortDir = "DESC"
		}
		var sortColumn string
		switch sort.Field {
		case "name":
			sortColumn = "games.name"
		case "starts_at":
			sortColumn = "games.starts_at"
		case "rating":
			sortColumn = "rating_value"
		case "participants":
			sortColumn = "participant_count"
		default:
			sortColumn = "games.created_at"
		}
		if sort.Field == "name" || sort.Field == "starts_at" {
			orderClause = sortColumn + " " + sortDir + ", games.created_at DESC"
		} else {
			orderClause = sortColumn + " " + sortDir
		}
	}

	// Сохраняем copy запроса до ORDER BY для возможного fallback-COUNT
	queryBeforeOrder := b.String()

	b.WriteString(" ORDER BY " + orderClause)

	offset := (page - 1) * perPage
	b.WriteString(" LIMIT " + strconv.Itoa(perPage) + " OFFSET " + strconv.Itoa(offset))
	query := b.String()

	type gameRow struct {
		Game
		AuthorName string `gorm:"column:author__name"`
		TotalCount int64
	}
	var rows []gameRow
	if err := s.gameRepo.Model(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}

	var total int64
	if len(rows) > 0 {
		total = rows[0].TotalCount
	} else {
		// Если страница пуста (за пределами данных), считаем total отдельным запросом
		// Безопасно: используем тот же query без ORDER BY/LIMIT/OFFSET
		countSQL := "SELECT COUNT(*) FROM (" + queryBeforeOrder + ") AS subq"
		_ = s.gameRepo.Model(ctx).Raw(countSQL, args...).Scan(&total)
	}

	games := make([]Game, len(rows))
	for i, row := range rows {
		games[i] = row.Game
		if row.AuthorName != "" {
			games[i].Author = user.User{Name: row.AuthorName}
		}
	}
	return games, total, nil
}

// ListByDateRange возвращает опубликованные публичные игры за указанный период (для календаря).
func (s *GameListingService) ListByDateRange(ctx context.Context, from, to time.Time) ([]Game, error) {
	return s.gameRepo.ListByDateRange(ctx, from, to)
}

// useSearchVector проверяет, существует ли столбец search_vector в таблице games.
// Результат кэшируется на время жизни сервиса.
func (s *GameListingService) useSearchVector(ctx context.Context) bool {
	s.searchVectorMu.RLock()
	if s.searchVectorChecked {
		exists := s.searchVectorExists
		s.searchVectorMu.RUnlock()
		return exists
	}
	s.searchVectorMu.RUnlock()

	s.searchVectorMu.Lock()
	defer s.searchVectorMu.Unlock()

	if s.searchVectorChecked {
		return s.searchVectorExists
	}

	var exists bool
	err := s.gameRepo.Model(ctx).
		Raw("SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='games' AND column_name='search_vector')").
		Scan(&exists).Error
	if err != nil || !exists {
		log.Warn().Err(err).Bool("exists", exists).Msg("GameListingService: search_vector column not found, falling back to ILIKE")
		s.searchVectorExists = false
	} else {
		s.searchVectorExists = true
	}
	s.searchVectorChecked = true
	return s.searchVectorExists
}

// ResetSearchVectorCheck сбрасывает кэш проверки search_vector (для тестов).
func (s *GameListingService) ResetSearchVectorCheck() {
	s.searchVectorMu.Lock()
	defer s.searchVectorMu.Unlock()
	s.searchVectorChecked = false
}
