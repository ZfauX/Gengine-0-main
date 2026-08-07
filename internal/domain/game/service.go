// internal/domain/game/service.go
//
// Note: mockgen may fail on anonymous interface params; tests use hand-rolled mocks
//
//go:generate go run go.uber.org/mock/mockgen -source=interfaces.go -destination=mock_service.go -package=game
package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/cache"
	errspkg "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Константы для фильтрации статусов игр (чтобы избежать магических строк)
const (
	filterDraft     = "draft"
	filterPublished = "published"
)

// ErrGameNotFound — игра не найдена либо недоступна для просмотра
// (несуществующая, приватная или черновик без прав). Позволяет хендлерам
// отличать 404 от внутренних ошибок без сравнения по err.Error().
var ErrGameNotFound = errors.New("игра не найдена")

// AllowedSortFields — допустимые поля для сортировки списка игр.
// Используется в svc_listing.go и тестами.
var AllowedSortFields = map[string]bool{
	"created_at": true, "name": true, "starts_at": true, "rating": true, "participants": true,
}

// CreateGameDTO — DTO для создания игры с обложкой.
type CreateGameDTO struct {
	Name                 string
	Description          string
	MaxTeamNumber        int
	Visibility           string
	StartsAt             *time.Time
	RegistrationDeadline *time.Time
	IsDraft              bool
	CoverFile            *multipart.FileHeader // файл обложки
}

// UpdateGameDTO — DTO для обновления игры с обложкой.
type UpdateGameDTO struct {
	Name                 string
	Description          string
	MaxTeamNumber        int
	Visibility           string
	StartsAt             *time.Time
	RegistrationDeadline *time.Time
	IsDraft              bool
	CoverFile            *multipart.FileHeader // новый файл обложки (если есть)
	DeleteCover          bool                  // флаг удаления существующей обложки
}

// GameService — фасад для подсервисов работы с играми.
// Делегирует вызовы GameCRUDService, GameCoverService, GameListingService.
type GameService struct {
	crudService    *GameCRUDService
	coverService   *GameCoverService
	listingService *GameListingService
	reviewService  *ReviewService
	photoService   *PhotoService
	storage        storage.FileStorage
	cache          cache.CacheStore
	ratingService  *RatingService
	gameRepo       GameRepository
	db             *gorm.DB
	coAuthorSvc    *CoAuthorService
	userRepo       user.UserRepository
	sg             singleflight.Group
}

// NewGameService создаёт фасад GameService с подсервисами.
func NewGameService(
	db *gorm.DB,
	gameRepo GameRepository,
	passingRepo GamePassingRepository,
	ca *CoAuthorService,
	rs *ReviewService,
	ms *MonitorService,
	ps *PhotoService,
	hub *ws.RoomHub,
	cfg *config.Config,
	storage storage.FileStorage,
	cacheStore cache.CacheStore,
	userRepo user.UserRepository,
	ratingSvc *RatingService,
) *GameService {
	crudSvc := NewGameCRUDService(gameRepo, ca, userRepo, ms, rs, ratingSvc)
	coverSvc := NewGameCoverService(gameRepo, storage, ca, int64(cfg.Server.MaxUploadSize))
	listingSvc := NewGameListingService(gameRepo, cacheStore)

	if cacheStore == nil {
		cacheStore = &cache.NoopCache{}
	}

	return &GameService{
		crudService:    crudSvc,
		coverService:   coverSvc,
		listingService: listingSvc,
		reviewService:  rs,
		photoService:   ps,
		storage:        storage,
		cache:          cacheStore,
		ratingService:  ratingSvc,
		gameRepo:       gameRepo,
		db:             db,
		coAuthorSvc:    ca,
		userRepo:       userRepo,
	}
}

// =============================================================================
// МЕТОДЫ ДЕЛЕГИРОВАНИЯ ПОДСЕРВИСАМ
// =============================================================================

// CreateGameWithCover делегирует GameCoverService.
func (s *GameService) CreateGameWithCover(ctx context.Context, dto *CreateGameDTO, authorID uint) (*Game, error) {
	return s.coverService.CreateGameWithCover(ctx, dto, authorID)
}

// UpdateGameWithCover делегирует GameCoverService.
func (s *GameService) UpdateGameWithCover(ctx context.Context, gameID uint, dto *UpdateGameDTO, userID uint, isAdmin bool) error {
	if err := s.coverService.UpdateGameWithCover(ctx, gameID, dto, userID, isAdmin); err != nil {
		return err
	}
	// Инвалидируем кэш игры — покрытие/поля игры изменились (P4).
	s.cache.DeleteWithCtx(ctx, fmt.Sprintf("game:%d", gameID))
	s.invalidateGameListCache(ctx)
	return nil
}

