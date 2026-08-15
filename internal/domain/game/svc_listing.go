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
	"gorm.io/gorm"
)

// GameListingService отвечает за списки игр, фильтрацию и сортировку.
type GameListingService struct {
	gameRepo            GameRepository
	cache               cache.CacheStore
	searchVectorExists  bool
	searchVectorMu      sync.RWMutex
	searchVectorChecked bool

	// versionCache (M7, PASS-16): кэш текущей версии листинга в памяти.
	// Раньше каждый запрос листинга делал GET games:list:version в Valkey
	// (RTT) ПЛЮС GET самого кэша — 2 round-trip на каждый просмотр /games,
	// даже на HTML-кэш-хите. Теперь version читается из памяти и лишь
	// периодически сверяется с Valkey. Инвалидация version происходит через
	// SetWithCtx("games:list:version", ...) в других сервисах — короткий TTL
	// (200мс) гарантирует, что пишущий сервис увидит свежую версию быстро,
	// а читающие не бьют в Valkey на каждый запрос.
	versionMu    sync.RWMutex
	versionCache int64
	versionOK    bool
	versionRead  time.Time
}

// listingVersionTTL — как долго кэшировать version в памяти (M7).
const listingVersionTTL = 200 * time.Millisecond

// NewGameListingService создаёт новый сервис списков.
func NewGameListingService(gameRepo GameRepository, cacheStore cache.CacheStore) *GameListingService {
	if cacheStore == nil {
		cacheStore = &cache.NoopCache{}
	}
	return &GameListingService{gameRepo: gameRepo, cache: cacheStore}
}

// listingCacheEntry — кэшированный результат анонимного листинга.
// L9 (PASS-16): храним DTO GameCard, а не полные Game — в JSON не попадают
// nil-слайсы (Levels/Passings/Reviews/CoAuthors/Notes), Description,
// RegistrationDeadline и прочие поля, не нужные карточкам. Кэш листинга
// (Valkey) и память меньше примерно на порядок на запись.
type listingCacheEntry struct {
	Games []GameCard
	Total int64
}

// GameCard — DTO карточки игры в списке (L9). Поля, которые реально читает
// games-list.html (card/table view) + AuthorName для подписи автора.
type GameCard struct {
	ID               uint
	Name             string
	CoverPath        string
	StartsAt         *time.Time
	IsDraft          bool
	Visibility       string
	RatingValue      float64
	ParticipantCount int
	AuthorID         uint
	AuthorName       string
}

// toCard конвертирует Game (результат SQL с заполненным Author.Name) в DTO.
func (g Game) toCard() GameCard {
	return GameCard{
		ID:               g.ID,
		Name:             g.Name,
		CoverPath:        g.CoverPath,
		StartsAt:         g.StartsAt,
		IsDraft:          g.IsDraft,
		Visibility:       g.Visibility,
		RatingValue:      g.RatingValue,
		ParticipantCount: g.ParticipantCount,
		AuthorID:         g.AuthorID,
		AuthorName:       g.Author.Name,
	}
}

// toGame восстанавливает Game из DTO (для шаблонов, ожидающих .Author.Name).
func (c GameCard) toGame() Game {
	return Game{
		Model:            gorm.Model{ID: c.ID},
		Name:             c.Name,
		CoverPath:        c.CoverPath,
		StartsAt:         c.StartsAt,
		IsDraft:          c.IsDraft,
		Visibility:       c.Visibility,
		RatingValue:      c.RatingValue,
		ParticipantCount: c.ParticipantCount,
		AuthorID:         c.AuthorID,
		Author:           user.User{Name: c.AuthorName},
	}
}

// listingCacheKey строит детерминированный ключ кэша по параметрам фильтра/сортировки.
func (s *GameListingService) listingCacheKey(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) string {
	field, order := "created_at", "desc"
	if sort != nil {
		field = sort.Field
		order = string(sort.Order)
	}
	authorID := uint(0)
	if filter.AuthorID != nil {
		authorID = *filter.AuthorID
	}
	// PF3: версия листинга в ключе — при инвалидации меняем один ключ
	// games:list:version (O(1)) вместо DeleteByPrefix (Valkey SCAN+DEL).
	// DEEP-REVIEW P1 (pass 46): ViewerID включён в ключ — авторизованные
	// пользователи видят свои/публичные игры и не смешиваются в кэше.
	version := s.listingVersion(ctx)
	return fmt.Sprintf("games:list:v%d:%d:%d:%d:%s:%s:%s:%s:%d:%s:%s",
		version, filter.ViewerID, page, perPage, filter.Status, filter.Search, filter.DateFrom, filter.DateTo, authorID, field, order)
}

// listingVersion возвращает текущую версию анонимного листинга.
// 0 — если версия ещё не устанавливалась (первый запуск, кэш пуст).
// M7 (PASS-16): читаем из памяти (listingVersionTTL), а не из Valkey на
// каждый запрос — экономия 1 RTT на каждый листинг.
func (s *GameListingService) listingVersion(ctx context.Context) int64 {
	s.versionMu.RLock()
	valid := s.versionOK && time.Since(s.versionRead) < listingVersionTTL
	if valid {
		v := s.versionCache
		s.versionMu.RUnlock()
		return v
	}
	s.versionMu.RUnlock()

	v, ok := cacheGetInt64(s.cache, ctx, "games:list:version")
	if !ok {
		return 0
	}
	s.versionMu.Lock()
	s.versionCache = v
	s.versionOK = true
	s.versionRead = time.Now()
	s.versionMu.Unlock()
	return v
}

