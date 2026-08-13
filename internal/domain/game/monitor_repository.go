// internal/domain/game/monitor_repository.go
// A-2 (pass 31): репозиторий мониторинга — MonitorService не обращается к
// *gorm.DB для read-запросов (GameSnapshot, попытки). Транзакции остаются.
package game

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// AggregatedPassing — строка сводки по прохождению (GameSnapshot).
type AggregatedPassing struct {
	GamePassingID  uint
	TeamID         uint
	TeamName       string
	Status         string
	Place          *int
	TotalLevels    int
	CompletedCount int
	TotalAttempts  int
	TotalPenalty   int
	FirstStarted   *time.Time
	LastFinished   *time.Time
	CurrentLevelID *uint
}

// MonitorRepository — контракт для read-запросов мониторинга.
type MonitorRepository interface {
	AggregateGameSnapshot(ctx context.Context, gameID uint) ([]AggregatedPassing, error)
	ListRecentAttempts(ctx context.Context, passingIDs []uint, since time.Time) ([]AttemptRecord, error)
}

type gormMonitorRepo struct{ db *gorm.DB }

func NewGormMonitorRepo(db *gorm.DB) MonitorRepository {
	return &gormMonitorRepo{db: db}
}

func (r *gormMonitorRepo) AggregateGameSnapshot(ctx context.Context, gameID uint) ([]AggregatedPassing, error) {
	var aggregated []AggregatedPassing
	query := `
		WITH total_levels_cte AS (
			SELECT COUNT(*) AS cnt FROM levels WHERE game_id = ?
		)
		SELECT
			gp.id AS game_passing_id,
			gp.team_id,
			t.name AS team_name,
			gp.status,
			gp.place,
			tl.cnt AS total_levels,
			COUNT(lp.id) FILTER (WHERE lp.finished_at IS NOT NULL) AS completed_count,
			COALESCE(ac.total_attempts, 0) AS total_attempts,
			COALESCE(SUM(lp.penalty_seconds), 0) AS total_penalty,
			MIN(lp.started_at) AS first_started,
			MAX(lp.finished_at) AS last_finished,
			cl.level_id AS current_level_id
		FROM game_passings gp
		JOIN teams t ON t.id = gp.team_id
		CROSS JOIN total_levels_cte tl
		LEFT JOIN level_progresses lp ON lp.game_passing_id = gp.id
		LEFT JOIN (
			SELECT a.level_progress_id, COUNT(*) AS total_attempts
			FROM attempts a
			JOIN level_progresses lp2 ON lp2.id = a.level_progress_id
			WHERE lp2.game_passing_id IN (SELECT id FROM game_passings WHERE game_id = ?)
			GROUP BY a.level_progress_id
		) ac ON ac.level_progress_id = lp.id
		LEFT JOIN LATERAL (
			SELECT level_id FROM level_progresses
			WHERE game_passing_id = gp.id AND finished_at IS NULL
			ORDER BY created_at DESC LIMIT 1
		) cl ON true
		WHERE gp.game_id = ?
		GROUP BY gp.id, gp.team_id, t.name, gp.status, gp.place, tl.cnt, ac.total_attempts, cl.level_id
		ORDER BY gp.place ASC`
	if err := r.db.WithContext(ctx).Raw(query, gameID, gameID, gameID).Scan(&aggregated).Error; err != nil {
		return nil, err
	}
	return aggregated, nil
}

func (r *gormMonitorRepo) ListRecentAttempts(ctx context.Context, passingIDs []uint, since time.Time) ([]AttemptRecord, error) {
	// F-3 (pass 36): LIMIT 500 последних попыток (DESC) вместо выкачивания
	// ВСЕХ кодов попыток за окно на каждый промах снапшот-кэша. Rate-детекция
	// (500/5мин = 100/мин >> 10/мин) и streak-детекция (3 подряд в пределах
	// 500) остаются корректными; хронологию восстанавливаем реверсом в Go.
	const recentAttemptsLimit = 500

	var attempts []AttemptRecord
	err := r.db.WithContext(ctx).Table("attempts").
		Select("level_progresses.game_passing_id AS passing_id, attempts.code, attempts.success, attempts.created_at").
		Joins("JOIN level_progresses ON level_progresses.id = attempts.level_progress_id").
		Where("level_progresses.game_passing_id IN ? AND attempts.created_at >= ?", passingIDs, since).
		Order("attempts.created_at DESC").
		Limit(recentAttemptsLimit).
		Find(&attempts).Error
	if err != nil {
		return nil, err
	}
	// Хронологический порядок (ASC) для streak-детекции.
	for i, j := 0, len(attempts)-1; i < j; i, j = i+1, j-1 {
		attempts[i], attempts[j] = attempts[j], attempts[i]
	}
	return attempts, nil
}

var _ MonitorRepository = (*gormMonitorRepo)(nil)