// Create делегирует GameCRUDService.
func (s *GameService) Create(ctx context.Context, game *Game, authorID uint) error {
	return s.crudService.Create(ctx, game, authorID)
}

// cacheGetGame пытается получить Game из кэша, поддерживая как in-memory (сохранение типа),
// так и Valkey (JSON → []byte → одна десериализация в Game, без map[string]any round-trip).
func cacheGetGame(store cache.CacheStore, ctx context.Context, key string) (*Game, bool) {
	// Valkey: сырые JSON-байты → одна десериализация (P9).
	if vc, ok := store.(*cache.ValkeyCache); ok {
		raw, ok := vc.GetBytesWithCtx(ctx, key)
		if !ok {
			return nil, false
		}
		var game Game
		if err := json.Unmarshal(raw, &game); err != nil {
			vc.DeleteWithCtx(ctx, key)
			return nil, false
		}
		return &game, true
	}

	// In-memory: кэш сохраняет тип *Game.
	cached, ok := store.GetWithCtx(ctx, key)
	if !ok {
		return nil, false
	}
	switch v := cached.(type) {
	case *Game:
		// Shallow-копия: защита от мутации кэшируемого объекта (B6).
		// Глубокий граф (Levels/Questions) остаётся общим — хендлеры не
		// должны мутировать элементы, но скалярные поля безопасны.
		g := *v
		return &g, true
	default:
		data, err := json.Marshal(cached)
		if err != nil {
			store.DeleteWithCtx(ctx, key)
			return nil, false
		}
		var game Game
		if err := json.Unmarshal(data, &game); err != nil {
			store.DeleteWithCtx(ctx, key)
			return nil, false
		}
		return &game, true
	}
}