// ListFilteredPaginated returns filtered games with pagination.
// Uses COUNT(*) OVER() window function to get total in a single query.
// Анонимный листинг (ViewerID == 0) кэшируется на 30с (P3).
func (s *GameListingService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	cacheKey := ""
	// DEEP-REVIEW P1 (pass 46): кэшируем и авторизованный листинг (раньше только
	// анонимный — каждый залогиненный заход на /games бил в PostgreSQL).
	// Ключ включает ViewerID (listingCacheKey), поэтому «мои/публичные» игры
	// не смешиваются между пользователями. Без дат (самые горячие).
	// P-6 (PASS-15): кэш расширен на page 2..10 — пагинация анонимов больше
	// не бьёт в PostgreSQL на каждую страницу (версионный ключ инвалидируется).
	// L8 (PASS-16): поиск тоже кэшируется — listingCacheKey содержит filter.Search,
	// поэтому кэш уникален по запросу; раньше каждый анонимный поиск бил в PG.
	if page >= 1 && page <= 10 && filter.DateFrom == "" && filter.DateTo == "" {
		cacheKey = s.listingCacheKey(ctx, filter, sort, page, perPage)
		var entry listingCacheEntry
		if cacheGetJSON(s.cache, ctx, cacheKey, &entry) {
			games := make([]Game, len(entry.Games))
			for i, c := range entry.Games {
				games[i] = c.toGame()
			}
			return games, entry.Total, nil
		}
	}

	var b strings.Builder
	b.Grow(1500)

	// P-05 (pass 42): Select только колонок, нужных карточкам листинга —
	// раньше games.* тащил description (2000) + search_vector (tsvector) на
	// каждую строку страницы, в т.ч. для авторизованных (некэшируемых) списков.
	b.WriteString(`
		SELECT games.id, games.name, games.cover_path, games.starts_at, games.is_draft,
			games.visibility, games.rating_value, games.participant_count, games.author_id,
			users.name as author__name,
			COUNT(*) OVER() AS total_count
		FROM games
		LEFT JOIN users ON users.id = games.author_id
		WHERE games.deleted_at IS NULL AND `)

	// F1 (pass 31): анонимный листинг (ViewerID==0) — чистые предикаты без OR,
	// чтобы Postgres использовал составные индексы (idx_games_draft_visibility_*).
	// Для авторизованного — только авторские/публичные игры с OR.
	if filter.ViewerID == 0 {
		b.WriteString(`(games.visibility = 'public' AND games.is_draft = false)`)
	} else {
		b.WriteString(`(games.visibility = 'public' OR games.author_id = ?) AND (games.is_draft = false OR games.author_id = ?)`)
	}

	// Рейтинг и участники прекомпьютится в колонках games.rating_value /
	// games.participant_count (миграция 000027 + триггеры) — без агрегаций на каждый запрос (P3).
	args := []any{}
	if filter.ViewerID != 0 {
		args = append(args, filter.ViewerID, filter.ViewerID)
	}

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
	if err := s.gameRepo.RawScan(ctx, &rows, query, args...); err != nil {
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
		if err := s.gameRepo.RawScan(ctx, &total, countSQL, args...); err != nil {
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
		// L9 (PASS-16): в кэш пишем DTO-карточки (без лишних полей Game).
		cards := make([]GameCard, len(games))
		for i, g := range games {
			cards[i] = g.toCard()
		}
		s.cache.SetWithCtx(ctx, cacheKey, listingCacheEntry{Games: cards, Total: total}, 30*time.Second)
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
// DEEP-REVIEW P9 (pass 46): результат кэшируется на 60с — раньше каждое
// нажатие клавиши в поиске било в БД.
func (s *GameListingService) AutocompleteSearch(ctx context.Context, query string, limit int) ([]AutocompleteItem, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return []AutocompleteItem{}, nil
	}
	cacheKey := "games:autocomplete:" + q
	var cached []AutocompleteItem
	if s.cache != nil && cacheGetJSON(s.cache, ctx, cacheKey, &cached) {
		if len(cached) > limit {
			cached = cached[:limit]
		}
		return cached, nil
	}

	// A-H4 (pass 33): поиск перенесён в репозиторий (Autocomplete) — без
	// Model(ctx) в сервисе.
	games, err := s.gameRepo.Autocomplete(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	items := make([]AutocompleteItem, 0, len(games))
	for _, g := range games {
		items = append(items, AutocompleteItem{ID: g.ID, Name: g.Name})
	}
	if s.cache != nil {
		s.cache.SetWithCtx(ctx, cacheKey, items, 60*time.Second)
	}
	return items, nil
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

	exists, err := s.gameRepo.SearchVectorExists(ctx)
	if err != nil || !exists {
		log.Warn().Err(err).Bool("exists", exists).Msg("GameListingService: search_vector not available, falling back to ILIKE")
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
