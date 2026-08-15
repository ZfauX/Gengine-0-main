// internal/domain/game/game_admin_service.go
package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/metrics"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GameAdminService struct {
	db          *gorm.DB
	teamRepo    team.TeamRepository
	userRepo    user.UserRepository
	coAuthorSvc *CoAuthorService
	cfg         *config.Config
	sseMgr      *SSEManager
	// gameFinishedCallback вызывается при принудительном завершении игры.
	gameFinishedCallback GameCompletionCallback
}

// NewGameAdminService создаёт новый экземпляр GameAdminService.
func NewGameAdminService(db *gorm.DB, coAuthorSvc *CoAuthorService, cfg *config.Config) *GameAdminService {
	return &GameAdminService{
		db:          db,
		coAuthorSvc: coAuthorSvc,
		cfg:         cfg,
	}
}

// WithRepositories внедряет репозитории команд и пользователей (A-M2, pass 34:
// notify-чтения идут через репозитории, а не raw s.db).
func (s *GameAdminService) WithRepositories(teamRepo team.TeamRepository, userRepo user.UserRepository) *GameAdminService {
	s.teamRepo = teamRepo
	s.userRepo = userRepo
	return s
}

// WithSSEManager устанавливает SSE-менеджер для broadcast-уведомлений.
func (s *GameAdminService) WithSSEManager(sseMgr *SSEManager) *GameAdminService {
	s.sseMgr = sseMgr
	return s
}

// WithGameFinishedCallback устанавливает колбэк завершения игры (турнирные очки и пр.).
func (s *GameAdminService) WithGameFinishedCallback(cb GameCompletionCallback) *GameAdminService {
	s.gameFinishedCallback = cb
	return s
}

