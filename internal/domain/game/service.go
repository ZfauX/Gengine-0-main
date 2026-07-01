// internal/domain/game/service.go
package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"slices"
	"strconv"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/metrics"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/rs/zerolog/log"
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

type GameService struct {
	gameRepo        GameRepository
	passingRepo     GamePassingRepository
	coAuthor        *CoAuthorService
	reviewService   *ReviewService
	monitorService  *MonitorService
	hub             *ws.RoomHub
	attemptService  *AttemptService
	progressService *LevelProgressService
	cfg             *config.Config
	storage         storage.FileStorage
	cache           *cache.Cache // кэш для игр и рейтингов
}

func NewGameService(
	gameRepo GameRepository,
	passingRepo GamePassingRepository,
	ca *CoAuthorService,
	rs *ReviewService,
	ms *MonitorService,
	hub *ws.RoomHub,
	attemptSvc *AttemptService,
	progressSvc *LevelProgressService,
	cfg *config.Config,
	storage storage.FileStorage,
	cache *cache.Cache,
) *GameService {
	return &GameService{
		gameRepo:        gameRepo,
		passingRepo:     passingRepo,
		coAuthor:        ca,
		reviewService:   rs,
		monitorService:  ms,
		hub:             hub,
		attemptService:  attemptSvc,
		progressService: progressSvc,
		cfg:             cfg,
		storage:         storage,
		cache:           cache,
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

	// Инвалидируем кэш игры при обновлении
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

func (s *GameService) getActiveGames(ctx context.Context) []Game {
	var games []Game
	if err := s.gameRepo.Model(ctx).Where("is_draft = false").Find(&games).Error; err != nil {
		log.Error().Err(err).Msg("getActiveGames: failed to fetch active games")
		return []Game{}
	}
	return games
}

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

// =============================================================================
// ВСПОМОГАТЕЛЬНАЯ ФУНКЦИЯ ДЛЯ ПРОВЕРКИ ЧЛЕНСТВА В КОМАНДЕ
// =============================================================================

// checkTeamMembership проверяет, является ли пользователь членом команды,
// связанной с прохождением. Используется внутри транзакций.
func checkTeamMembership(tx *gorm.DB, passingID, userID uint) error {
	var passing GamePassing
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&passing, passingID).Error; err != nil {
		return err
	}

	var count int64
	if err := tx.Table("team_members").Where("team_id = ? AND user_id = ?", passing.TeamID, userID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	// Проверяем капитана (капитан может не быть в team_members)
	var team team.Team
	if err := tx.First(&team, passing.TeamID).Error; err != nil {
		return err
	}
	if team.CaptainID == userID {
		return nil
	}

	return errors.New("вы не являетесь участником этой команды")
}

// =============================================================================
// МЕТОДЫ С ТРАНЗАКЦИЯМИ И БЛОКИРОВКАМИ
// =============================================================================

// SubmitCode обрабатывает отправку текстового кода с транзакцией и блокировкой.
func (s *GameService) SubmitCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error) {
	var attempt *Attempt

	err := s.gameRepo.DB(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Блокируем прогресс текущего уровня
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		// 2. Проверяем членство в команде
		if err := checkTeamMembership(tx, passingID, userID); err != nil {
			return err
		}

		// 3. Отправляем код через attemptService с передачей tx
		att, success, err := s.attemptService.SubmitCodeWithTx(tx, progress, code)
		if err != nil {
			return err
		}
		attempt = att

		if success {
			// 4. Завершаем уровень
			if err := CompleteLevel(tx, progress); err != nil {
				return err
			}
		}

		// 5. Сохраняем лог в БД (не критично, но для консистентности внутри транзакции)
		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("код %s: %s", code, map[bool]string{true: "принят", false: "неверный"}[success]),
		}
		return tx.Create(&logEntry).Error
	})

	if err != nil {
		return nil, err
	}

	// Отправляем WebSocket-обновления и уведомления после транзакции
	if attempt != nil {
		s.broadcastSnapshot(ctx, passingID)
	}

	return attempt, nil
}

