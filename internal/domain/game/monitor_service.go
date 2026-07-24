// internal/domain/game/monitor_service.go
package game

import (
	"container/list"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/pkg/util"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

// MonitorServiceInterface определяет методы мониторинга, используемые другими сервисами.
type MonitorServiceInterface interface {
	GetOrFetchSnapshot(ctx context.Context, gameID uint) ([]TeamProgress, error)
	InvalidateCache(gameID uint)
	CalculateResults(ctx context.Context, gameID uint) error
}

// MonitorService собирает сводную информацию о прохождении игры.
type MonitorService struct {
	DB        *gorm.DB
	cache     map[uint]*cachedSnapshot
	cacheList *list.List
	cacheKeys map[uint]*list.Element
	cacheTTL  time.Duration
	mu        sync.RWMutex
	sfGroup   singleflight.Group
}

type cachedSnapshot struct {
	data      []TeamProgress
	timestamp time.Time
}

const maxMonitorCacheSize = 1000

func NewMonitorService(db *gorm.DB) *MonitorService {
	s := &MonitorService{
		DB:       db,
		cache:    make(map[uint]*cachedSnapshot),
		cacheTTL: 30 * time.Second,
	}
	s.cacheList = list.New()
	s.cacheKeys = make(map[uint]*list.Element)
	return s
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
func (s *MonitorService) GetOrFetchSnapshot(ctx context.Context, gameID uint) ([]TeamProgress, error) {
	// Быстрая проверка кэша с RLock
	s.mu.RLock()
	if cached, ok := s.cache[gameID]; ok && time.Since(cached.timestamp) < s.cacheTTL {
		s.mu.RUnlock()
		return cached.data, nil
	}
	s.mu.RUnlock()

	// Используем singleflight для группировки одновременных запросов
	key := fmt.Sprintf("snapshot:%d", gameID)
	result, err, _ := s.sfGroup.Do(key, func() (any, error) {
		// Повторная проверка кэша уже внутри Lock (защита от гонки)
		s.mu.RLock()
		if cached, ok := s.cache[gameID]; ok && time.Since(cached.timestamp) < s.cacheTTL {
			s.mu.RUnlock()
			return cached.data, nil
		}
		s.mu.RUnlock()

		// Загрузка из БД
		snapshot, err := s.GameSnapshot(ctx, gameID)
		if err != nil {
			return nil, err
		}

		// Сохраняем в кэш с лимитом: максимум maxMonitorCacheSize, вытеснение старых
		s.mu.Lock()
		if len(s.cache) > maxMonitorCacheSize {
			front := s.cacheList.Front()
			if front != nil {
				if oldestID, ok := front.Value.(uint); ok {
					delete(s.cache, oldestID)
					delete(s.cacheKeys, oldestID)
					s.cacheList.Remove(front)
				}
			}
		}
		s.cache[gameID] = &cachedSnapshot{
			data:      snapshot,
			timestamp: time.Now(),
		}
		s.cacheKeys[gameID] = s.cacheList.PushBack(gameID)
		s.mu.Unlock()

		return snapshot, nil
	})

	if err != nil {
		return nil, err
	}
	teamProgress, ok := result.([]TeamProgress)
	if !ok {
		return nil, fmt.Errorf("unexpected type for result")
	}
	return teamProgress, nil
}

// InvalidateCache удаляет кэшированный снимок игры (вызывается при изменениях).
func (s *MonitorService) InvalidateCache(gameID uint) {
	s.mu.Lock()
	delete(s.cache, gameID)
	if elem, ok := s.cacheKeys[gameID]; ok {
		s.cacheList.Remove(elem)
		delete(s.cacheKeys, gameID)
	}
	s.mu.Unlock()
}

// teamAggregatedData — данные для batch-анализа подозрительного поведения.
type teamAggregatedData struct {
	TeamID        uint
	GamePassingID uint
}

// attemptRecord — запись об попытке для batch-анализа.
type attemptRecord struct {
	PassingID uint
	Code      string
	Success   bool
	CreatedAt time.Time
}

// GameSnapshot формирует полную сводку по всем прохождениям игры.
// Оптимизированная версия: объединяет 3 SQL-запроса в один.
func (s *MonitorService) GameSnapshot(ctx context.Context, gameID uint) ([]TeamProgress, error) {
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

	var aggregated []AggregatedPassing
	query := `
		SELECT
			gp.id AS game_passing_id,
			gp.team_id,
			t.name AS team_name,
			gp.status,
			gp.place,
			(SELECT COUNT(*) FROM levels WHERE game_id = ?) AS total_levels,
			COUNT(lp.id) FILTER (WHERE lp.finished_at IS NOT NULL) AS completed_count,
			COALESCE(ac.total_attempts, 0) AS total_attempts,
			COALESCE(SUM(lp.penalty_seconds), 0) AS total_penalty,
			MIN(lp.started_at) AS first_started,
			MAX(lp.finished_at) AS last_finished,
			cl.level_id AS current_level_id
		FROM game_passings gp
		JOIN teams t ON t.id = gp.team_id
		LEFT JOIN level_progresses lp ON lp.game_passing_id = gp.id
		LEFT JOIN (
			SELECT level_progress_id, COUNT(*) AS total_attempts
			FROM attempts
			GROUP BY level_progress_id
		) ac ON ac.level_progress_id = lp.id
		LEFT JOIN LATERAL (
			SELECT level_id FROM level_progresses
			WHERE game_passing_id = gp.id AND finished_at IS NULL
			ORDER BY created_at DESC LIMIT 1
		) cl ON true
		WHERE gp.game_id = ?
		GROUP BY gp.id, gp.team_id, t.name, gp.status, gp.place, ac.total_attempts, cl.level_id
		ORDER BY gp.place ASC`
	if err := s.DB.WithContext(ctx).Raw(query, gameID, gameID).Scan(&aggregated).Error; err != nil {
		return nil, err
	}

	// Собираем данные для batch-анализа
	teamData := make([]teamAggregatedData, 0, len(aggregated))
	for _, a := range aggregated {
		teamData = append(teamData, teamAggregatedData{
			TeamID:        a.TeamID,
			GamePassingID: a.GamePassingID,
		})
	}

	// Формируем результат
	suspiciousMap := s.analyzeTeamsBehavior(teamData)

	result := make([]TeamProgress, 0, len(aggregated))
	for _, a := range aggregated {
		tp := TeamProgress{
			TeamID:          a.TeamID,
			TeamName:        a.TeamName,
			TotalLevels:     a.TotalLevels,
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
		if a.CurrentLevelID != nil && !tp.Finished {
			tp.CurrentLevel = a.CurrentLevelID
		}

		// Подозрительное поведение (из batch-анализа)
		if reason, ok := suspiciousMap[a.TeamID]; ok {
			tp.Suspicious = true
			tp.SuspiciousReason = reason
		}

		result = append(result, tp)
	}

	return result, nil
}

// CalculateResults пересчитывает итоговое время и места для завершённых прохождений.
func (s *MonitorService) CalculateResults(ctx context.Context, gameID uint) error {
	var passings []GamePassing
	if err := s.DB.WithContext(ctx).Where("game_id = ? AND status = ?", gameID, StatusFinished).Find(&passings).Error; err != nil {
		return err
	}
	if len(passings) == 0 {
		return nil
	}

	// Загружаем все progresses одним запросом
	type progressDuration struct {
		GamePassingID  uint
		FinishedAt     *time.Time
		StartedAt      time.Time
		PenaltySeconds int
	}
	var progresses []progressDuration
	if err := s.DB.Table("level_progresses").Select("game_passing_id, finished_at, started_at, penalty_seconds").
		Where("game_passing_id IN ?", func() []uint {
			ids := make([]uint, len(passings))
			for i, p := range passings {
				ids[i] = p.ID
			}
			return ids
		}()).Find(&progresses).Error; err != nil {
		return err
	}

	// Группируем progresses по passing
	durationMap := make(map[uint]time.Duration)
	for _, pr := range progresses {
		if pr.FinishedAt != nil {
			durationMap[pr.GamePassingID] += pr.FinishedAt.Sub(pr.StartedAt) + time.Duration(pr.PenaltySeconds)*time.Second
		}
	}

	// Рассчитываем места
	type passingResult struct {
		ID       uint
		Duration time.Duration
	}
	var results []passingResult
	for _, p := range passings {
		total := durationMap[p.ID]
		results = append(results, passingResult{ID: p.ID, Duration: total})
	}

	// Batch update durations и места через UPDATE с CASE
	if len(results) == 0 {
		return nil
	}

	// Формируем SQL UPDATE с CASE для каждого passing
	// result_duration = CASE id WHEN ? THEN ? ... END
	// place = CASE id WHEN ? THEN ? ... END
	var durationWHENs []string
	var durationArgs []any
	var placeWHENs []string
	var placeArgs []any

	sort.Slice(results, func(i, j int) bool {
		return results[i].Duration < results[j].Duration
	})

	lastPlace := 0
	for i, res := range results {
		durationWHENs = append(durationWHENs, "WHEN ? THEN ?")
		durationArgs = append(durationArgs, res.ID, res.Duration)

		place := i + 1
		if i > 0 && results[i].Duration == results[i-1].Duration {
			place = lastPlace
		}
		lastPlace = place
		placeWHENs = append(placeWHENs, "WHEN ? THEN ?")
		placeArgs = append(placeArgs, res.ID, place)
	}

	var idPlaceholders []string
	for range results {
		idPlaceholders = append(idPlaceholders, "?")
	}

	query := fmt.Sprintf(
		"UPDATE game_passings SET result_duration = CASE id %s ELSE result_duration END, place = CASE id %s ELSE place END WHERE id IN (%s)",
		strings.Join(durationWHENs, " "),
		strings.Join(placeWHENs, " "),
		strings.Join(idPlaceholders, ", "),
	)
	allArgs := append(durationArgs, placeArgs...)
	for _, res := range results {
		allArgs = append(allArgs, res.ID)
	}
	if err := s.DB.WithContext(ctx).Exec(query, allArgs...).Error; err != nil {
		return err
	}
	return nil
}

// analyzeTeamsBehavior — batch-версия: проверяет все команды одним запросом.
// Возвращает map[teamID]suspiciousReason.
func (s *MonitorService) analyzeTeamsBehavior(teamData []teamAggregatedData) map[uint]string {
	// Собираем уникальные teamID и их passingIDs
	type teamPassings struct {
		TeamID     uint
		PassingIDs []uint
	}
	teamMap := make(map[uint]*teamPassings)
	for _, td := range teamData {
		if tp, ok := teamMap[td.TeamID]; ok {
			tp.PassingIDs = append(tp.PassingIDs, td.GamePassingID)
		} else {
			teamMap[td.TeamID] = &teamPassings{TeamID: td.TeamID, PassingIDs: []uint{td.GamePassingID}}
		}
	}

	if len(teamMap) == 0 {
		return nil
	}

	// Собираем все passingIDs для batch-запроса
	var allPassingIDs []uint
	for _, tp := range teamMap {
		allPassingIDs = append(allPassingIDs, tp.PassingIDs...)
	}

	fiveMinAgo := time.Now().Add(-5 * time.Minute)
	var attempts []attemptRecord
	err := s.DB.Table("attempts").
		Select("attempts.level_progress_id, attempts.code, attempts.success, attempts.created_at").
		Joins("JOIN level_progresses ON level_progresses.id = attempts.level_progress_id").
		Where("level_progresses.game_passing_id IN ? AND attempts.created_at >= ?", allPassingIDs, fiveMinAgo).
		Order("attempts.created_at ASC").
		Find(&attempts).Error
	if err != nil {
		return nil
	}
	if len(attempts) == 0 {
		return nil
	}

	// Группируем attempts по passingID
	attemptsByPassing := make(map[uint][]attemptRecord)
	for _, a := range attempts {
		attemptsByPassing[a.PassingID] = append(attemptsByPassing[a.PassingID], a)
	}

	// Группируем passingID по teamID
	passingToTeam := make(map[uint]uint)
	for _, tp := range teamMap {
		for _, pid := range tp.PassingIDs {
			passingToTeam[pid] = tp.TeamID
		}
	}

	// Анализируем подозрительное поведение по passingID
	suspiciousPassings := make(map[uint]string)
	for pid, atts := range attemptsByPassing {
		reason := checkSuspiciousAttempts(atts)
		if reason != "" {
			suspiciousPassings[pid] = reason
		}
	}

	// Группируем подозрительные passing по teamID
	suspiciousMap := make(map[uint]string)
	for pid, reason := range suspiciousPassings {
		teamID := passingToTeam[pid]
		if _, exists := suspiciousMap[teamID]; !exists {
			suspiciousMap[teamID] = reason
		}
	}

	return suspiciousMap
}

// checkSuspiciousAttempts проверяет список попыток на подозрительное поведение.
func checkSuspiciousAttempts(attempts []attemptRecord) string {
	if len(attempts) == 0 {
		return ""
	}

	rate := float64(len(attempts)) / 5.0 // попыток в минуту
	if rate > 10 {
		return fmt.Sprintf("Подозрительная частота: %.1f попыток/мин", rate)
	}

	var lastCode string
	var streak int
	for _, a := range attempts {
		if !a.Success {
			if a.Code == lastCode {
				streak++
				if streak >= 3 {
					return fmt.Sprintf("Брутфорс: код '%s' введён %d раз подряд", a.Code, streak+1)
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
	return ""
}