// cacheGetJSON читает typed-значение из кэша (P-H1): для Valkey — raw bytes +
// json.Unmarshal (иначе type assertion против map[string]any всегда падает и
// кэш молча не хитнит); для in-memory — JSON round-trip через target.
// Set выполняется обычным SetWithCtx (который уже маршалит в JSON).
func cacheGetJSON(store cache.CacheStore, ctx context.Context, key string, target any) bool {
	if vc, ok := store.(*cache.ValkeyCache); ok {
		raw, ok := vc.GetBytesWithCtx(ctx, key)
		if !ok {
			return false
		}
		if err := json.Unmarshal(raw, target); err != nil {
			vc.DeleteWithCtx(ctx, key)
			return false
		}
		return true
	}
	cached, ok := store.GetWithCtx(ctx, key)
	if !ok {
		return false
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(data, target); err != nil {
		return false
	}
	return true
}

// cacheGetInt64 читает int64 из кэша (число хранится как JSON-число/строка в
// обоих реализациях). Используется для version-ключа листинга (PF3).
func cacheGetInt64(store cache.CacheStore, ctx context.Context, key string) (int64, bool) {
	cached, ok := store.GetWithCtx(ctx, key)
	if !ok {
		return 0, false
	}
	switch v := cached.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case string:
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}

// publicGame возвращает true, если игра публичная и не черновик — видимость
// такой игры не зависит от зрителя, поэтому CanViewGame (2 запроса) можно
// пропустить на самом горячем пути (PF-4, pass 29).
func publicGame(g *Game) bool {
	return g != nil && !g.IsDraft && g.Visibility != "private"
}

// GetByID возвращает игру по ID с кэшированием.
// isAdmin позволяет админам просматривать черновики и приватные игры, автором которых они не являются.
func (s *GameService) GetByID(ctx context.Context, id uint, viewerID uint, isAdmin bool) (*Game, error) {
	// Один ключ на игру (а не на пару игра+зритель): приватность проверяется
	// отдельно через CanViewGame, поэтому копию игры на каждого зрителя
	// хранить не нужно (P7).
	cacheKey := fmt.Sprintf("game:%d", id)

	role := "user"
	if isAdmin {
		role = "admin"
	}

	if game, ok := cacheGetGame(s.cache, ctx, cacheKey); ok {
		// PF-4: публичные не-draft игры видимы всем — без permission-запросов.
		if !publicGame(game) {
			canView, err := s.crudService.CanViewGame(ctx, game, viewerID, role)
			if err != nil {
				return nil, err
			}
			if !canView {
				s.cache.DeleteWithCtx(ctx, cacheKey)
				return nil, ErrGameNotFound
			}
		}
		log.Debug().Uint("game_id", id).Msg("GetByID: cache hit")
		return game, nil
	}

	// Кэш-промах: singleflight на загрузку из БД (P2) — при стампеде на
	// «горячую» игру только один запрос выполняет полный preload, остальные
	// ждут результат. CanViewGame проверяется вне singleflight (per-viewer).
	val, sfErr, _ := s.sg.Do(cacheKey, func() (any, error) {
		if game, ok := cacheGetGame(s.cache, ctx, cacheKey); ok {
			return game, nil
		}
		game, err := s.crudService.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if !game.IsDraft {
			s.cache.SetWithCtx(ctx, cacheKey, game, 5*time.Minute)
		}
		return game, nil
	})
	if sfErr != nil {
		return nil, sfErr
	}
	game, ok := val.(*Game)
	if !ok {
		return nil, errors.New("game: unexpected cached type")
	}

	// PF-4: публичные не-draft игры видимы всем — без permission-запросов.
	if !publicGame(game) {
		canView, err := s.crudService.CanViewGame(ctx, game, viewerID, role)
		if err != nil {
			return nil, err
		}
		if !canView {
			return nil, ErrGameNotFound
		}
	}
	return game, nil
}

// Update делегирует GameCRUDService.
func (s *GameService) Update(ctx context.Context, id uint, updated *Game, userID uint) error {
	err := s.crudService.Update(ctx, id, updated, userID)
	if err != nil {
		return err
	}
	s.cache.DeleteWithCtx(ctx, fmt.Sprintf("game:%d", id))
	return nil
}

// Delete делегирует GameCRUDService.
func (s *GameService) Delete(ctx context.Context, id uint, userID uint) error {
	game, err := s.crudService.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if game.AuthorID != userID {
		return errors.New("только владелец может удалить игру")
	}

	// Сначала строка в БД, затем best-effort файлы (B7): при сбое удаления
	// (например, FK) не оставляем игру с битой cover_path, а файлы — сиротами.
	deleteErr := s.crudService.Delete(ctx, id, userID)
	if deleteErr != nil {
		return deleteErr
	}
	s.invalidateGameCache(ctx, id)
	s.invalidateGameListCache(ctx)
	s.deleteGameFiles(ctx, id, game.CoverPath)
	return nil
}

// AdminDelete удаляет игру администратором: очищает обложку/фото с диска,
// инвалидирует кэш и удаляет игру без проверки авторства.
func (s *GameService) AdminDelete(ctx context.Context, id uint) error {
	game, err := s.crudService.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := s.crudService.Delete(ctx, id, game.AuthorID); err != nil {
		return err
	}
	s.invalidateGameCache(ctx, id)
	s.invalidateGameListCache(ctx)
	s.cache.DeleteWithCtx(ctx, fmt.Sprintf("rating:game:%d", id))
	// Файлы — после успешного удаления строки (B7).
	s.deleteGameFiles(ctx, id, game.CoverPath)
	return nil
}

// deleteGameFiles удаляет обложку и фото игры с диска (общий код Delete/AdminDelete, C2).
func (s *GameService) deleteGameFiles(ctx context.Context, id uint, coverPath string) {
	if coverPath != "" {
		if delErr := s.storage.Delete(coverPath); delErr != nil {
			log.Error().Err(delErr).Str("path", coverPath).Msg("deleteGameFiles: failed to delete cover file")
		}
	}

	if s.photoService == nil {
		return
	}
	photos, listErr := s.photoService.List(ctx, id)
	if listErr != nil {
		log.Warn().Err(listErr).Uint("game_id", id).Msg("deleteGameFiles: failed to list photos")
		return
	}
	// Параллельное удаление файлов с errgroup для корректной обработки ошибок.
	var g errgroup.Group
	for _, photo := range photos {
		photoPath := photo.Path
		g.Go(func() error {
			if delErr := s.storage.Delete(photoPath); delErr != nil {
				log.Error().Err(delErr).Str("path", photoPath).Msg("deleteGameFiles: failed to delete photo file")
				return delErr
			}
			return nil
		})
	}
	errspkg.LogSilently(g.Wait(), "deleteGameFiles: parallel photo cleanup failed")
}

// invalidateGameCache удаляет закэшированную игру по ключу game:%d.
func (s *GameService) invalidateGameCache(ctx context.Context, id uint) {
	s.cache.DeleteWithCtx(ctx, fmt.Sprintf("game:%d", id))
}

// invalidateGameListCache сбрасывает анонимный кэш листинга игр (P3/PF3):
// запись/публикация/удаление игры должна отражаться на списках немедленно.
// Версионный ключ (PF3): меняем ОДИН ключ games:list:version — старые
// games:list:vN:... ключи перестают читаться и истекают по TTL. Это O(1)
// вместо DeleteByPrefix (Valkey SCAN + DEL по всем ключам на каждый write).
func (s *GameService) invalidateGameListCache(ctx context.Context) {
	s.cache.SetWithCtx(ctx, "games:list:version", time.Now().UnixNano(), 24*time.Hour)
}

// Publish делегирует GameCRUDService.
func (s *GameService) Publish(ctx context.Context, id uint, userID uint) error {
	err := s.crudService.Publish(ctx, id, userID)
	if err != nil {
		return err
	}
	s.cache.DeleteWithCtx(ctx, fmt.Sprintf("game:%d", id))
	s.invalidateGameListCache(ctx)
	return nil
}

// ListFilteredPaginated делегирует GameListingService.
func (s *GameService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	return s.listingService.ListFilteredPaginated(ctx, filter, sort, page, perPage)
}

// GetUserGamesView возвращает предпочтение вида списка игр (U-3): сервер
// рендерит правильный вид сразу — без FOUC и ожидания /api/preferences.
func (s *GameService) GetUserGamesView(ctx context.Context, userID uint) string {
	if userID == 0 {
		return "table"
	}
	view, err := s.userRepo.GetGamesView(ctx, userID)
	if err != nil {
		return "table"
	}
	if view != "cards" {
		return "table"
	}
	return "cards"
}

// ListReviews делегирует ReviewService.
func (s *GameService) ListReviews(ctx context.Context, gameID uint) ([]Review, error) {
	if s.reviewService == nil {
		return []Review{}, nil
	}
	return s.reviewService.ListByGame(ctx, gameID)
}

// cacheGetRating пытается получить рейтинг из кэша, поддерживая как in-memory, так и Valkey.
func cacheGetRating(store cache.CacheStore, ctx context.Context, key string) (float64, int64, bool) {
	// Valkey: сырые JSON-байты → одна десериализация в struct (P9).
	if vc, ok := store.(*cache.ValkeyCache); ok {
		raw, ok := vc.GetBytesWithCtx(ctx, key)
		if !ok {
			return 0, 0, false
		}
		var rr struct {
			Avg   float64 `json:"avg"`
			Count int64   `json:"count"`
		}
		if err := json.Unmarshal(raw, &rr); err != nil {
			vc.DeleteWithCtx(ctx, key)
			return 0, 0, false
		}
		return rr.Avg, rr.Count, true
	}

	cached, ok := store.GetWithCtx(ctx, key)
	if !ok {
		return 0, 0, false
	}
	switch v := cached.(type) {
	case map[string]any:
		avg, avgOk := v["avg"].(float64)
		if !avgOk {
			return 0, 0, false
		}
		var count int64
		switch cv := v["count"].(type) {
		case int64:
			count = cv
		case float64:
			count = int64(cv)
		case int:
			count = int64(cv)
		default:
			return 0, 0, false
		}
		return avg, count, true
	default:
		return 0, 0, false
	}
}

// GetAverageRating делегирует RatingService (кэш на 5 мин внутри сервиса рейтинга).
func (s *GameService) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	if s.ratingService == nil {
		return 0, 0, nil
	}
	return s.ratingService.GetAverageRating(ctx, gameID)
}

