// internal/domain/game/service.go
package game

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/rs/zerolog/log"
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
}

// NewGameService создаёт фасад GameService с подсервисами.
func NewGameService(
	gameRepo GameRepository,
	passingRepo GamePassingRepository,
	ca *CoAuthorService,
	rs *ReviewService,
	ms *MonitorService,
	ps *PhotoService,
	hub *ws.RoomHub,
	cfg *config.Config,
	storage storage.FileStorage,
	cache cache.CacheStore,
	userRepo user.UserRepository,
	ratingSvc *RatingService,
) *GameService {
	crudSvc := NewGameCRUDService(gameRepo, ca, userRepo, ms)
	coverSvc := NewGameCoverService(gameRepo, storage, ca)
	listingSvc := NewGameListingService(gameRepo)

	return &GameService{
		crudService:    crudSvc,
		coverService:   coverSvc,
		listingService: listingSvc,
		reviewService:  rs,
		photoService:   ps,
		hub:            hub,
		cfg:            cfg,
		storage:        storage,
		cache:          cache,
		ratingService:  ratingSvc,
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

// GetByID возвращает игру по ID с кэшированием.
func (s *GameService) GetByID(ctx context.Context, id uint, viewerID uint) (*Game, error) {
	cacheKey := fmt.Sprintf("game:%d:viewer:%d", id, viewerID)

	if s.cache != nil {
		if cached, ok := s.cache.Get(cacheKey); ok {
			if game, ok := cached.(*Game); ok {
				canView, err := s.crudService.CanViewGame(ctx, game, viewerID)
				if err != nil {
					return nil, err
				}
				if !canView {
					if s.cache != nil {
						s.cache.Delete(cacheKey)
					}
					return nil, errors.New("игра не найдена")
				}
				log.Debug().Uint("game_id", id).Msg("GetByID: cache hit")
				return game, nil
			}
		}
	}

	game, err := s.crudService.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	ok, err := s.crudService.CanViewGame(ctx, game, viewerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("игра не найдена")
	}

	if s.cache != nil && (!game.IsDraft || s.crudService.hasAdminRole(ctx, viewerID) || ok) {
		ttl := 1 * time.Minute
		if !game.IsDraft {
			ttl = 5 * time.Minute
		}
		s.cache.Set(cacheKey, game, ttl)
	}

	return game, nil
}

// Update делегирует GameCRUDService.
func (s *GameService) Update(ctx context.Context, id uint, updated *Game, userID uint) error {
	err := s.crudService.Update(ctx, id, updated, userID)
	if err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Delete(fmt.Sprintf("game:%d", id))
		s.cache.DeleteByPrefix("games:list:")
	}
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
		photos, err := s.photoService.List(id)
		if err == nil {
			for _, photo := range photos {
				if delErr := s.storage.Delete(photo.Path); delErr != nil {
					log.Error().Err(delErr).Str("path", photo.Path).Msg("Delete: failed to delete photo file")
				}
			}
		}
	}

	err = s.crudService.Delete(ctx, id, userID)
	if err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Delete(fmt.Sprintf("game:%d", id))
		s.cache.DeleteByPrefix("games:list:")
	}
	return nil
}

// Publish делегирует GameCRUDService.
func (s *GameService) Publish(ctx context.Context, id uint, userID uint) error {
	err := s.crudService.Publish(ctx, id, userID)
	if err != nil {
		return err
	}
	if s.cache != nil {
		s.cache.Delete(fmt.Sprintf("game:%d", id))
		s.cache.DeleteByPrefix("games:list:")
	}
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
	return s.reviewService.ListByGame(gameID)
}

// GetAverageRating делегирует RatingService.
func (s *GameService) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	if s.reviewService == nil {
		return 0, 0, nil
	}

	cacheKey := fmt.Sprintf("rating:game:%d", gameID)

	if s.cache != nil {
		if cached, ok := s.cache.Get(cacheKey); ok {
			if result, ok := cached.(map[string]interface{}); ok {
				if avg, ok := result["avg"].(float64); ok {
					if count, ok := result["count"].(int64); ok {
						log.Debug().Uint("game_id", gameID).Msg("GetAverageRating: cache hit")
						return avg, count, nil
					}
				}
			}
		}
	}

	avg, count, err := s.reviewService.GetAverageRating(gameID)
	if err != nil {
		return 0, 0, err
	}

	if s.cache != nil {
		result := map[string]interface{}{
			"avg":   avg,
			"count": count,
		}
		s.cache.Set(cacheKey, result, 5*time.Minute)
	}

	return avg, count, nil
}

// IsUserManager делегирует CoAuthorService.
func (s *GameService) IsUserManager(gameID, userID uint) (bool, error) {
	if s.crudService == nil || s.crudService.CoAuthor == nil {
		return false, nil
	}
	return s.crudService.CoAuthor.IsUserManager(gameID, userID)
}
