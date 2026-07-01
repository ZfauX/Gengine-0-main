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

// GameAdminService отвечает за административные действия: принудительное завершение,
// дисквалификация, удаление уровней из активной игры.
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

// ForceFinishGame принудительно завершает игру с транзакцией и блокировками.
func (s *GameAdminService) ForceFinishGame(ctx context.Context, gameID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
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
			if err := finishPassingProgress(tx, &p, now); err != nil {
				return err
			}
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
func (s *GameAdminService) DisqualifyTeam(ctx context.Context, gameID, teamID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
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

// DeleteLevelFromActiveGame удаляет уровень из активной игры с транзакцией.
func (s *GameAdminService) DeleteLevelFromActiveGame(ctx context.Context, gameID, levelID, userID uint) error {
	db := s.db
	ok, err := s.coAuthorSvc.HasPermission(gameID, userID, "content")
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

		if err := tx.Unscoped().Where("level_id = ?", levelID).Delete(&LevelProgress{}).Error; err != nil {
			return fmt.Errorf("ошибка удаления прогресса уровней: %w", err)
		}

		if err := tx.Unscoped().Delete(&lvl).Error; err != nil {
			return fmt.Errorf("ошибка удаления уровня: %w", err)
		}
		return nil
	})
}

// notifyCaptainAboutFinish отправляет уведомление капитану о принудительном завершении игры.
func (s *GameAdminService) notifyCaptainAboutFinish(_ context.Context, tx *gorm.DB, teamID, gameID uint) {
	if s.cfg == nil || !s.cfg.SMTP.Enabled {
		return
	}
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
	if err := email.Enqueue(
		captain.Email,
		"Игра завершена",
		fmt.Sprintf("Игра «%s» была принудительно завершена автором.", g.Name),
	); err != nil {
		log.Error().Err(err).Uint("game", gameID).Uint("team", teamID).Msg("notifyCaptainAboutFinish: failed to enqueue email")
	}
}

// notifyCaptainAboutDisqualification отправляет уведомление капитану о дисквалификации команды.
func (s *GameAdminService) notifyCaptainAboutDisqualification(_ context.Context, tx *gorm.DB, teamID, gameID uint) {
	if s.cfg == nil || !s.cfg.SMTP.Enabled {
		return
	}
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
	if err := email.Enqueue(
		captain.Email,
		"Дисквалификация",
		fmt.Sprintf("Ваша команда была дисквалифицирована в игре «%s».", g.Name),
	); err != nil {
		log.Error().Err(err).Uint("game", gameID).Uint("team", teamID).Msg("notifyCaptainAboutDisqualification: failed to enqueue email")
	}
}
