// internal/domain/game/service.go
package game

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/email"
	ws "gengine-0/internal/pkg/websocket"
)

// GameService содержит всю бизнес-логику, связанную с игрой и игровым процессом.
type GameService struct {
	DB              *gorm.DB
	CoAuthor        *CoAuthorService
	reviewService   *ReviewService
	monitorService  *MonitorService
	hub             *ws.RoomHub
	attemptService  *AttemptService
	progressService *LevelProgressService
	cfg             *config.Config
}

// NewGameService создаёт GameService со всеми зависимостями.
func NewGameService(
	db *gorm.DB,
	ca *CoAuthorService,
	rs *ReviewService,
	ms *MonitorService,
	hub *ws.RoomHub,
	attemptSvc *AttemptService,
	progressSvc *LevelProgressService,
	cfg *config.Config,
) *GameService {
	return &GameService{
		DB:              db,
		CoAuthor:        ca,
		reviewService:   rs,
		monitorService:  ms,
		hub:             hub,
		attemptService:  attemptSvc,
		progressService: progressSvc,
		cfg:             cfg,
	}
}

// ---------- CRUD игр ----------

// Create создаёт новую игру как черновик.
func (s *GameService) Create(game *Game, authorID uint) error {
	game.AuthorID = authorID
	game.IsDraft = true
	return s.DB.Create(game).Error
}

// GetByID возвращает игру по ID с учётом видимости и прав.
func (s *GameService) GetByID(id uint, viewerID uint) (*Game, error) {
	var g Game
	if err := s.DB.Preload("Author").Preload("GameSetting").First(&g, id).Error; err != nil {
		return nil, err
	}
	if g.IsDraft {
		isManager, _ := s.CoAuthor.IsUserManager(id, viewerID)
		if !isManager {
			var role string
			s.DB.Table("users").Select("role").Where("id = ?", viewerID).Scan(&role)
			if role != "admin" {
				return nil, errors.New("игра не найдена")
			}
		}
	}
	if g.Visibility == "private" {
		isManager, _ := s.CoAuthor.IsUserManager(id, viewerID)
		if !isManager {
			var role string
			s.DB.Table("users").Select("role").Where("id = ?", viewerID).Scan(&role)
			if role != "admin" {
				return nil, errors.New("игра не найдена")
			}
		}
	}
	return &g, nil
}