// GetGameWithStats объединяет запросы: игра + отзывы + рейтинг (оптимизация Show).
func (s *GameService) GetGameWithStats(ctx context.Context, gameID uint) (*Game, []Review, float64, int64, error) {
	return s.crudService.GetGameWithStats(ctx, gameID)
}

// AutocompleteSearch делегирует GameListingService (для /api/search/games).
func (s *GameService) AutocompleteSearch(ctx context.Context, query string, limit int) ([]AutocompleteItem, error) {
	return s.listingService.AutocompleteSearch(ctx, query, limit)
}

// ShowGame возвращает данные для страницы игры за один вызов:
// игра (с проверкой прав и кэшем) + отзывы + рейтинг.
func (s *GameService) ShowGame(ctx context.Context, gameID, viewerID uint, isAdmin bool) (*Game, []Review, float64, int64, error) {
	game, err := s.GetByID(ctx, gameID, viewerID, isAdmin)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	reviews, avgRating, reviewsCount, err := s.crudService.GetStats(ctx, gameID)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	return game, reviews, avgRating, reviewsCount, nil
}

// IsUserManager делегирует CoAuthorService.
func (s *GameService) IsUserManager(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.coAuthorSvc.IsUserManager(ctx, gameID, userID)
}

// GetPassingByUser возвращает активное passing для игры и пользователя.
// ORDER BY id: при совпадении нескольких статусов выбираем детерминированно (B10).
func (s *GameService) GetPassingByUser(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	return s.gameRepo.GetPassingByUser(ctx, gameID, userID)
}

