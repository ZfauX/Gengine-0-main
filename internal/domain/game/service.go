// internal/domain/game/service.go
package game

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"slices"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/metrics"
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

// GameService отвечает за CRUD-операции с играми, публикацию, списки и работу с обложками.
type GameService struct {
	gameRepo       GameRepository
	passingRepo    GamePassingRepository
	coAuthor       *CoAuthorService
	reviewService  *ReviewService
	monitorService *MonitorService
	hub            *ws.RoomHub
	cfg            *config.Config
	storage        storage.FileStorage
	cache          *cache.Cache
}

func NewGameService(
	gameRepo GameRepository,
	passingRepo GamePassingRepository,
	ca *CoAuthorService,
	rs *ReviewService,
	ms *MonitorService,
	hub *ws.RoomHub,
	cfg *config.Config,
	storage storage.FileStorage,
	cache *cache.Cache,
) *GameService {
	return &GameService{
		gameRepo:       gameRepo,
		passingRepo:    passingRepo,
		coAuthor:       ca,
		reviewService:  rs,
		monitorService: ms,
		hub:            hub,
		cfg:            cfg,
		storage:        storage,
		cache:          cache,
	}
}

// hasAdminRole проверяет, является ли пользователь администратором.
func (s *GameService) hasAdminRole(ctx context.Context, userID uint) bool {
	if userID == 0 {
		return false
	}
	var role string
	err := s.gameRepo.DB(ctx).Table("users").Select("role").Where("id = ?", userID).Scan(&role).Error
	if err != nil {
		log.Warn().Err(err).Uint("user_id", userID).Msg("hasAdminRole: failed to fetch user role")
		return false
	}
	return role == "admin"
}

