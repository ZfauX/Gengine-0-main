// internal/domain/game/service.go
//
//go:generate go run go.uber.org/mock/mockgen -source=service.go -destination=mock_service.go -package=game
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
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Константы для фильтрации статусов игр (чтобы избежать магических строк)
const (
	filterDraft     = "draft"
	filterPublished = "published"
)

// allowedSortFields — белый список полей, по которым разрешена сортировка
var allowedSortFields = map[string]bool{
	"created_at":   true,
	"name":         true,
	"starts_at":    true,
	"rating":       true,
	"participants": true,
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
	hub            *ws.RoomHub
	cfg            *config.Config
	storage        storage.FileStorage
	cache          cache.CacheStore
	ratingService  *RatingService
	db             *gorm.DB
	coAuthorSvc    *CoAuthorService
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
	listingSvc := NewGameListingService(gameRepo)

	if cacheStore == nil {
		cacheStore = &cache.NoopCache{}
	}

	return &GameService{
		crudService:    crudSvc,
		coverService:   coverSvc,
		listingService: listingSvc,
		reviewService:  rs,
		photoService:   ps,
		hub:            hub,
		cfg:            cfg,
		storage:        storage,
		cache:          cacheStore,
		ratingService:  ratingSvc,
		db:             db,
		coAuthorSvc:    ca,
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
func (s *GameService) UpdateGameWithCover(ctx context.Context, gameID uint, dto *UpdateGameDTO, userID uint) error {
	return s.coverService.UpdateGameWithCover(ctx, gameID, dto, userID)
}

// Create делегирует GameCRUDService.
func (s *GameService) Create(ctx context.Context, game *Game, authorID uint) error {
	return s.crudService.Create(ctx, game, authorID)
}

// cacheGetGame пытается получить Game из кэша, поддерживая как in-memory (сохранение типа),
// так и Valkey (JSON → map[string]any → обратная конверсия через json.Marshal/Unmarshal).
func cacheGetGame(store cache.CacheStore, ctx context.Context, key string) (*Game, bool) {
	cached, ok := store.GetWithCtx(ctx, key)
	if !ok {
		return nil, false
	}
	switch v := cached.(type) {
	case *Game:
		return v, true
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

// GetByID возвращает игру по ID с кэшированием.
func (s *GameService) GetByID(ctx context.Context, id uint, viewerID uint) (*Game, error) {
	cacheKey := fmt.Sprintf("game:%d:viewer:%d", id, viewerID)

	if game, ok := cacheGetGame(s.cache, ctx, cacheKey); ok {
		canView, err := s.crudService.CanViewGame(ctx, game, viewerID, "user")
		if err != nil {
			return nil, err
		}
		if !canView {
			s.cache.DeleteWithCtx(ctx, cacheKey)
			return nil, errors.New("игра не найдена")
		}
		log.Debug().Uint("game_id", id).Msg("GetByID: cache hit")
		return game, nil
	}

	game, err := s.crudService.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	ok, err := s.crudService.CanViewGame(ctx, game, viewerID, "user")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("игра не найдена")
	}

	if !game.IsDraft {
		s.cache.SetWithCtx(ctx, cacheKey, game, 5*time.Minute)
	}

	return game, nil
}

// Update делегирует GameCRUDService.
func (s *GameService) Update(ctx context.Context, id uint, updated *Game, userID uint) error {
	err := s.crudService.Update(ctx, id, updated, userID)
	if err != nil {
		return err
	}
	s.cache.DeleteByPrefixWithCtx(ctx, fmt.Sprintf("game:%d:viewer:", id))
	s.cache.DeleteByPrefixWithCtx(ctx, "games:list:")
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

	if game.CoverPath != "" {
		if delErr := s.storage.Delete(game.CoverPath); delErr != nil {
			log.Error().Err(delErr).Str("path", game.CoverPath).Msg("Delete: failed to delete cover file")
		}
	}

	if s.photoService != nil {
		photos, listErr := s.photoService.List(ctx, id)
		if listErr == nil {
			// Параллельное удаление файлов с errgroup для корректной обработки ошибок
			var g errgroup.Group
			for _, photo := range photos {
				photoPath := photo.Path
				g.Go(func() error {
					if delErr := s.storage.Delete(photoPath); delErr != nil {
						log.Error().Err(delErr).Str("path", photoPath).Msg("Delete: failed to delete photo file")
						return delErr
					}
					return nil
				})
			}
			errspkg.LogSilently(g.Wait(), "Delete: parallel photo cleanup failed")
		}
	}

	deleteErr := s.crudService.Delete(ctx, id, userID)
	if deleteErr != nil {
		return deleteErr
	}
	s.cache.DeleteByPrefixWithCtx(ctx, fmt.Sprintf("game:%d:viewer:", id))
	s.cache.DeleteByPrefixWithCtx(ctx, "games:list:")
	return nil
}

// Publish делегирует GameCRUDService.
func (s *GameService) Publish(ctx context.Context, id uint, userID uint) error {
	err := s.crudService.Publish(ctx, id, userID)
	if err != nil {
		return err
	}
	s.cache.DeleteByPrefixWithCtx(ctx, fmt.Sprintf("game:%d:viewer:", id))
	s.cache.DeleteByPrefixWithCtx(ctx, "games:list:")
	return nil
}

// ListFilteredPaginated делегирует GameListingService.
func (s *GameService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	return s.listingService.ListFilteredPaginated(ctx, filter, sort, page, perPage)
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
		// Valkey path: JSON → map[string]any, handled above
		return 0, 0, false
	}
}

// GetAverageRating делегирует RatingService.
func (s *GameService) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	if s.reviewService == nil {
		return 0, 0, nil
	}

	cacheKey := fmt.Sprintf("rating:game:%d", gameID)

	if avg, count, ok := cacheGetRating(s.cache, ctx, cacheKey); ok {
		log.Debug().Uint("game_id", gameID).Msg("GetAverageRating: cache hit")
		return avg, count, nil
	}

	avgRating, count, err := s.ratingService.GetAverageRating(gameID)
	if err != nil {
		return 0, 0, err
	}

	s.cache.SetWithCtx(ctx, cacheKey, map[string]any{
		"avg":   avgRating,
		"count": count,
	}, 5*time.Minute)

	return avgRating, count, nil
}

// GetGameWithStats объединяет запросы: игра + отзывы + рейтинг (оптимизация Show).
func (s *GameService) GetGameWithStats(ctx context.Context, gameID uint) (*Game, []Review, float64, int64, error) {
	return s.crudService.GetGameWithStats(ctx, gameID)
}

// IsUserManager делегирует CoAuthorService.
func (s *GameService) IsUserManager(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.coAuthorSvc.IsUserManager(ctx, gameID, userID)
}

// GetPassingByUser возвращает активное passing для игры и пользователя.
func (s *GameService) GetPassingByUser(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	var passing GamePassing
	err := s.db.WithContext(ctx).
		Joins("JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.game_id = ? AND game_passings.status IN (?,?) AND team_members.user_id = ?",
			gameID, StatusAccepted, StatusStarted, userID).
		First(&passing).Error
	return &passing, err
}

// GetLogsByGameID возвращает логи игры, отсортированные по времени создания.
func (s *GameService) GetLogsByGameID(ctx context.Context, gameID uint) ([]Log, error) {
	var logs []Log
	err := s.db.WithContext(ctx).
		Joins("JOIN game_passings ON game_passings.id = logs.game_passing_id").
		Where("game_passings.game_id = ?", gameID).
		Order("logs.created_at ASC").
		Find(&logs).Error
	return logs, err
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
	var settings GameSetting
	err := s.db.WithContext(ctx).Where("game_id = ?", gameID).First(&settings).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &GameSetting{
				GameID:                   gameID,
				AllowHints:               true,
				HintPenaltySeconds:       300,
				MaxHints:                 3,
				PerLevelTimeLimit:        0,
				HideAnswersUntilFinished: false,
				AutoStart:                false,
			}, nil
		}
		return nil, err
	}
	return &settings, nil
}

// SaveSettings сохраняет или обновляет настройки игры.
func (s *GameService) SaveSettings(ctx context.Context, gameID uint, input GameSetting) (*GameSetting, error) {
	var result GameSetting
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing GameSetting
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_id = ?", gameID).First(&existing).Error
		if err == nil {
			existing.AllowHints = input.AllowHints
			existing.HintPenaltySeconds = input.HintPenaltySeconds
			existing.MaxHints = input.MaxHints
			existing.PerLevelTimeLimit = input.PerLevelTimeLimit
			existing.HideAnswersUntilFinished = input.HideAnswersUntilFinished
			existing.AutoStart = input.AutoStart
			result = existing
			return tx.Save(&existing).Error
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			newSettings := GameSetting{
				GameID:                   gameID,
				AllowHints:               input.AllowHints,
				HintPenaltySeconds:       input.HintPenaltySeconds,
				MaxHints:                 input.MaxHints,
				PerLevelTimeLimit:        input.PerLevelTimeLimit,
				HideAnswersUntilFinished: input.HideAnswersUntilFinished,
				AutoStart:                input.AutoStart,
			}
			result = newSettings
			return tx.Create(&newSettings).Error
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