// SubmitFile обрабатывает файловый ответ с транзакцией и блокировкой.
func (s *GameService) SubmitFile(ctx context.Context, passingID, userID uint, filePath string) (*Attempt, error) {
	var attempt *Attempt

	err := s.gameRepo.DB(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Блокируем прогресс текущего уровня
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		// 2. Проверяем членство в команде
		if err := checkTeamMembership(tx, passingID, userID); err != nil {
			return err
		}

		// 3. Создаём файловую попытку
		att, err := s.attemptService.SubmitFileWithTx(tx, progress, filePath)
		if err != nil {
			return err
		}
		attempt = att

		// 4. Для файловых ответов успех не определяется автоматически
		// (ожидается подтверждение автором или дальнейшая проверка)

		// 5. Логируем
		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("загружен файл: %s", filePath),
		}
		return tx.Create(&logEntry).Error
	})

	if err != nil {
		return nil, err
	}

	if attempt != nil {
		s.broadcastSnapshot(ctx, passingID)
	}
	return attempt, nil
}

// UseHint использует подсказку с транзакцией и блокировкой.
func (s *GameService) UseHint(ctx context.Context, passingID, userID uint) error {
	return s.gameRepo.DB(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Блокируем прогресс текущего уровня
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		// 2. Проверяем членство в команде
		if err := checkTeamMembership(tx, passingID, userID); err != nil {
			return err
		}

		// 3. Загружаем прохождение и настройки
		var passing GamePassing
		if err := tx.First(&passing, passingID).Error; err != nil {
			return err
		}
		var settings GameSetting
		if err := tx.Where("game_id = ?", passing.GameID).First(&settings).Error; err != nil {
			settings = GameSetting{AllowHints: true, HintPenaltySeconds: 300, MaxHints: 3}
		}

		if !settings.AllowHints {
			return errors.New("подсказки запрещены")
		}
		if settings.MaxHints > 0 && progress.HintsUsed >= settings.MaxHints {
			return errors.New("лимит подсказок исчерпан")
		}

		// 4. Применяем подсказку
		progress.HintsUsed++
		penalty := settings.HintPenaltySeconds * progress.HintsUsed
		progress.PenaltySeconds += penalty
		if err := tx.Save(progress).Error; err != nil {
			return err
		}

		// 5. Логируем
		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       fmt.Sprintf("использована подсказка (+%d сек)", penalty),
		}
		return tx.Create(&logEntry).Error
	})
}

// AcceptBlackboxAnswer подтверждает ответ на уровне "чёрный ящик" с транзакцией и блокировкой.
func (s *GameService) AcceptBlackboxAnswer(ctx context.Context, passingID, userID uint) error {
	return s.gameRepo.DB(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Блокируем прогресс текущего уровня
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		// 2. Проверяем, что пользователь — автор игры
		var passing GamePassing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&passing, passingID).Error; err != nil {
			return err
		}
		var g Game
		if err := tx.First(&g, passing.GameID).Error; err != nil {
			return err
		}
		if g.AuthorID != userID {
			return errors.New("только автор может подтвердить ответ")
		}

		// 3. Подтверждаем последнюю попытку
		if err := s.attemptService.AcceptPendingAttemptWithTx(tx, progress); err != nil {
			return err
		}

		// 4. Завершаем уровень
		if err := CompleteLevel(tx, progress); err != nil {
			return err
		}

		// 5. Логируем
		logEntry := Log{
			GamePassingID: passingID,
			LevelID:       progress.LevelID,
			Message:       "автор принял ответ",
		}
		return tx.Create(&logEntry).Error
	})
}

// ForceFinishGame принудительно завершает игру с транзакцией и блокировками.
func (s *GameService) ForceFinishGame(ctx context.Context, gameID uint) error {
	return s.gameRepo.DB(ctx).Transaction(func(tx *gorm.DB) error {
		// Блокируем все активные прохождения игры
		var passings []GamePassing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_id = ? AND status = ?", gameID, StatusStarted).
			Find(&passings).Error; err != nil {
			return err
		}
		if len(passings) == 0 {
			return errors.New("нет активных прохождений")
		}

		now := time.Now()
		for _, p := range passings {
			// Завершаем прогресс каждого прохождения
			if err := finishPassingProgress(tx, &p, now); err != nil {
				return err
			}
			// Отправляем уведомление капитану (может быть вне транзакции, но оставим здесь)
			s.notifyCaptainAboutFinish(ctx, tx, p.TeamID, gameID)
			metrics.IncGamePassings(string(StatusFinished))
			if !p.CreatedAt.IsZero() {
				duration := now.Sub(p.CreatedAt).Seconds()
				metrics.ObserveGameDuration(duration)
			}
		}
		return nil
	})
}

