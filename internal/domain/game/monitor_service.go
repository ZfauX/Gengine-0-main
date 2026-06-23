// internal/domain/game/monitor_service.go
package game

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/pkg/util"

	"gorm.io/gorm"
)

// MonitorService собирает сводную информацию о прохождении игры.
type MonitorService struct {
	DB       *gorm.DB
	cache    map[uint]*cachedSnapshot
	cacheTTL time.Duration
	mu       sync.RWMutex
}

type cachedSnapshot struct {
	data      []TeamProgress
	timestamp time.Time
}

func NewMonitorService(db *gorm.DB) *MonitorService {
	return &MonitorService{
		DB:       db,
		cache:    make(map[uint]*cachedSnapshot),
		cacheTTL: 10 * time.Second,
	}
}

// TeamProgress содержит агрегированные данные о прогрессе одной команды.
type TeamProgress struct {
	TeamID           uint   `json:"team_id"`
	TeamName         string `json:"team_name"`
	TotalLevels      int    `json:"total_levels"`
	CompletedLevels  int    `json:"completed_levels"`
	CurrentLevel     *uint  `json:"current_level,omitempty"`
	TotalTime        string `json:"total_time"`
	Attempts         int    `json:"attempts"`
	Finished         bool   `json:"finished"`
	Place            *int   `json:"place,omitempty"`
	Suspicious       bool   `json:"suspicious"`
	SuspiciousReason string `json:"suspicious_reason,omitempty"`
}

// GetOrFetchSnapshot возвращает снимок игры: из кэша, если TTL не истёк, иначе из БД.
func (s *MonitorService) GetOrFetchSnapshot(gameID uint) ([]TeamProgress, error) {
	s.mu.RLock()
	if cached, ok := s.cache[gameID]; ok && time.Since(cached.timestamp) < s.cacheTTL {
		s.mu.RUnlock()
		return cached.data, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	if cached, ok := s.cache[gameID]; ok && time.Since(cached.timestamp) < s.cacheTTL {
		s.mu.Unlock()
		return cached.data, nil
	}
	s.mu.Unlock()

	snapshot, err := s.GameSnapshot(gameID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache[gameID] = &cachedSnapshot{
		data:      snapshot,
		timestamp: time.Now(),
	}
	s.mu.Unlock()

	return snapshot, nil
}

// InvalidateCache удаляет кэшированный снимок игры (вызывается при изменениях).
func (s *MonitorService) InvalidateCache(gameID uint) {
	s.mu.Lock()
	delete(s.cache, gameID)
	s.mu.Unlock()
}

// GameSnapshot формирует полную сводку по всем прохождениям игры.
func (s *MonitorService) GameSnapshot(gameID uint) ([]TeamProgress, error) {
	var passings []GamePassing
	err := s.DB.
		Preload("Team").
		Preload("Progresses.Attempts").
		Where("game_id = ?", gameID).
		Find(&passings).Error
	if err != nil {
		return nil, err
	}

	var levels []level.Level
	s.DB.Where("game_id = ?", gameID).Order("position ASC").Find(&levels)
	totalLevels := len(levels)

	var progressList []TeamProgress
	for _, p := range passings {
		tp := TeamProgress{
			TeamID:      p.TeamID,
			TeamName:    p.Team.Name,
			TotalLevels: totalLevels,
			Finished:    p.Status == StatusFinished,
			Place:       p.Place,
		}

		var completed int
		var totalDuration time.Duration
		var attemptsCount int
		var currentLevel *uint

		for _, lp := range p.Progresses {
			attemptsCount += len(lp.Attempts)

			if lp.FinishedAt != nil {
				completed++
				duration := lp.FinishedAt.Sub(lp.StartedAt) + time.Duration(lp.PenaltySeconds)*time.Second
				totalDuration += duration
			} else if currentLevel == nil {
				currentLevel = &lp.LevelID
			}
		}

		tp.CompletedLevels = completed
		tp.CurrentLevel = currentLevel
		tp.TotalTime = util.FormatDuration(totalDuration)
		tp.Attempts = attemptsCount

		sus, reason := s.analyzeTeamBehavior(p.TeamID, gameID)
		tp.Suspicious = sus
		tp.SuspiciousReason = reason

		progressList = append(progressList, tp)
	}

	return progressList, nil
}

// CalculateResults пересчитывает итоговое время и места для завершённых прохождений.
func (s *MonitorService) CalculateResults(gameID uint) error {
	var passings []GamePassing
	if err := s.DB.Where("game_id = ? AND status = ?", gameID, StatusFinished).Find(&passings).Error; err != nil {
		return err
	}

	type passingResult struct {
		ID       uint
		Duration time.Duration
	}
	var results []passingResult
	for _, p := range passings {
		var progresses []LevelProgress
		if err := s.DB.Where("game_passing_id = ?", p.ID).Find(&progresses).Error; err != nil {
			return err
		}
		var total time.Duration
		for _, pr := range progresses {
			if pr.FinishedAt != nil {
				total += pr.FinishedAt.Sub(pr.StartedAt) + time.Duration(pr.PenaltySeconds)*time.Second
			}
		}
		results = append(results, passingResult{ID: p.ID, Duration: total})
		if err := s.DB.Model(&p).Update("result_duration", total).Error; err != nil {
			return err
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Duration < results[j].Duration })
	for i, res := range results {
		place := i + 1
		if err := s.DB.Model(&GamePassing{}).Where("id = ?", res.ID).Update("place", place).Error; err != nil {
			return err
		}
	}
	return nil
}

// analyzeTeamBehavior проверяет команду на подозрительную активность.
func (s *MonitorService) analyzeTeamBehavior(teamID, gameID uint) (bool, string) {
	var passings []uint
	s.DB.Model(&GamePassing{}).Where("game_id = ? AND team_id = ?", gameID, teamID).Pluck("id", &passings)
	if len(passings) == 0 {
		return false, ""
	}

	fiveMinAgo := time.Now().Add(-5 * time.Minute)
	var attempts []Attempt
	s.DB.Joins("JOIN level_progresses ON level_progresses.id = attempts.level_progress_id").
		Where("level_progresses.game_passing_id IN ? AND attempts.created_at >= ?", passings, fiveMinAgo).
		Order("attempts.created_at ASC").
		Find(&attempts)

	if len(attempts) == 0 {
		return false, ""
	}

	rate := float64(len(attempts)) / 5.0 // попыток в минуту
	if rate > 10 {
		return true, fmt.Sprintf("Подозрительная частота: %.1f попыток/мин", rate)
	}

	var lastCode string
	var streak int
	for _, a := range attempts {
		if !a.Success {
			if a.Code == lastCode {
				streak++
				if streak >= 3 {
					return true, fmt.Sprintf("Брутфорс: код '%s' введён %d раз подряд", a.Code, streak+1)
				}
			} else {
				lastCode = a.Code
				streak = 0
			}
		} else {
			lastCode = ""
			streak = 0
		}
	}
	return false, ""
}