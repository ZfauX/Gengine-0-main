// internal/domain/game/game_listing_service.go
package game

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/sqlutil"

	"github.com/rs/zerolog/log"
)

// GameListingService отвечает за списки игр, фильтрацию и сортировку.
type GameListingService struct {
	gameRepo            GameRepository
	cache               cache.CacheStore
	searchVectorExists  bool
	searchVectorMu      sync.RWMutex
	searchVectorChecked bool
}

// NewGameListingService создаёт новый сервис списков.
func NewGameListingService(gameRepo GameRepository, cacheStore cache.CacheStore) *GameListingService {
	if cacheStore == nil {
		cacheStore = &cache.NoopCache{}
	}
	return &GameListingService{gameRepo: gameRepo, cache: cacheStore}
}

// listingCacheEntry — кэшированный результат анонимного листинга.
type listingCacheEntry struct {
	Games []Game
	Total int64
}

// listingCacheKey строит детерминированный ключ кэша по параметрам фильтра/сортировки.
func listingCacheKey(filter GameFilter, sort *GameSort, page, perPage int) string {
	field, order := "created_at", "desc"
	if sort != nil {
		field = sort.Field
		order = string(sort.Order)
	}
	authorID := uint(0)
	if filter.AuthorID != nil {
		authorID = *filter.AuthorID
	}
	return fmt.Sprintf("games:list:%d:%d:%s:%s:%s:%s:%d:%s:%s",
		page, perPage, filter.Status, filter.Search, filter.DateFrom, filter.DateTo, authorID, field, order)
}

// ListFilteredPaginated returns filtered games with pagination.
// Uses COUNT(*) OVER() window function to get total in a single query.
// Анонимный листинг (ViewerID == 0) кэшируется на 30с (P3).
func (s *GameListingService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	cacheKey := ""
	if filter.ViewerID == 0 {
		// Perf (pass 25): кэшируем только листинг без поиска/даты — иначе каждый
		// уникальный поисковый запрос создаёт ключ, фрагментируя LRU/Valkey.
		if filter.Search == "" && filter.DateFrom == "" && filter.DateTo == "" {
			cacheKey = listingCacheKey(filter, sort, page, perPage)
			var entry listingCacheEntry
			if cacheGetJSON(s.cache, ctx, cacheKey, &entry) {
				return entry.Games, entry.Total, nil
			}
		}
	}

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
		// C-1: не игнорируем ошибку count-запроса — иначе total=0 «нет игр» при сбое БД.
		if err := s.gameRepo.Model(ctx).Raw(countSQL, args...).Scan(&total).Error; err != nil {
			log.Error().Err(err).Msg("ListFilteredPaginated: count fallback failed")
			return nil, 0, err
		}
	}

	games := make([]Game, len(rows))
	for i, row := range rows {
		games[i] = row.Game
		if row.AuthorName != "" {
			games[i].Author = user.User{Name: row.AuthorName}
		}
	}
	if cacheKey != "" {
		s.cache.SetWithCtx(ctx, cacheKey, listingCacheEntry{Games: games, Total: total}, 30*time.Second)
	}
	return games, total, nil
}

// ListByDateRange возвращает опубликованные публичные игры за указанный период (для календаря).
func (s *GameListingService) ListByDateRange(ctx context.Context, from, to time.Time) ([]Game, error) {
	return s.gameRepo.ListByDateRange(ctx, from, to)
}

// AutocompleteItem — лёгкий результат автодополнения поиска игр.
type AutocompleteItem struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// AutocompleteSearch возвращает до limit опубликованных публичных игр по запросу
// (full-text + ILIKE fallback). Используется /api/search/games (C1 — без *gorm.DB в хендлере).
func (s *GameListingService) AutocompleteSearch(ctx context.Context, query string, limit int) ([]AutocompleteItem, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	items := []AutocompleteItem{}
	err := s.gameRepo.Model(ctx).
		Select("id, name").
		Where("is_draft = false AND visibility = 'public' AND (search_vector @@ plainto_tsquery('russian', ?) OR name ILIKE ?)",
			query, "%"+sqlutil.EscapeLike(query)+"%").
		Limit(limit).
		Find(&items).Error
	return items, err
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