// DisqualifyTeam дисквалифицирует команду с транзакцией и блокировкой.
func (s *GameService) DisqualifyTeam(ctx context.Context, gameID, teamID uint) error {
	return s.gameRepo.DB(ctx).Transaction(func(tx *gorm.DB) error {
		// Блокируем прохождение команды
		var passing GamePassing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_id = ? AND team_id = ? AND status = ?", gameID, teamID, StatusStarted).
			First(&passing).Error; err != nil {
			return errors.New("команда не в игре или уже завершила")
		}

		now := time.Now()
		if err := finishPassingProgress(tx, &passing, now); err != nil {
			return err
		}

		passing.Status = StatusDisqualified
		if err := tx.Save(&passing).Error; err != nil {
			return err
		}
		metrics.IncGamePassings(string(StatusDisqualified))

		s.notifyCaptainAboutDisqualification(ctx, tx, teamID, gameID)
		return nil
	})
}

// StartTesting создаёт тестовое прохождение с транзакцией.
func (s *GameService) StartTesting(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	var passing *GamePassing

	err := s.gameRepo.DB(ctx).Transaction(func(tx *gorm.DB) error {
		// Создаём тестовую команду
		testTeam := team.Team{
			Name:      fmt.Sprintf("_test_%d", userID),
			CaptainID: userID,
		}
		if err := tx.Create(&testTeam).Error; err != nil {
			return err
		}

		// Создаём прохождение со статусом testing
		passing = &GamePassing{
			GameID: gameID,
			TeamID: testTeam.ID,
			Status: StatusTesting,
		}
		if err := tx.Create(passing).Error; err != nil {
			return err
		}
		metrics.IncGamePassings(string(StatusTesting))

		// Инициализируем первый уровень
		progressSvc := NewLevelProgressService(tx)
		return progressSvc.InitFirstLevel(ctx, passing.ID)
	})

	if err != nil {
		return nil, err
	}
	return passing, nil
}

// SubmitTestCode отправляет код в тестовом режиме с транзакцией.
func (s *GameService) SubmitTestCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error) {
	var attempt *Attempt

	err := s.gameRepo.DB(ctx).Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		// В тестовом режиме всегда успешно
		attempt = &Attempt{
			LevelProgressID: progress.ID,
			Code:            code,
			Success:         true,
		}
		if err := tx.Create(attempt).Error; err != nil {
			return err
		}

		// Завершаем уровень
		if err := CompleteLevel(tx, progress); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	s.broadcastSnapshot(ctx, passingID)
	return attempt, nil
}

// SkipLevelTest пропускает уровень в тестовом режиме с транзакцией.
func (s *GameService) SkipLevelTest(ctx context.Context, passingID, userID uint) error {
	return s.gameRepo.DB(ctx).Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgressForUpdate(tx, passingID)
		if err != nil {
			return err
		}

		now := time.Now()
		progress.FinishedAt = &now
		if err := tx.Save(progress).Error; err != nil {
			return err
		}
		return AdvanceToNextLevel(tx, passingID, progress.LevelID)
	})
}

// DeleteLevelFromActiveGame удаляет уровень из активной игры с транзакцией.
func (s *GameService) DeleteLevelFromActiveGame(ctx context.Context, gameID, levelID, userID uint) error {
	db := s.gameRepo.DB(ctx)
	ok, err := s.coAuthor.HasPermission(gameID, userID, "content")
	if err != nil {
		return fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может удалять уровни")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var lvl level.Level
		if err := tx.First(&lvl, levelID).Error; err != nil {
			return err
		}
		if lvl.DeletedAt.Valid {
			return errors.New("уровень уже удалён")
		}

		// Блокируем активные прохождения
		var passings []GamePassing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_id = ? AND status = ?", gameID, StatusStarted).
			Find(&passings).Error; err != nil {
			return err
		}

		now := time.Now()
		for _, p := range passings {
			progress, err := GetCurrentProgressForUpdate(tx, p.ID)
			if err != nil {
				log.Error().Uint("passing", p.ID).Err(err).Msg("DeleteLevelFromActiveGame: GetCurrentProgress error")
				continue
			}
			if progress.LevelID == levelID {
				progress.FinishedAt = &now
				if err := tx.Save(progress).Error; err != nil {
					log.Error().Uint("progress", progress.ID).Err(err).Msg("DeleteLevelFromActiveGame: Save progress error")
					continue
				}
				if err := AdvanceToNextLevel(tx, p.ID, levelID); err != nil {
					log.Error().Uint("passing", p.ID).Err(err).Msg("DeleteLevelFromActiveGame: AdvanceToNextLevel error")
				}
			}
		}

		// Удаляем прогресс для этого уровня
		if err := tx.Unscoped().Where("level_id = ?", levelID).Delete(&LevelProgress{}).Error; err != nil {
			return fmt.Errorf("ошибка удаления прогресса уровней: %w", err)
		}

		// Удаляем сам уровень
		if err := tx.Unscoped().Delete(&lvl).Error; err != nil {
			return fmt.Errorf("ошибка удаления уровня: %w", err)
		}
		return nil
	})
}