// ListFilteredPaginated возвращает игры с фильтрацией, сортировкой и пагинацией.
func (s *GameService) ListFilteredPaginated(filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	query := s.DB.Model(&Game{}).Preload("Author")
	query = query.Where("(visibility = 'public' OR author_id = ?) AND (is_draft = false OR author_id = ?)", filter.ViewerID, filter.ViewerID)

	switch filter.Status {
	case "draft":
		query = query.Where("is_draft = true AND author_id = ?", filter.ViewerID)
	case "published":
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

	var total int64
	query.Count(&total)

	orderClause := "games.created_at DESC"
	if sort != nil {
		col := "created_at"
		switch sort.Field {
		case "name":
			col = "name"
		case "starts_at":
			col = "starts_at"
		case "rating":
			col = "(SELECT COALESCE(AVG(r.rating), 0) FROM reviews r WHERE r.game_id = games.id)"
		case "participants":
			col = "(SELECT COUNT(DISTINCT gp.team_id) FROM game_passings gp WHERE gp.game_id = games.id AND gp.status IN ('accepted','started','finished'))"
		}
		direction := "ASC"
		if sort.Order == SortDesc {
			direction = "DESC"
		}
		orderClause = fmt.Sprintf("%s %s", col, direction)
	}
	query = query.Order(orderClause)

	offset := (page - 1) * perPage
	var games []Game
	if err := query.Offset(offset).Limit(perPage).Find(&games).Error; err != nil {
		return nil, 0, err
	}
	return games, total, nil
}

// Update обновляет игру, проверяя права (автор или контент-менеджер).
func (s *GameService) Update(id uint, updated *Game, userID uint) error {
	var game Game
	if err := s.DB.First(&game, id).Error; err != nil {
		return err
	}
	isManager, _ := s.CoAuthor.HasPermission(id, userID, "content")
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
	return s.DB.Save(&game).Error
}

// Publish публикует черновик.
func (s *GameService) Publish(id uint, userID uint) error {
	var game Game
	if err := s.DB.First(&game, id).Error; err != nil {
		return err
	}
	isManager, _ := s.CoAuthor.HasPermission(id, userID, "content")
	if !isManager {
		return errors.New("только автор или контент-менеджер может опубликовать игру")
	}
	if !game.IsDraft {
		return errors.New("игра уже опубликована")
	}
	var levelCount int64
	if err := s.DB.Model(&level.Level{}).Where("game_id = ?", id).Count(&levelCount).Error; err != nil {
		return err
	}
	if levelCount == 0 {
		return errors.New("нельзя опубликовать игру без уровней")
	}
	game.IsDraft = false
	return s.DB.Save(&game).Error
}

// Delete удаляет игру (только владелец).
func (s *GameService) Delete(id uint, userID uint) error {
	var game Game
	if err := s.DB.First(&game, id).Error; err != nil {
		return err
	}
	if game.AuthorID != userID {
		return errors.New("только владелец может удалить игру")
	}
	return s.DB.Delete(&game).Error
}

// ---------- Игровой процесс ----------

// SubmitCode – обработка ввода кода командой.
func (s *GameService) SubmitCode(passingID, userID uint, code string) (*Attempt, error) {
	progress, err := GetCurrentProgress(s.DB, passingID)
	if err != nil {
		return nil, err
	}

	attempt, success, err := s.attemptService.SubmitCode(progress, code)
	if err != nil {
		return nil, err
	}

	if success {
		if err := CompleteLevel(s.DB, progress); err != nil {
			return nil, err
		}
		s.logAndNotify(progress, "код принят", userID)
	} else {
		s.logAndNotify(progress, "неверный код", userID)
	}

	s.broadcastSnapshot(passingID)
	return attempt, nil
}

// SubmitFile – файловый ответ.
func (s *GameService) SubmitFile(passingID, userID uint, filePath string) (*Attempt, error) {
	progress, err := GetCurrentProgress(s.DB, passingID)
	if err != nil {
		return nil, err
	}

	attempt, err := s.attemptService.SubmitFile(progress, filePath)
	if err != nil {
		return nil, err
	}

	s.broadcastSnapshot(passingID)
	return attempt, nil
}

// UseHint – использование подсказки.
func (s *GameService) UseHint(passingID, userID uint) error {
	progress, err := GetCurrentProgress(s.DB, passingID)
	if err != nil {
		return err
	}

	var passing GamePassing
	if err := s.DB.First(&passing, passingID).Error; err != nil {
		return err
	}
	var settings GameSetting
	if err := s.DB.Where("game_id = ?", passing.GameID).First(&settings).Error; err != nil {
		settings = GameSetting{AllowHints: true, HintPenaltySeconds: 300, MaxHints: 3}
	}

	if !settings.AllowHints {
		return errors.New("подсказки запрещены")
	}
	if settings.MaxHints > 0 && progress.HintsUsed >= settings.MaxHints {
		return errors.New("лимит подсказок исчерпан")
	}

	progress.HintsUsed++
	penalty := settings.HintPenaltySeconds * progress.HintsUsed
	progress.PenaltySeconds += penalty
	if err := s.DB.Save(&progress).Error; err != nil {
		return err
	}

	s.logAndNotify(progress, fmt.Sprintf("использована подсказка (+%d сек)", penalty), userID)
	s.broadcastSnapshot(passingID)
	return nil
}

// AcceptBlackboxAnswer – подтверждение ответа автором.
func (s *GameService) AcceptBlackboxAnswer(passingID, userID uint) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		progress, err := GetCurrentProgress(tx, passingID)
		if err != nil {
			return err
		}

		var passing GamePassing
		if err := tx.First(&passing, passingID).Error; err != nil {
			return err
		}
		var g Game
		if err := tx.First(&g, passing.GameID).Error; err != nil {
			return err
		}
		if g.AuthorID != userID {
			return errors.New("только автор может подтвердить ответ")
		}

		if err := s.attemptService.AcceptPendingAttemptWithTx(tx, progress); err != nil {
			return err
		}
		if err := CompleteLevel(tx, progress); err != nil {
			return err
		}

		s.logAndNotify(progress, "автор принял ответ", userID)
		s.broadcastSnapshot(passingID)
		return nil
	})
}