// canViewGame проверяет, имеет ли пользователь право видеть игру.
func (s *GameService) canViewGame(ctx context.Context, game *Game, viewerID uint) (bool, error) {
	if !game.IsDraft && game.Visibility != "private" {
		return true, nil
	}

	isManager, err := s.coAuthor.IsUserManager(game.ID, viewerID)
	if err != nil {
		return false, fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if isManager {
		return true, nil
	}

	if s.hasAdminRole(ctx, viewerID) {
		return true, nil
	}

	return false, nil
}

// CreateGameWithCover создаёт игру с загрузкой обложки.
func (s *GameService) CreateGameWithCover(ctx context.Context, dto *CreateGameDTO, authorID uint) (*Game, error) {
	game := &Game{
		Name:                 dto.Name,
		Description:          dto.Description,
		MaxTeamNumber:        dto.MaxTeamNumber,
		Visibility:           dto.Visibility,
		StartsAt:             dto.StartsAt,
		RegistrationDeadline: dto.RegistrationDeadline,
		IsDraft:              dto.IsDraft,
		AuthorID:             authorID,
	}

	if dto.CoverFile != nil {
		coverPath, err := s.saveCoverFile(dto.CoverFile, authorID)
		if err != nil {
			return nil, fmt.Errorf("не удалось сохранить обложку: %w", err)
		}
		game.CoverPath = coverPath
	}

	if err := s.gameRepo.Create(ctx, game); err != nil {
		if game.CoverPath != "" {
			if delErr := s.storage.Delete(game.CoverPath); delErr != nil {
				log.Error().Err(delErr).Str("path", game.CoverPath).Msg("CreateGameWithCover: failed to delete orphaned cover")
			}
		}
		return nil, err
	}

	metrics.IncGamesCreated()
	return game, nil
}

// UpdateGameWithCover обновляет игру с возможностью замены или удаления обложки.
func (s *GameService) UpdateGameWithCover(ctx context.Context, gameID uint, dto *UpdateGameDTO, userID uint) error {
	game, err := s.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		return err
	}

	isManager, err := s.coAuthor.HasPermission(gameID, userID, "content")
	if err != nil {
		return fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if !isManager {
		return errors.New("только автор или контент-менеджер может редактировать игру")
	}

	game.Name = dto.Name
	game.Description = dto.Description
	game.MaxTeamNumber = dto.MaxTeamNumber
	game.Visibility = dto.Visibility
	game.StartsAt = dto.StartsAt
	game.RegistrationDeadline = dto.RegistrationDeadline
	game.IsDraft = dto.IsDraft

	if dto.DeleteCover {
		if game.CoverPath != "" {
			if err := s.storage.Delete(game.CoverPath); err != nil {
				log.Error().Err(err).Str("path", game.CoverPath).Msg("UpdateGameWithCover: failed to delete cover")
			}
			game.CoverPath = ""
		}
	} else if dto.CoverFile != nil {
		newPath, err := s.saveCoverFile(dto.CoverFile, userID)
		if err != nil {
			return fmt.Errorf("не удалось сохранить новую обложку: %w", err)
		}
		if game.CoverPath != "" {
			if err := s.storage.Delete(game.CoverPath); err != nil {
				log.Error().Err(err).Str("path", game.CoverPath).Msg("UpdateGameWithCover: failed to delete old cover")
			}
		}
		game.CoverPath = newPath
	}

	if s.cache != nil {
		s.cache.Delete(fmt.Sprintf("game:%d", gameID))
	}

	return s.gameRepo.Update(ctx, game)
}

// saveCoverFile — внутренняя функция для загрузки файла обложки с проверками.
func (s *GameService) saveCoverFile(fileHeader *multipart.FileHeader, userID uint) (string, error) {
	file, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("не удалось открыть файл: %w", err)
	}
	defer func() { _ = file.Close() }()

	if fileHeader.Size > 5*1024*1024 {
		return "", errors.New("размер файла не должен превышать 5 МБ")
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
	contentType := fileHeader.Header.Get("Content-Type")
	if !slices.Contains(allowedTypes, contentType) {
		return "", errors.New("допустимы только JPEG, PNG и WebP")
	}

	webPath, err := s.storage.Save("uploads/covers", file, fileHeader.Filename, userID, 5*1024*1024, allowedTypes)
	if err != nil {
		return "", fmt.Errorf("ошибка сохранения обложки: %w", err)
	}
	return webPath, nil
}

// Create создаёт игру как черновик.
func (s *GameService) Create(ctx context.Context, game *Game, authorID uint) error {
	game.AuthorID = authorID
	game.IsDraft = true
	err := s.gameRepo.Create(ctx, game)
	if err == nil {
		metrics.IncGamesCreated()
	}
	return err
}

// GetByID возвращает игру по ID с кэшированием.
func (s *GameService) GetByID(ctx context.Context, id uint, viewerID uint) (*Game, error) {
	cacheKey := fmt.Sprintf("game:%d:viewer:%d", id, viewerID)

	if s.cache != nil {
		if cached, ok := s.cache.Get(cacheKey); ok {
			if game, ok := cached.(*Game); ok {
				log.Debug().Uint("game_id", id).Msg("GetByID: cache hit")
				return game, nil
			}
		}
	}

	game, err := s.gameRepo.GetByIDPreloaded(ctx, id)
	if err != nil {
		return nil, err
	}

	ok, err := s.canViewGame(ctx, game, viewerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("игра не найдена")
	}

	if s.cache != nil && (!game.IsDraft || s.hasAdminRole(ctx, viewerID) || ok) {
		s.cache.Set(cacheKey, game, 10*time.Second)
	}

	return game, nil
}

// ListFilteredPaginated возвращает список игр с фильтрацией и пагинацией.
// Использует триграммный поиск для ускорения поиска по названию (индекс gin_trgm_ops).
func (s *GameService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	cacheable := filter.Status == "published" && filter.Search == "" && filter.AuthorID == nil && filter.DateFrom == "" && filter.DateTo == ""

	if cacheable && s.cache != nil {
		cacheKey := fmt.Sprintf("games:list:status=%s:sort=%s:%s:page=%d:per=%d", filter.Status, sort.Field, sort.Order, page, perPage)
		if cached, ok := s.cache.Get(cacheKey); ok {
			if result, ok := cached.(map[string]interface{}); ok {
				if games, ok := result["games"].([]Game); ok {
					if total, ok := result["total"].(int64); ok {
						log.Debug().Msg("ListFilteredPaginated: cache hit")
						return games, total, nil
					}
				}
			}
		}
	}

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

		var col string
		switch field {
		case "name":
			col = "name"
		case "starts_at":
			col = "starts_at"
		case "rating":
			col = "(SELECT COALESCE(AVG(r.rating), 0) FROM reviews r WHERE r.game_id = games.id)"
		case "participants":
			col = "(SELECT COUNT(DISTINCT gp.team_id) FROM game_passings gp WHERE gp.game_id = games.id AND gp.status IN ('accepted','started','finished'))"
		default:
			col = "created_at"
		}

		direction := "ASC"
		if sort.Order == SortDesc {
			direction = "DESC"
		}
		orderClause = fmt.Sprintf("%s %s", col, direction)
	}
	query = query.Order(orderClause)

	offset := (page - 1) * perPage
	games, err := s.gameRepo.ListFiltered(ctx, query, offset, perPage)
	if err != nil {
		return nil, 0, err
	}

	if cacheable && s.cache != nil && len(games) > 0 {
		cacheKey := fmt.Sprintf("games:list:status=%s:sort=%s:%s:page=%d:per=%d", filter.Status, sort.Field, sort.Order, page, perPage)
		result := map[string]interface{}{
			"games": games,
			"total": total,
		}
		s.cache.Set(cacheKey, result, 30*time.Second)
	}

	return games, total, nil
}