// =============================================================================
// ВСПОМОГАТЕЛЬНЫЕ МЕТОДЫ
// =============================================================================

func finishPassingProgress(tx *gorm.DB, passing *GamePassing, now time.Time) error {
	var progress LevelProgress
	err := tx.Where("game_passing_id = ? AND finished_at IS NULL", passing.ID).First(&progress).Error
	if err == nil {
		progress.FinishedAt = &now
		if err := tx.Save(&progress).Error; err != nil {
			return err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	passing.Status = StatusFinished
	return tx.Save(passing).Error
}

func (s *GameService) notifyCaptainAboutFinish(_ context.Context, tx *gorm.DB, teamID, gameID uint) {
	if s.cfg == nil || !s.cfg.SMTP.Enabled {
		return
	}
	emailService := email.NewEmailService(s.cfg)
	var t team.Team
	if err := tx.First(&t, teamID).Error; err != nil {
		log.Error().Err(err).Uint("team", teamID).Msg("notifyCaptainAboutFinish: failed to get team")
		return
	}
	var captain user.User
	if err := tx.First(&captain, t.CaptainID).Error; err != nil {
		log.Error().Err(err).Uint("captain", t.CaptainID).Msg("notifyCaptainAboutFinish: failed to get captain")
		return
	}
	var g Game
	if err := tx.First(&g, gameID).Error; err != nil {
		log.Error().Err(err).Uint("game", gameID).Msg("notifyCaptainAboutFinish: failed to get game")
		return
	}
	if err := emailService.Send(captain.Email, "Игра завершена",
		fmt.Sprintf("Игра «%s» была принудительно завершена автором.", g.Name)); err != nil {
		log.Error().Err(err).Uint("game", gameID).Uint("team", teamID).Msg("notifyCaptainAboutFinish: failed to send email")
	}
}

func (s *GameService) notifyCaptainAboutDisqualification(_ context.Context, tx *gorm.DB, teamID, gameID uint) {
	if s.cfg == nil || !s.cfg.SMTP.Enabled {
		return
	}
	emailService := email.NewEmailService(s.cfg)
	var t team.Team
	if err := tx.First(&t, teamID).Error; err != nil {
		log.Error().Err(err).Uint("team", teamID).Msg("notifyCaptainAboutDisqualification: failed to get team")
		return
	}
	var captain user.User
	if err := tx.First(&captain, t.CaptainID).Error; err != nil {
		log.Error().Err(err).Uint("captain", t.CaptainID).Msg("notifyCaptainAboutDisqualification: failed to get captain")
		return
	}
	var g Game
	if err := tx.First(&g, gameID).Error; err != nil {
		log.Error().Err(err).Uint("game", gameID).Msg("notifyCaptainAboutDisqualification: failed to get game")
		return
	}
	if err := emailService.Send(captain.Email, "Дисквалификация",
		fmt.Sprintf("Ваша команда была дисквалифицирована в игре «%s».", g.Name)); err != nil {
		log.Error().Err(err).Uint("game", gameID).Uint("team", teamID).Msg("notifyCaptainAboutDisqualification: failed to send email")
	}
}

func (s *GameService) broadcastSnapshot(ctx context.Context, passingID uint) {
	if s.monitorService == nil || s.hub == nil {
		return
	}
	var passing GamePassing
	if err := s.gameRepo.DB(ctx).Select("game_id").First(&passing, passingID).Error; err != nil {
		log.Error().Err(err).Uint("passing", passingID).Msg("GameService.broadcastSnapshot: failed to find passing")
		return
	}
	gameID := passing.GameID
	s.monitorService.InvalidateCache(gameID)
	snapshot, err := s.monitorService.GetOrFetchSnapshot(gameID)
	if err != nil {
		log.Error().Err(err).Uint("game", gameID).Msg("GameService.broadcastSnapshot: GetOrFetchSnapshot error")
		return
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		log.Error().Err(err).Uint("game", gameID).Msg("GameService.broadcastSnapshot: failed to marshal snapshot")
		return
	}
	s.hub.BroadcastToRoom(strconv.Itoa(int(gameID)), data)
}

// =============================================================================
// МЕТОДЫ ДЛЯ РАБОТЫ С ОТЗЫВАМИ (С КЭШИРОВАНИЕМ)
// =============================================================================

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
