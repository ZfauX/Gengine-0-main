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
	coAuthorSvc *CoAuthorService
	cfg         *config.Config
}

// NewGameAdminService создаёт новый экземпляр GameAdminService.
func NewGameAdminService(db *gorm.DB, coAuthorSvc *CoAuthorService, cfg *config.Config) *GameAdminService {
	return &GameAdminService{
		db:          db,
		coAuthorSvc: coAuthorSvc,
		cfg:         cfg,
	}
}

// GameAdminService принудительно завершает игру с транзакцией и блокировками.
// Требует прав модератора.
func (s *GameAdminService) ForceFinishGame(ctx context.Context, gameID, userID uint) error {
	var passings []GamePassing
	var game Game
	var teamIDs []uint

	// Проверка прав перед транзакцией
	ok, err := s.coAuthorSvc.HasPermission(ctx, gameID, userID, RoleModerator)
	if err != nil {
		return fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if !ok {
		return errors.New("только автор или модератор может завершить игру")
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_id = ? AND status = ?", gameID, StatusStarted).
			Preload("Team.Captain").
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

	// Отправляем уведомления после фиксации транзакции
	for _, p := range passings {
		s.notifyCaptainAboutFinish(p.TeamID, &game)
	}
	return nil
}

// DisqualifyTeam дисквалифицирует команду с транзакцией и блокировкой.
// Требует прав модератора.
func (s *GameAdminService) DisqualifyTeam(ctx context.Context, gameID, teamID, userID uint) error {
	var passing GamePassing
	var game Game

	// Проверка прав перед транзакцией
	ok, err := s.coAuthorSvc.HasPermission(ctx, gameID, userID, RoleModerator)
	if err != nil {
		return fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if !ok {
		return errors.New("только автор или модератор может дисквалифицировать команду")
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("game_id = ? AND team_id = ? AND status = ?", gameID, teamID, StatusStarted).
			Preload("Team.Captain").
			First(&passing).Error; err != nil {
			return errors.New("команда не в игре или уже завершила")
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
	s.notifyCaptainAboutDisqualification(teamID, &game)
	return nil
}

// DeleteLevelFromActiveGame удаляет уровень из активной игры с транзакцией.
func (s *GameAdminService) DeleteLevelFromActiveGame(ctx context.Context, gameID, levelID, userID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Проверка прав внутри транзакции
		ok, err := s.coAuthorSvc.HasPermissionTx(tx, gameID, userID, RoleContentEditor)
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
				if err := AdvanceToNextLevel(tx, p.ID, levelID, nil); err != nil {
					log.Error().Uint("passing", p.ID).Err(err).Msg("DeleteLevelFromActiveGame: AdvanceToNextLevel error")
				}
			}
		}

		if err := tx.Unscoped().Where("level_id = ?", levelID).Delete(&LevelProgress{}).Error; err != nil {
			return fmt.Errorf("ошибка удаления прогресса уровней: %w", err)
		}

		if err := tx.Unscoped().Delete(&lvl).Error; err != nil {
			return fmt.Errorf("ошибка удаления уровня: %w", err)
		}
		return nil
	})
}

// notifyCaptainAboutFinish отправляет уведомление после фиксации транзакции.
func (s *GameAdminService) notifyCaptainAboutFinish(teamID uint, game *Game) {
	if s.cfg == nil || !s.cfg.SMTP.Enabled {
		return
	}
	var t team.Team
	if err := s.db.First(&t, teamID).Error; err != nil {
		log.Error().Err(err).Uint("team", teamID).Msg("notifyCaptainAboutFinish: failed to get team")
		return
	}
	var captain user.User
	if err := s.db.First(&captain, t.CaptainID).Error; err != nil {
		log.Error().Err(err).Uint("captain", t.CaptainID).Msg("notifyCaptainAboutFinish: failed to get captain")
		return
	}
	if err := email.Enqueue(
		captain.Email,
		"Игра завершена",
		fmt.Sprintf("Игра «%s» была принудительно завершена автором.", game.Name),
	); err != nil {
		log.Error().Err(err).Uint("game", game.ID).Uint("team", teamID).Msg("notifyCaptainAboutFinish: failed to enqueue email")
	}
}

// notifyCaptainAboutDisqualification отправляет уведомление после фиксации транзакции.
func (s *GameAdminService) notifyCaptainAboutDisqualification(teamID uint, game *Game) {
	if s.cfg == nil || !s.cfg.SMTP.Enabled {
		return
	}
	var t team.Team
	if err := s.db.First(&t, teamID).Error; err != nil {
		log.Error().Err(err).Uint("team", teamID).Msg("notifyCaptainAboutDisqualification: failed to get team")
		return
	}
	var captain user.User
	if err := s.db.First(&captain, t.CaptainID).Error; err != nil {
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