// ForceFinishGame принудительно завершает игру.
func (s *GameService) ForceFinishGame(gameID uint) error {
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		var passings []GamePassing
		if err := tx.Where("game_id = ? AND status = ?", gameID, StatusStarted).Find(&passings).Error; err != nil {
			return err
		}
		if len(passings) == 0 {
			return errors.New("нет активных прохождений")
		}

		now := time.Now()
		for _, p := range passings {
			if err := finishPassingProgress(tx, &p, now); err != nil {
				return err
			}
			s.notifyCaptainAboutFinish(tx, p.TeamID, gameID)
		}

		return nil
	}); err != nil {
		return err
	}

	s.updateMonitorAndResults(gameID)
	return nil
}

// DisqualifyTeam дисквалифицирует команду.
func (s *GameService) DisqualifyTeam(gameID, teamID uint) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		var passing GamePassing
		if err := tx.Where("game_id = ? AND team_id = ? AND status = ?", gameID, teamID, StatusStarted).First(&passing).Error; err != nil {
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

		s.notifyCaptainAboutDisqualification(tx, teamID, gameID)
		s.updateMonitorAndResults(gameID)
		return nil
	})
}

// StartTesting создаёт тестовое прохождение.
func (s *GameService) StartTesting(gameID, userID uint) (*GamePassing, error) {
	var g Game
	if err := s.DB.First(&g, gameID).Error; err != nil {
		return nil, err
	}

	testTeam := team.Team{
		Name:      fmt.Sprintf("_test_%d", userID),
		CaptainID: userID,
	}
	if err := s.DB.Create(&testTeam).Error; err != nil {
		return nil, err
	}

	passing := GamePassing{
		GameID: gameID,
		TeamID: testTeam.ID,
		Status: StatusTesting,
	}
	if err := s.DB.Create(&passing).Error; err != nil {
		return nil, err
	}

	if err := s.progressService.InitFirstLevel(passing.ID); err != nil {
		return nil, err
	}
	return &passing, nil
}

// SubmitTestCode – ввод кода в тестовом режиме.
func (s *GameService) SubmitTestCode(passingID, userID uint, code string) (*Attempt, error) {
	progress, err := GetCurrentProgress(s.DB, passingID)
	if err != nil {
		return nil, err
	}

	attempt := &Attempt{
		LevelProgressID: progress.ID,
		Code:            code,
		Success:         true,
	}
	if err := s.DB.Create(attempt).Error; err != nil {
		return nil, err
	}

	if err := CompleteLevel(s.DB, progress); err != nil {
		return nil, err
	}
	s.broadcastSnapshot(passingID)
	return attempt, nil
}

// SkipLevelTest пропускает уровень в тестовом режиме.
func (s *GameService) SkipLevelTest(passingID, userID uint) error {
	progress, err := GetCurrentProgress(s.DB, passingID)
	if err != nil {
		return err
	}

	now := time.Now()
	progress.FinishedAt = &now
	if err := s.DB.Save(&progress).Error; err != nil {
		return err
	}
	return AdvanceToNextLevel(s.DB, passingID, progress.LevelID)
}

// DeleteLevelFromActiveGame – реализация интерфейса level.ActiveGameManager.
func (s *GameService) DeleteLevelFromActiveGame(gameID, levelID, userID uint) error {
	ok, err := s.CoAuthor.HasPermission(gameID, userID, "content")
	if err != nil || !ok {
		return errors.New("только автор или контент-менеджер может удалять уровни")
	}

	var lvl level.Level
	if err := s.DB.First(&lvl, levelID).Error; err != nil {
		return err
	}
	if lvl.DeletedAt.Valid {
		return errors.New("уровень уже удалён")
	}

	var passings []GamePassing
	s.DB.Where("game_id = ? AND status = ?", gameID, StatusStarted).Find(&passings)

	now := time.Now()
	for _, p := range passings {
		progress, err := GetCurrentProgress(s.DB, p.ID)
		if err != nil {
			log.Error().Uint("passing", p.ID).Err(err).Msg("DeleteLevelFromActiveGame: GetCurrentProgress error")
			continue
		}
		if progress.LevelID == levelID {
			progress.FinishedAt = &now
			if err := s.DB.Save(progress).Error; err != nil {
				log.Error().Uint("progress", progress.ID).Err(err).Msg("DeleteLevelFromActiveGame: Save progress error")
				continue
			}
			if err := AdvanceToNextLevel(s.DB, p.ID, levelID); err != nil {
				log.Error().Uint("passing", p.ID).Err(err).Msg("DeleteLevelFromActiveGame: AdvanceToNextLevel error")
			}
		}
	}

	if err := s.DB.Unscoped().Delete(&lvl).Error; err != nil {	
			return err
	}

	if s.monitorService != nil && s.hub != nil {
		snapshot, _ := s.monitorService.GameSnapshot(gameID)
		s.hub.BroadcastToRoom(strconv.Itoa(int(gameID)), snapshot)
	}
	return nil
}

