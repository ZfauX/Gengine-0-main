// internal/domain/game/service.go
package game

import (
	"context"
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
	"gengine-0/internal/pkg/metrics"
	ws "gengine-0/internal/pkg/websocket"
)

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
	}
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

func (s *GameService) GetByID(ctx context.Context, id uint, viewerID uint) (*Game, error) {
	game, err := s.gameRepo.GetByIDPreloaded(ctx, id)
	if err != nil {
		return nil, err
	}
	if game.IsDraft {
		isManager, err := s.coAuthor.IsUserManager(id, viewerID)
		if err != nil {
			return nil, fmt.Errorf("ошибка проверки прав: %w", err)
		}
		if !isManager {
			var role string
			s.gameRepo.DB(ctx).Table("users").Select("role").Where("id = ?", viewerID).Scan(&role)
			if role != "admin" {
				return nil, errors.New("игра не найдена")
			}
		}
	}
	if game.Visibility == "private" {
		isManager, err := s.coAuthor.IsUserManager(id, viewerID)
		if err != nil {
			return nil, fmt.Errorf("ошибка проверки прав: %w", err)
		}
		if !isManager {
			var role string
			s.gameRepo.DB(ctx).Table("users").Select("role").Where("id = ?", viewerID).Scan(&role)
			if role != "admin" {
				return nil, errors.New("игра не найдена")
			}
		}
	}
	return game, nil
}

func (s *GameService) ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error) {
	query := s.gameRepo.Model(ctx).Preload("Author")
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

	total, err := s.gameRepo.Count(ctx, query)
	if err != nil {
		return nil, 0, err
	}

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
	games, err := s.gameRepo.ListFiltered(ctx, query, offset, perPage)
	if err != nil {
		return nil, 0, err
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
	return nil
}

func (s *GameService) getActiveGames(ctx context.Context) []Game {
	var games []Game
	s.gameRepo.Model(ctx).Where("is_draft = false").Find(&games)
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
	return nil
}

func (s *GameService) SubmitCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error) {
	db := s.gameRepo.DB(ctx)
	progress, err := GetCurrentProgress(db, passingID)
	if err != nil {
		return nil, err
	}

	attempt, success, err := s.attemptService.SubmitCode(progress, code)
	if err != nil {
		return nil, err
	}

	if success {
		if err := CompleteLevel(db, progress); err != nil {
			return nil, err
		}
		s.logAndNotify(ctx, db, progress, "код принят", userID)
	} else {
		s.logAndNotify(ctx, db, progress, "неверный код", userID)
	}

	s.broadcastSnapshot(ctx, passingID)
	return attempt, nil
}

func (s *GameService) SubmitFile(ctx context.Context, passingID, userID uint, filePath string) (*Attempt, error) {
	db := s.gameRepo.DB(ctx)
	progress, err := GetCurrentProgress(db, passingID)
	if err != nil {
		return nil, err
	}

	attempt, err := s.attemptService.SubmitFile(progress, filePath)
	if err != nil {
		return nil, err
	}

	s.broadcastSnapshot(ctx, passingID)
	return attempt, nil
}

func (s *GameService) UseHint(ctx context.Context, passingID, userID uint) error {
	db := s.gameRepo.DB(ctx)
	progress, err := GetCurrentProgress(db, passingID)
	if err != nil {
		return err
	}

	var passing GamePassing
	if err := db.First(&passing, passingID).Error; err != nil {
		return err
	}
	var settings GameSetting
	if err := db.Where("game_id = ?", passing.GameID).First(&settings).Error; err != nil {
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
	if err := db.Save(&progress).Error; err != nil {
		return err
	}

	s.logAndNotify(ctx, db, progress, fmt.Sprintf("использована подсказка (+%d сек)", penalty), userID)
	s.broadcastSnapshot(ctx, passingID)
	return nil
}

func (s *GameService) AcceptBlackboxAnswer(ctx context.Context, passingID, userID uint) error {
	db := s.gameRepo.DB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
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

		s.logAndNotify(ctx, tx, progress, "автор принял ответ", userID)
		s.broadcastSnapshot(ctx, passingID)
		return nil
	})
}