// ForceFinishGame принудительно завершает игру с транзакцией и блокировками.
// Требует прав модератора.
func (s *GameAdminService) ForceFinishGame(ctx context.Context, gameID, userID uint) error {
	var passings []GamePassing
	var game Game
	var teamIDs []uint

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Проверка прав ВНУТРИ транзакции (предотвращает race condition)
		ok, err := s.coAuthorSvc.CanModerateGameTx(ctx, tx, gameID, userID)
		if err != nil {
			return fmt.Errorf("ошибка проверки прав: %w", err)
		}
		if !ok {
			return errors.New("только автор или модератор может завершить игру")
		}

		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_id = ? AND status = ?", gameID, StatusStarted).
			Find(&passings).Error; err != nil {
			return err
		}
		if len(passings) == 0 {
			return errors.New("нет активных прохождений")
		}

		if err := tx.First(&game, gameID).Error; err != nil {
			return err
		}

		now := time.Now()
		for _, p := range passings {
			if err := finishPassingProgress(tx, &p, now); err != nil {
				return err
			}
			teamIDs = append(teamIDs, p.TeamID)
			metrics.IncGamePassings(string(StatusFinished))
			if !p.CreatedAt.IsZero() {
				duration := now.Sub(p.CreatedAt).Seconds()
				metrics.ObserveGameDuration(duration)
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	// Отправляем уведомления после фиксации транзакции (батч, P-M8).
	s.notifyCaptainsAboutFinish(ctx, teamIDs, &game)

	// Принудительное завершение = завершение игры: начисляем турнирные очки
	// и пересчитываем результаты (аналогично обычному финишу).
	if s.gameFinishedCallback != nil {
		s.gameFinishedCallback(context.WithoutCancel(ctx), gameID)
	}
	return nil
}

// DisqualifyTeam дисквалифицирует команду с транзакцией и блокировкой.
// Требует прав модератора.
func (s *GameAdminService) DisqualifyTeam(ctx context.Context, gameID, teamID, userID uint) error {
	var passing GamePassing
	var game Game

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Проверка прав ВНУТРИ транзакции (предотвращает race condition)
		ok, err := s.coAuthorSvc.CanModerateGameTx(ctx, tx, gameID, userID)
		if err != nil {
			return fmt.Errorf("ошибка проверки прав: %w", err)
		}
		if !ok {
			return errors.New("только автор или модератор может дисквалифицировать команду")
		}

		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_id = ? AND team_id = ? AND status = ?", gameID, teamID, StatusStarted).
			First(&passing).Error; err != nil {
			// Различаем «команда не в игре» и реальную ошибку БД (B3).
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("команда не в игре или уже завершила")
			}
			return err
		}

		if err := tx.First(&game, gameID).Error; err != nil {
			return err
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
		return nil
	})

	if err != nil {
		return err
	}

	// Отправляем уведомление после фиксации транзакции
	s.notifyCaptainAboutDisqualification(ctx, teamID, &game)
	// Отправляем SSE-уведомление о дисквалификации
	if s.sseMgr != nil {
		s.sseMgr.Broadcast(gameID, "team_disqualified", map[string]any{
			"game_id":         gameID,
			"team_id":         teamID,
			"disqualified_at": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	return nil
}

// DeleteLevelFromActiveGame удаляет уровень из активной игры с транзакцией.
func (s *GameAdminService) DeleteLevelFromActiveGame(ctx context.Context, gameID, levelID, userID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Проверка прав внутри транзакции
		ok, err := s.coAuthorSvc.CanEditContentTx(ctx, tx, gameID, userID)
		if err != nil {
			return fmt.Errorf("ошибка проверки прав: %w", err)
		}
		if !ok {
			return errors.New("только автор или контент-менеджер может удалять уровни")
		}

		var lvl level.Level
		if err := tx.First(&lvl, levelID).Error; err != nil {
			return err
		}
		// IDOR (PASS-8, CRITICAL): уровень должен принадлежать игре из URL —
		// иначе менеджер игры A удаляет уровень чужой игры B (cross-game).
		if lvl.GameID != gameID {
			return ErrLevelNotInGame
		}
		if lvl.DeletedAt.Valid {
			return errors.New("уровень уже удалён")
		}

		var passings []GamePassing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_id = ? AND status = ?", gameID, StatusStarted).
			Find(&passings).Error; err != nil {
			return err
		}

		now := time.Now()
		// A-2 (pass 32): загружаем текущие прогрессы ВСЕХ активных прохождений
		// одним запросом с FOR UPDATE (вместо GetCurrentProgressForUpdate в цикле —
		// был N+1). Фильтруем совпадающие по levelID в Go.
		passingIDs := make([]uint, 0, len(passings))
		for _, p := range passings {
			passingIDs = append(passingIDs, p.ID)
		}
		var progressList []LevelProgress
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_passing_id IN ? AND finished_at IS NULL", passingIDs).
			Find(&progressList).Error; err != nil {
			log.Error().Err(err).Msg("DeleteLevelFromActiveGame: batch load progress error")
			return fmt.Errorf("не удалось получить прогрессы прохождений: %w", err)
		}
		progressByPassing := make(map[uint]*LevelProgress, len(progressList))
		for i := range progressList {
			progressByPassing[progressList[i].GamePassingID] = &progressList[i]
		}
		for _, p := range passings {
			progress, ok := progressByPassing[p.ID]
			if !ok {
				log.Error().Uint("passing", p.ID).Err(gorm.ErrRecordNotFound).Msg("DeleteLevelFromActiveGame: no active progress")
				// Не удаляем уровень, если не можем перевести активные команды —
				// иначе прохождение останется без прогресса (C-H5).
				return fmt.Errorf("не удалось получить прогресс прохождения %d", p.ID)
			}
			if progress.LevelID == levelID {
				progress.FinishedAt = &now
				if err := tx.Save(progress).Error; err != nil {
					log.Error().Uint("progress", progress.ID).Err(err).Msg("DeleteLevelFromActiveGame: Save progress error")
					return fmt.Errorf("не удалось завершить прогресс прохождения %d: %w", p.ID, err)
				}
				// P-3 (pass 38): передаём уже загруженный passing (range p) —
				// AdvanceToNextLevel не делает повторный SELECT.
				pCopy := p
				if _, err := AdvanceToNextLevelWithPassing(tx, &pCopy, levelID, nil); err != nil {
					log.Error().Uint("passing", p.ID).Err(err).Msg("DeleteLevelFromActiveGame: AdvanceToNextLevel error")
					return fmt.Errorf("не удалось перевести прохождение %d на следующий уровень: %w", p.ID, err)
				}
			}
		}

		if err := tx.Unscoped().Where("level_id = ?", levelID).Delete(&LevelProgress{}).Error; err != nil {
			return fmt.Errorf("ошибка удаления прогресса уровней: %w", err)
		}

		// G7: удаляем ответы и вопросы уровня (в этом порядке — FK answers→questions),
		// иначе после hard-delete уровня остаются сироты-вопросы и падает FK.
		// Сначала ответы всех вопросов уровня, затем сами вопросы.
		var questionIDs []uint
		if err := tx.Model(&level.Question{}).Where("level_id = ?", levelID).Pluck("id", &questionIDs).Error; err != nil {
			return fmt.Errorf("ошибка загрузки вопросов уровня: %w", err)
		}
		if len(questionIDs) > 0 {
			if err := tx.Unscoped().Where("question_id IN ?", questionIDs).Delete(&level.Answer{}).Error; err != nil {
				return fmt.Errorf("ошибка удаления ответов уровня: %w", err)
			}
		}
		if err := tx.Unscoped().Where("level_id = ?", levelID).Delete(&level.Question{}).Error; err != nil {
			return fmt.Errorf("ошибка удаления вопросов уровня: %w", err)
		}
		// Мини-игра уровня — тоже удаляем физически (soft-delete оставил бы ссылку на уровень).
		if err := tx.Unscoped().Where("level_id = ?", levelID).Delete(&level.MiniGame{}).Error; err != nil {
			return fmt.Errorf("ошибка удаления мини-игры уровня: %w", err)
		}

		if err := tx.Unscoped().Delete(&lvl).Error; err != nil {
			return fmt.Errorf("ошибка удаления уровня: %w", err)
		}
		return nil
	})
}

// notifyCaptainsAboutFinish отправляет уведомления капитанам после фиксации
// транзакции. Батч-версия (P-M8): 2 запроса на все команды вместо 2 на команду.
func (s *GameAdminService) notifyCaptainsAboutFinish(ctx context.Context, teamIDs []uint, game *Game) {
	if s.cfg == nil || !s.cfg.SMTP.Enabled || len(teamIDs) == 0 {
		return
	}
	// M5 (PASS-19): WithoutCancel — уведомления после коммита не должны
	// отменяться из-за disconnect/таймаута запроса админа.
	notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	var teams []team.Team
	if s.teamRepo == nil {
		log.Warn().Msg("notifyCaptainsAboutFinish: teamRepo is nil, skipping teams fetch")
		return
	}
	// A-M2 (pass 34): через teamRepo вместо raw s.db.
	teams, err := s.teamRepo.ListByIDs(notifyCtx, teamIDs)
	if err != nil {
		log.Error().Err(err).Msg("notifyCaptainsAboutFinish: failed to get teams")
		return
	}
	captainIDs := make([]uint, 0, len(teams))
	for i := range teams {
		captainIDs = append(captainIDs, teams[i].CaptainID)
	}
	var captains []user.User
	if s.userRepo == nil {
		log.Warn().Msg("notifyCaptainsAboutFinish: userRepo is nil, skipping captains fetch")
		return
	}
	captains, err = s.userRepo.ListByIDs(notifyCtx, captainIDs)
	if err != nil {
		log.Error().Err(err).Msg("notifyCaptainsAboutFinish: failed to get captains")
		return
	}
	emailByID := make(map[uint]string, len(captains))
	for _, c := range captains {
		emailByID[c.ID] = c.Email
	}
	for _, t := range teams {
		addr, ok := emailByID[t.CaptainID]
		if !ok || addr == "" {
			continue
		}
		if err := email.Enqueue(
			addr,
			"Игра завершена",
			fmt.Sprintf("Игра «%s» была принудительно завершена автором.", game.Name),
		); err != nil {
			log.Error().Err(err).Uint("game", game.ID).Uint("team", t.ID).Msg("notifyCaptainsAboutFinish: failed to enqueue email")
		}
	}
}

// notifyCaptainAboutDisqualification отправляет уведомление после фиксации транзакции.
func (s *GameAdminService) notifyCaptainAboutDisqualification(ctx context.Context, teamID uint, game *Game) {
	if s.cfg == nil || !s.cfg.SMTP.Enabled {
		return
	}
	notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if s.teamRepo == nil {
		log.Warn().Uint("team", teamID).Msg("notifyCaptainAboutDisqualification: teamRepo is nil, skipping")
		return
	}
	// A-M2 (pass 34): через teamRepo вместо raw s.db.
	t, err := s.teamRepo.GetByID(notifyCtx, teamID)
	if err != nil {
		log.Error().Err(err).Uint("team", teamID).Msg("notifyCaptainAboutDisqualification: failed to get team")
		return
	}
	var captain *user.User
	if s.userRepo == nil {
		log.Warn().Msg("notifyCaptainAboutDisqualification: userRepo is nil, skipping captain fetch")
		return
	}
	captain, err = s.userRepo.GetByID(notifyCtx, t.CaptainID)
	if err != nil {
		log.Error().Err(err).Uint("captain", t.CaptainID).Msg("notifyCaptainAboutDisqualification: failed to get captain")
		return
	}
	if err := email.Enqueue(
		captain.Email,
		"Дисквалификация",
		fmt.Sprintf("Ваша команда была дисквалифицирована в игре «%s».", game.Name),
	); err != nil {
		log.Error().Err(err).Uint("game", game.ID).Uint("team", teamID).Msg("notifyCaptainAboutDisqualification: failed to enqueue email")
	}
}