// ---------- Вспомогательные методы ----------

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

func (s *GameService) notifyCaptainAboutFinish(tx *gorm.DB, teamID, gameID uint) {
	if s.cfg == nil {
		return
	}
	emailService := email.NewEmailService(s.cfg)
	var t team.Team
	if err := tx.First(&t, teamID).Error; err != nil {
		return
	}
	var captain user.User
	if err := tx.First(&captain, t.CaptainID).Error; err != nil {
		return
	}
	var g Game
	if err := tx.First(&g, gameID).Error; err != nil {
		return
	}
	if err := emailService.Send(captain.Email, "Игра завершена",
		fmt.Sprintf("Игра «%s» была принудительно завершена автором.", g.Name)); err != nil {
		log.Error().Err(err).Uint("game", gameID).Uint("team", teamID).Msg("notifyCaptainAboutFinish: failed to send email")
	}
}

func (s *GameService) notifyCaptainAboutDisqualification(tx *gorm.DB, teamID, gameID uint) {
	if s.cfg == nil {
		return
	}
	emailService := email.NewEmailService(s.cfg)
	var t team.Team
	if err := tx.First(&t, teamID).Error; err != nil {
		return
	}
	var captain user.User
	if err := tx.First(&captain, t.CaptainID).Error; err != nil {
		return
	}
	var g Game
	if err := tx.First(&g, gameID).Error; err != nil {
		return
	}
	if err := emailService.Send(captain.Email, "Дисквалификация",
		fmt.Sprintf("Ваша команда была дисквалифицирована в игре «%s».", g.Name)); err != nil {
		log.Error().Err(err).Uint("game", gameID).Uint("team", teamID).Msg("notifyCaptainAboutDisqualification: failed to send email")
	}
}

func (s *GameService) updateMonitorAndResults(gameID uint) {
	if s.monitorService != nil {
		s.monitorService.InvalidateCache(gameID)
		if err := s.monitorService.CalculateResults(gameID); err != nil {
			log.Error().Err(err).Uint("game", gameID).Msg("updateMonitorAndResults: CalculateResults error")
		}
		if s.hub != nil {
			snapshot, err := s.monitorService.GetOrFetchSnapshot(gameID)
			if err != nil {
				log.Error().Err(err).Uint("game", gameID).Msg("updateMonitorAndResults: GetOrFetchSnapshot error")
			} else {
				s.hub.BroadcastToRoom(strconv.Itoa(int(gameID)), snapshot)
			}
		}
	}
}

func (s *GameService) logAndNotify(progress *LevelProgress, message string, _ uint) {
	logEntry := Log{
		GamePassingID: progress.GamePassingID,
		LevelID:       progress.LevelID,
		Message:       message,
	}
	if err := s.DB.Create(&logEntry).Error; err != nil {
		log.Error().Err(err).Uint("passing", progress.GamePassingID).Uint("level", progress.LevelID).Msg("GameService.logAndNotify: failed to save log")
	}
}

func (s *GameService) broadcastSnapshot(passingID uint) {
	if s.monitorService == nil || s.hub == nil {
		return
	}
	var passing GamePassing
	if err := s.DB.Select("game_id").First(&passing, passingID).Error; err != nil {
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
	s.hub.BroadcastToRoom(strconv.Itoa(int(gameID)), snapshot)
}