func (s *GameService) ForceFinishGame(ctx context.Context, gameID uint) error {
	db := s.gameRepo.DB(ctx)
	if err := db.Transaction(func(tx *gorm.DB) error {
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
			s.notifyCaptainAboutFinish(ctx, tx, p.TeamID, gameID)
			metrics.IncGamePassings("finished")
			// Используем CreatedAt как время начала прохождения
			if !p.CreatedAt.IsZero() {
				duration := now.Sub(p.CreatedAt).Seconds()
				metrics.ObserveGameDuration(duration)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	s.updateMonitorAndResults(ctx, gameID)
	return nil
}

func (s *GameService) DisqualifyTeam(ctx context.Context, gameID, teamID uint) error {
	db := s.gameRepo.DB(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
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
		metrics.IncGamePassings("disqualified")

		s.notifyCaptainAboutDisqualification(ctx, tx, teamID, gameID)
		s.updateMonitorAndResults(ctx, gameID)
		return nil
	})
}

func (s *GameService) StartTesting(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	db := s.gameRepo.DB(ctx)
	testTeam := team.Team{
		Name:      fmt.Sprintf("_test_%d", userID),
		CaptainID: userID,
	}
	if err := db.Create(&testTeam).Error; err != nil {
		return nil, err
	}

	passing := GamePassing{
		GameID: gameID,
		TeamID: testTeam.ID,
		Status: StatusTesting,
	}
	if err := s.passingRepo.Create(ctx, &passing); err != nil {
		return nil, err
	}
	metrics.IncGamePassings("testing")

	if err := s.progressService.InitFirstLevel(ctx, passing.ID); err != nil {
		return nil, err
	}
	return &passing, nil
}

func (s *GameService) SubmitTestCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error) {
	db := s.gameRepo.DB(ctx)
	progress, err := GetCurrentProgress(db, passingID)
	if err != nil {
		return nil, err
	}

	attempt := &Attempt{
		LevelProgressID: progress.ID,
		Code:            code,
		Success:         true,
	}
	if err := db.Create(attempt).Error; err != nil {
		return nil, err
	}

	if err := CompleteLevel(db, progress); err != nil {
		return nil, err
	}
	s.broadcastSnapshot(ctx, passingID)
	return attempt, nil
}

func (s *GameService) SkipLevelTest(ctx context.Context, passingID, userID uint) error {
	db := s.gameRepo.DB(ctx)
	progress, err := GetCurrentProgress(db, passingID)
	if err != nil {
		return err
	}

	now := time.Now()
	progress.FinishedAt = &now
	if err := db.Save(&progress).Error; err != nil {
		return err
	}
	return AdvanceToNextLevel(db, passingID, progress.LevelID)
}

func (s *GameService) DeleteLevelFromActiveGame(ctx context.Context, gameID, levelID, userID uint) error {
	db := s.gameRepo.DB(ctx)
	ok, err := s.coAuthor.HasPermission(gameID, userID, "content")
	if err != nil {
		return fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if !ok {
		return errors.New("только автор или контент-менеджер может удалять уровни")
	}

	var lvl level.Level
	if err := db.First(&lvl, levelID).Error; err != nil {
		return err
	}
	if lvl.DeletedAt.Valid {
		return errors.New("уровень уже удалён")
	}

	var passings []GamePassing
	db.Where("game_id = ? AND status = ?", gameID, StatusStarted).Find(&passings)

	now := time.Now()
	for _, p := range passings {
		progress, err := GetCurrentProgress(db, p.ID)
		if err != nil {
			log.Error().Uint("passing", p.ID).Err(err).Msg("DeleteLevelFromActiveGame: GetCurrentProgress error")
			continue
		}
		if progress.LevelID == levelID {
			progress.FinishedAt = &now
			if err := db.Save(progress).Error; err != nil {
				log.Error().Uint("progress", progress.ID).Err(err).Msg("DeleteLevelFromActiveGame: Save progress error")
				continue
			}
			if err := AdvanceToNextLevel(db, p.ID, levelID); err != nil {
				log.Error().Uint("passing", p.ID).Err(err).Msg("DeleteLevelFromActiveGame: AdvanceToNextLevel error")
			}
		}
	}

	if err := db.Unscoped().Where("level_id = ?", levelID).Delete(&LevelProgress{}).Error; err != nil {
		return fmt.Errorf("ошибка удаления прогресса уровней: %w", err)
	}

	if err := db.Unscoped().Delete(&lvl).Error; err != nil {
		return fmt.Errorf("ошибка удаления уровня: %w", err)
	}

	if s.monitorService != nil && s.hub != nil {
		snapshot, err := s.monitorService.GameSnapshot(gameID)
		if err != nil {
			log.Error().Err(err).Uint("game", gameID).Msg("DeleteLevelFromActiveGame: GameSnapshot error")
		} else {
			s.hub.BroadcastToRoom(strconv.Itoa(int(gameID)), snapshot)
		}
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

func (s *GameService) notifyCaptainAboutFinish(ctx context.Context, tx *gorm.DB, teamID, gameID uint) {
	_ = ctx
	if s.cfg == nil {
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

func (s *GameService) notifyCaptainAboutDisqualification(ctx context.Context, tx *gorm.DB, teamID, gameID uint) {
	_ = ctx
	if s.cfg == nil {
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

func (s *GameService) updateMonitorAndResults(ctx context.Context, gameID uint) {
	_ = ctx
	if s.monitorService == nil {
		return
	}
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

func (s *GameService) logAndNotify(ctx context.Context, tx *gorm.DB, progress *LevelProgress, message string, _ uint) {
	_ = ctx
	logEntry := Log{
		GamePassingID: progress.GamePassingID,
		LevelID:       progress.LevelID,
		Message:       message,
	}
	if err := tx.Create(&logEntry).Error; err != nil {
		log.Error().Err(err).Uint("passing", progress.GamePassingID).Uint("level", progress.LevelID).Msg("GameService.logAndNotify: failed to save log")
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
	s.hub.BroadcastToRoom(strconv.Itoa(int(gameID)), snapshot)
}
