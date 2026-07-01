// internal/domain/game/monitor_service.go
package game

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/pkg/util"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// MonitorService собирает сводную информацию о прохождении игры.
type MonitorService struct {
	DB       *gorm.DB
	cache    map[uint]*cachedSnapshot
	cacheTTL time.Duration
	mu       sync.RWMutex
	sfGroup  singleflight.Group // предотвращает множественные одновременные запросы к БД
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
// Использует singleflight для предотвращения множественных одновременных запросов к БД.
func (s *MonitorService) GetOrFetchSnapshot(gameID uint) ([]TeamProgress, error) {
	// Быстрая проверка кэша с RLock
	s.mu.RLock()
	if cached, ok := s.cache[gameID]; ok && time.Since(cached.timestamp) < s.cacheTTL {
		s.mu.RUnlock()
		return cached.data, nil
	}
	s.mu.RUnlock()

	// Используем singleflight для группировки одновременных запросов
	key := fmt.Sprintf("snapshot:%d", gameID)
	result, err, _ := s.sfGroup.Do(key, func() (interface{}, error) {
		// Повторная проверка кэша уже внутри Lock (защита от гонки)
		s.mu.RLock()
		if cached, ok := s.cache[gameID]; ok && time.Since(cached.timestamp) < s.cacheTTL {
			s.mu.RUnlock()
			return cached.data, nil
		}
		s.mu.RUnlock()

		// Загрузка из БД
		snapshot, err := s.GameSnapshot(gameID)
		if err != nil {
			return nil, err
		}

		// Сохраняем в кэш
		s.mu.Lock()
		s.cache[gameID] = &cachedSnapshot{
			data:      snapshot,
			timestamp: time.Now(),
		}
		s.mu.Unlock()

		return snapshot, nil
	})

	if err != nil {
		return nil, err
	}
	return result.([]TeamProgress), nil
}

// InvalidateCache удаляет кэшированный снимок игры (вызывается при изменениях).
func (s *MonitorService) InvalidateCache(gameID uint) {
	s.mu.Lock()
	delete(s.cache, gameID)
	s.mu.Unlock()
}

// GameSnapshot формирует полную сводку по всем прохождениям игры.
// Оптимизированная версия: использует агрегирующие SQL-запросы.
func (s *MonitorService) GameSnapshot(gameID uint) ([]TeamProgress, error) {
	// 1. Получаем общее количество уровней в игре
	var totalLevels int64
	if err := s.DB.Model(&level.Level{}).Where("game_id = ?", gameID).Count(&totalLevels).Error; err != nil {
		return nil, err
	}

	// 2. Получаем агрегированные данные по прохождениям
	type AggregatedPassing struct {
		GamePassingID  uint
		TeamID         uint
		TeamName       string
		Status         string
		Place          *int
		CompletedCount int
		TotalAttempts  int
		TotalPenalty   int
		FirstStarted   *time.Time
		LastFinished   *time.Time
	}

	var aggregated []AggregatedPassing
	query := `
		SELECT 
			gp.id AS game_passing_id,
			gp.team_id,
			t.name AS team_name,
			gp.status,
			gp.place,
			COUNT(lp.id) FILTER (WHERE lp.finished_at IS NOT NULL) AS completed_count,
			COALESCE(SUM((SELECT COUNT(*) FROM attempts a WHERE a.level_progress_id = lp.id)), 0) AS total_attempts,
			COALESCE(SUM(lp.penalty_seconds), 0) AS total_penalty,
			MIN(lp.started_at) AS first_started,
			MAX(lp.finished_at) AS last_finished
		FROM game_passings gp
		JOIN teams t ON t.id = gp.team_id
		LEFT JOIN level_progresses lp ON lp.game_passing_id = gp.id
		WHERE gp.game_id = ?
		GROUP BY gp.id, gp.team_id, t.name, gp.status, gp.place
	`
	if err := s.DB.Raw(query, gameID).Scan(&aggregated).Error; err != nil {
		return nil, err
	}

	// 3. Определяем текущий уровень для незавершённых прохождений
	type CurrentLevel struct {
		GamePassingID uint
		LevelID       uint
	}
	var currentLevels []CurrentLevel
	if err := s.DB.Table("level_progresses").
		Select("DISTINCT ON (game_passing_id) game_passing_id, level_id").
		Where("game_passing_id IN (SELECT id FROM game_passings WHERE game_id = ? AND status = ?)", gameID, StatusStarted).
		Order("game_passing_id, created_at DESC").
		Scan(&currentLevels).Error; err != nil {
		// Не фатально, просто не покажем текущий уровень
		currentLevels = nil
	}

	currentLevelMap := make(map[uint]uint)
	for _, cl := range currentLevels {
		currentLevelMap[cl.GamePassingID] = cl.LevelID
	}

	// 4. Формируем результат
	result := make([]TeamProgress, 0, len(aggregated))
	for _, a := range aggregated {
		tp := TeamProgress{
			TeamID:          a.TeamID,
			TeamName:        a.TeamName,
			TotalLevels:     int(totalLevels),
			CompletedLevels: a.CompletedCount,
			Finished:        a.Status == string(StatusFinished),
			Place:           a.Place,
			Attempts:        a.TotalAttempts,
		}

		// Вычисляем общее время
		var totalDuration time.Duration
		if a.FirstStarted != nil && a.LastFinished != nil {
			totalDuration = a.LastFinished.Sub(*a.FirstStarted) + time.Duration(a.TotalPenalty)*time.Second
		}
		tp.TotalTime = util.FormatDuration(totalDuration)

		// Устанавливаем текущий уровень
		if cur, ok := currentLevelMap[a.GamePassingID]; ok && !tp.Finished {
			tp.CurrentLevel = &cur
		}

		// Проверка на подозрительное поведение (можно оставить как есть или упростить)
		// Для производительности можно пропустить анализ или делать его асинхронно
		sus, reason := s.analyzeTeamBehavior(a.TeamID, gameID)
		tp.Suspicious = sus
		tp.SuspiciousReason = reason

		result = append(result, tp)
	}

	return result, nil
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
// Упрощённая версия для снижения нагрузки.
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
