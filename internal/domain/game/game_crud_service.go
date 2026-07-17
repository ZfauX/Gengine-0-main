// internal/domain/game/game_crud_service.go
package game

import (
	"context"
	"errors"
	"fmt"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/metrics"

	"github.com/rs/zerolog/log"
)

// GameCRUDService отвечает за базовые CRUD-операции с играми.
type GameCRUDService struct {
	gameRepo       GameRepository
	CoAuthor       *CoAuthorService
	userRepo       user.UserRepository
	monitorService *MonitorService
	reviewService  *ReviewService
	ratingService  *RatingService
}

// NewGameCRUDService создаёт новый сервис CRUD.
func NewGameCRUDService(
	gameRepo GameRepository,
	coAuthor *CoAuthorService,
	userRepo user.UserRepository,
	monitorService *MonitorService,
	reviewService *ReviewService,
	ratingService *RatingService,
) *GameCRUDService {
	return &GameCRUDService{
		gameRepo:       gameRepo,
		CoAuthor:       coAuthor,
		userRepo:       userRepo,
		monitorService: monitorService,
		reviewService:  reviewService,
		ratingService:  ratingService,
	}
}

// Create создаёт игру как черновик.
func (s *GameCRUDService) Create(ctx context.Context, game *Game, authorID uint) error {
	game.AuthorID = authorID
	game.IsDraft = true
	err := s.gameRepo.Create(ctx, game)
	if err == nil {
		metrics.IncGamesCreated()
	}
	return err
}

// GetByID возвращает игру по ID.
func (s *GameCRUDService) GetByID(ctx context.Context, id uint) (*Game, error) {
	return s.gameRepo.GetByIDPreloaded(ctx, id)
}

// Update обновляет базовые поля игры.
func (s *GameCRUDService) Update(ctx context.Context, id uint, updated *Game, userID uint) error {
	game, err := s.gameRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	isManager, err := s.CoAuthor.HasPermission(ctx, id, userID, RoleContentEditor)
	if err != nil {
		return fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if !isManager {
		return errors.New("только автор или контент-менеджер может редактировать игру")
	}
	game.Name = updated.Name
	game.Description = updated.Description
	game.StartsAt = updated.StartsAt
	game.RegistrationDeadline = updated.RegistrationDeadline
	game.MaxTeamNumber = updated.MaxTeamNumber
	game.Visibility = updated.Visibility
	game.CoverPath = updated.CoverPath

	return s.gameRepo.Update(ctx, game)
}

// Delete удаляет игру (только владелец).
func (s *GameCRUDService) Delete(ctx context.Context, id uint, userID uint) error {
	game, err := s.gameRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if game.AuthorID != userID {
		return errors.New("только владелец может удалить игру")
	}

	if err := s.gameRepo.Delete(ctx, id); err != nil {
		return err
	}
	metrics.IncGamesDeleted()
	return nil
}

// Publish публикует черновик игры.
func (s *GameCRUDService) Publish(ctx context.Context, id uint, userID uint) error {
	game, err := s.gameRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	isManager, err := s.CoAuthor.HasPermission(ctx, id, userID, RoleContentEditor)
	if err != nil {
		return fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if !isManager {
		return errors.New("только автор или контент-менеджер может опубликовать игру")
	}
	if !game.IsDraft {
		return errors.New("игра уже опубликована")
	}
	var levelCount int64
	if err := s.gameRepo.Model(ctx).Model(&level.Level{}).Where("game_id = ?", id).Count(&levelCount).Error; err != nil {
		return err
	}
	if levelCount == 0 {
		return errors.New("нельзя опубликовать игру без уровней")
	}
	game.IsDraft = false
	if err := s.gameRepo.Update(ctx, game); err != nil {
		return err
	}
	metrics.IncGamesPublished()
	metrics.SetActiveGames(float64(s.getActiveGames(ctx)))
	return nil
}

// getActiveGames возвращает количество опубликованных игр для обновления метрики.
func (s *GameCRUDService) getActiveGames(ctx context.Context) int64 {
	var count int64
	if err := s.gameRepo.Model(ctx).Where("is_draft = false").Count(&count).Error; err != nil {
		log.Error().Err(err).Msg("getActiveGames: failed to count active games")
		return 0
	}
	return count
}

// CanViewGame проверяет, имеет ли пользователь право видеть игру.
func (s *GameCRUDService) CanViewGame(ctx context.Context, game *Game, viewerID uint, userRole string) (bool, error) {
	if !game.IsDraft && game.Visibility != "private" {
		return true, nil
	}

	isManager, err := s.CoAuthor.IsUserManager(ctx, game.ID, viewerID)
	if err != nil {
		return false, fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if isManager {
		return true, nil
	}

	// Роль уже есть в JWT — читаем из параметра, а не из БД
	if userRole == "admin" {
		return true, nil
	}

	return false, nil
}

// GetGameWithStats объединяет запросы: игра + отзывы + рейтинг (оптимизация Show).
// Возвращает игру, список отзывов, средний рейтинг и количество отзывов.
func (s *GameCRUDService) GetGameWithStats(ctx context.Context, gameID uint) (*Game, []Review, float64, int64, error) {
	game, err := s.gameRepo.GetByIDPreloaded(ctx, gameID)
	if err != nil {
		return nil, nil, 0, 0, err
	}

	reviews := []Review{}
	avgRating := 0.0
	reviewsCount := int64(0)

	if s.reviewService != nil {
		reviews, err = s.reviewService.ListByGame(gameID)
		if err != nil {
			log.Warn().Err(err).Uint("game_id", gameID).Msg("GetGameWithStats: failed to list reviews")
		}
	}

	if s.ratingService != nil {
		avgRating, reviewsCount, err = s.ratingService.GetAverageRating(gameID)
		if err != nil {
			log.Warn().Err(err).Uint("game_id", gameID).Msg("GetGameWithStats: failed to get average rating")
		}
	}

	return game, reviews, avgRating, reviewsCount, nil
}

// ListReviews делегирует ReviewService.
func (s *GameCRUDService) ListReviews(ctx context.Context, gameID uint) ([]Review, error) {
	if s.reviewService == nil {
		return []Review{}, nil
	}
	return s.reviewService.ListByGame(gameID)
}

// GetAverageRating делегирует RatingService.
func (s *GameCRUDService) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	if s.ratingService == nil {
		return 0, 0, nil
	}
	return s.ratingService.GetAverageRating(gameID)
}