// GetFinishedPassingForTeam возвращает завершённое прохождение команды (для экспорта результатов).
func (s *GameService) GetFinishedPassingForTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error) {
	return s.gameRepo.GetFinishedPassingByGameAndTeam(ctx, gameID, teamID)
}

// IsTeamCaptain — является ли пользователь капитаном команды (для экспорта результатов).
func (s *GameService) IsTeamCaptain(ctx context.Context, teamID, userID uint) (bool, error) {
	return s.gameRepo.IsTeamCaptain(ctx, teamID, userID)
}

// GetGameByIDUnchecked возвращает игру без кэша и проверки прав (для проверки авторства при экспорте).
func (s *GameService) GetGameByIDUnchecked(ctx context.Context, gameID uint) (*Game, error) {
	return s.crudService.GetByID(ctx, gameID)
}

// GetLogsByGameID возвращает логи игры, отсортированные по времени создания.
func (s *GameService) GetLogsByGameID(ctx context.Context, gameID uint) ([]Log, error) {
	return s.gameRepo.GetLogsByGameID(ctx, gameID)
}

// GetLogsByGameIDPaginated возвращает страницу логов игры.
func (s *GameService) GetLogsByGameIDPaginated(ctx context.Context, gameID uint, page, pageSize int) ([]Log, int64, error) {
	var total int64
	db := s.db.WithContext(ctx).Session(&gorm.Session{NewDB: true})
	if err := db.Model(&Log{}).
		Joins("JOIN game_passings ON game_passings.id = logs.game_passing_id").
		Where("game_passings.game_id = ?", gameID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	} else if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize
	var logs []Log
	err := db.
		Joins("JOIN game_passings ON game_passings.id = logs.game_passing_id").
		Where("game_passings.game_id = ?", gameID).
		Order("logs.created_at ASC").
		Limit(pageSize).Offset(offset).
		Find(&logs).Error
	return logs, total, err
}

// GetSettingsWithDefaults загружает настройки игры или возвращает значения по умолчанию.
func (s *GameService) GetSettingsWithDefaults(ctx context.Context, gameID uint) (*GameSetting, error) {
	settings, err := s.gameRepo.GetGameSettingByGameID(ctx, gameID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return defaultGameSetting(gameID), nil
		}
		return nil, err
	}
	return settings, nil
}

// SaveSettings сохраняет или обновляет настройки игры.
func (s *GameService) SaveSettings(ctx context.Context, gameID uint, input GameSetting) (*GameSetting, error) {
	settings := GameSetting{
		GameID:                   gameID,
		AllowHints:               input.AllowHints,
		HintPenaltySeconds:       input.HintPenaltySeconds,
		MaxHints:                 input.MaxHints,
		PerLevelTimeLimit:        input.PerLevelTimeLimit,
		HideAnswersUntilFinished: input.HideAnswersUntilFinished,
		AutoStart:                input.AutoStart,
	}

	// Единый upsert (B4): update-then-insert имел гонку — два параллельных
	// первых сохранения оба видели 0 затронутых строк и оба делали Create.
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "game_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"allow_hints":                 settings.AllowHints,
			"hint_penalty_seconds":        settings.HintPenaltySeconds,
			"max_hints":                   settings.MaxHints,
			"per_level_time_limit":        settings.PerLevelTimeLimit,
			"hide_answers_until_finished": settings.HideAnswersUntilFinished,
			"auto_start":                  settings.AutoStart,
		}),
	}).Create(&settings).Error; err != nil {
		return nil, err
	}

	// Инвалидируем кэш game:%d — GameSetting входит в закэшированную игру (P4).
	s.cache.DeleteWithCtx(ctx, fmt.Sprintf("game:%d", gameID))
	s.invalidateGameListCache(ctx)

	return &settings, nil
}
