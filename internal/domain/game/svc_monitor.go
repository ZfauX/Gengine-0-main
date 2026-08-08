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

	"github.com/rs/zerolog/log"
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
	repo      MonitorRepository
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

// WithRepository устанавливает репозиторий мониторинга (A-2, pass 31).
func (s *MonitorService) WithRepository(repo MonitorRepository) *MonitorService {
	s.repo = repo
	return s
}

// repoOrDefault возвращает репозиторий или создаёт дефолтный на DB.
func (s *MonitorService) repoOrDefault() MonitorRepository {
	if s.repo == nil {
		return NewGormMonitorRepo(s.DB)
	}
	return s.repo
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
		if len(s.cache) >= maxMonitorCacheSize {
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
		// Удаляем старый элемент списка для того же gameID, чтобы LRU-список не рос
		// бесконечно при повторных обновлениях одного снапшота (утечка памяти).
		if elem, ok := s.cacheKeys[gameID]; ok {
			s.cacheList.Remove(elem)
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

// AttemptRecord — запись об попытке для batch-анализа.
// PassingID мапится из level_progresses.game_passing_id (алиас passing_id в SQL).
type AttemptRecord struct {
	PassingID uint
	Code      string
	Success   bool
	CreatedAt time.Time
}

// GameSnapshot формирует полную сводку по всем прохождениям игры.
// Оптимизированная версия: объединяет 3 SQL-запроса в один.
func (s *MonitorService) GameSnapshot(ctx context.Context, gameID uint) ([]TeamProgress, error) {
	aggregated, err := s.repoOrDefault().AggregateGameSnapshot(ctx, gameID)
	if err != nil {
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
	suspiciousMap := s.analyzeTeamsBehavior(ctx, teamData)

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
// Сериализовано через pg_advisory_xact_lock(gameID): два параллельных финиша
// не должны перезаписывать места из частично-закоммиченного набора (B2).
func (s *MonitorService) CalculateResults(ctx context.Context, gameID uint) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Advisory xact lock сериализует пересчёт по конкретной игре.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(gameID)).Error; err != nil {
			return fmt.Errorf("pg_advisory_xact_lock: %w", err)
		}

		var passings []GamePassing
		if err := tx.Where("game_id = ? AND status = ?", gameID, StatusFinished).Find(&passings).Error; err != nil {
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
		if err := tx.Table("level_progresses").Select("game_passing_id, finished_at, started_at, penalty_seconds").
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

		// Batch update durations и места через отдельные UPDATE (проще и безопаснее)
		if len(results) == 0 {
			return nil
		}

		sort.Slice(results, func(i, j int) bool {
			return results[i].Duration < results[j].Duration
		})

		// Строим оба CASE и список ID в одном цикле (гарантирует синхронный порядок)
		var durationCases []string
		var durationArgs []any
		var placeCases []string
		var placeArgs []any
		var ids []uint

		lastPlace := 0
		for i, res := range results {
			durationCases = append(durationCases, "WHEN ? THEN ?")
			durationArgs = append(durationArgs, res.ID, res.Duration)

			place := i + 1
			if i > 0 && results[i].Duration == results[i-1].Duration {
				place = lastPlace
			}
			lastPlace = place

			placeCases = append(placeCases, "WHEN ? THEN ?")
			placeArgs = append(placeArgs, res.ID, place)
			ids = append(ids, res.ID)
		}

		idPlaceholders := joinPlaceholders(len(results))

		// Первый UPDATE: длительность
		durQuery := fmt.Sprintf(
			"UPDATE game_passings SET result_duration = CASE id %s ELSE result_duration END WHERE id IN (%s)",
			strings.Join(durationCases, " "),
			idPlaceholders,
		)
		allDurationArgs := append(durationArgs, toAnySlice(ids)...)
		if err := tx.Exec(durQuery, allDurationArgs...).Error; err != nil {
			return fmt.Errorf("обновление длительности: %w", err)
		}

		// Второй UPDATE: места
		placeQuery := fmt.Sprintf(
			"UPDATE game_passings SET place = CASE id %s ELSE place END WHERE id IN (%s)",
			strings.Join(placeCases, " "),
			idPlaceholders,
		)
		allPlaceArgs := append(placeArgs, toAnySlice(ids)...)
		if err := tx.Exec(placeQuery, allPlaceArgs...).Error; err != nil {
			return fmt.Errorf("обновление места: %w", err)
		}

		return nil
	})
}

func joinPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ", ")
}

func toAnySlice[T any](s []T) []any {
	result := make([]any, len(s))
	for i, v := range s {
		result[i] = v
	}
	return result
}

// analyzeTeamsBehavior — batch-версия: проверяет все команды одним запросом.
// Возвращает map[teamID]suspiciousReason.
func (s *MonitorService) analyzeTeamsBehavior(ctx context.Context, teamData []teamAggregatedData) map[uint]string {
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
	attempts, err := s.repoOrDefault().ListRecentAttempts(ctx, allPassingIDs, fiveMinAgo)
	if err != nil {
		log.Debug().Err(err).Msg("MonitorService: failed to load recent attempts")
		return nil
	}
	if len(attempts) == 0 {
		return nil
	}

	// Группируем attempts по passingID
	attemptsByPassing := make(map[uint][]AttemptRecord)
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
		reason := CheckSuspiciousAttempts(atts)
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

// CheckSuspiciousAttempts проверяет список попыток на подозрительное поведение.
func CheckSuspiciousAttempts(attempts []AttemptRecord) string {
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
				// streak считает совпадения после первого: 2 совпадения = 3 подряд.
				if streak >= 2 {
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
