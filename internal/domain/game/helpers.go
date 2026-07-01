// internal/domain/game/helpers.go
package game

import (
	"errors"
	"time"

	"gengine-0/internal/domain/team"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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

// finishPassingProgress завершает прогресс прохождения (все незавершённые уровни)
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