// Update обновляет игру (только базовые поля, без обложки).
func (s *GameService) Update(ctx context.Context, id uint, updated *Game, userID uint) error {
	game, err := s.gameRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	isManager, err := s.coAuthor.HasPermission(id, userID, "content")
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

	if s.cache != nil {
		s.cache.Delete(fmt.Sprintf("game:%d", id))
		s.cache.Delete("games:list:*")
	}

	return s.gameRepo.Update(ctx, game)
}

// Publish публикует черновик игры.
func (s *GameService) Publish(ctx context.Context, id uint, userID uint) error {
	game, err := s.gameRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	isManager, err := s.coAuthor.HasPermission(id, userID, "content")
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
	metrics.SetActiveGames(float64(len(s.getActiveGames(ctx))))

	if s.cache != nil {
		s.cache.Delete(fmt.Sprintf("game:%d", id))
		s.cache.Delete("games:list:*")
	}
	return nil
}

// getActiveGames возвращает список опубликованных игр для обновления метрики.
func (s *GameService) getActiveGames(ctx context.Context) []Game {
	var games []Game
	if err := s.gameRepo.Model(ctx).Where("is_draft = false").Find(&games).Error; err != nil {
		log.Error().Err(err).Msg("getActiveGames: failed to fetch active games")
		return []Game{}
	}
	return games
}

// Delete удаляет игру (только владелец).
func (s *GameService) Delete(ctx context.Context, id uint, userID uint) error {
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
	if !game.IsDraft {
		metrics.SetActiveGames(float64(len(s.getActiveGames(ctx))))
	}

	if s.cache != nil {
		s.cache.Delete(fmt.Sprintf("game:%d", id))
		s.cache.Delete("games:list:*")
	}
	return nil
}

// ListReviews возвращает все отзывы для игры.
func (s *GameService) ListReviews(ctx context.Context, gameID uint) ([]Review, error) {
	if s.reviewService == nil {
		return []Review{}, nil
	}
	return s.reviewService.ListByGame(gameID)
}

// GetAverageRating возвращает средний рейтинг и количество отзывов с кэшированием.
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
