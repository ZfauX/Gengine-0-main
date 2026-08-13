// internal/domain/game/monitor_service.go
package game

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gengine-0/internal/pkg/util"

	lru "github.com/hashicorp/golang-lru/v2"
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
// P-3 (pass 39): самописный LRU (cache+list+keys+mu) заменён на thread-safe
// hashicorp/golang-lru — устранён глобальный Lock contention на горячем
// polling-пути и риск гонок при ручном eviction.
type MonitorService struct {
	db       *gorm.DB
	repo     MonitorRepository
	cache    *lru.Cache[uint, *cachedSnapshot]
	cacheTTL time.Duration
	sfGroup  singleflight.Group

	// epochs (MEDIUM #2, PASS-13): счётчик версий снапшота игры. InvalidateCache
	// инкрементирует эпоху; in-flight singleflight-вычисление, завершившееся
	// после инвалидации, видит старую эпоху и НЕ пишет в кэш (иначе stale-данные,
	// прочитанные до мутации, перезаписали бы свежие). Forget не отменяет
	// вычисление — epoch закрывает гонку.
	epochMu sync.Mutex
	epochs  map[uint]uint64
}

type cachedSnapshot struct {
	data      []TeamProgress
	timestamp time.Time
	// json — маршалнутые байты data (F-2, pass 36): чтобы поллер не
	// сериализовал снапшот каждые 5с, когда данные не менялись.
	json []byte
}

const maxMonitorCacheSize = 1000

func NewMonitorService(db *gorm.DB) *MonitorService {
	cache, err := lru.New[uint, *cachedSnapshot](maxMonitorCacheSize)
	if err != nil {
		// Практически недостижимо при size>0.
		log.Error().Err(err).Msg("MonitorService: failed to create LRU cache, using unlimited")
		cache, _ = lru.New[uint, *cachedSnapshot](0)
	}
	return &MonitorService{
		db:       db,
		cache:    cache,
		cacheTTL: 30 * time.Second,
		epochs:   make(map[uint]uint64),
	}
}

// WithRepository устанавливает репозиторий мониторинга (A-2, pass 31).
func (s *MonitorService) WithRepository(repo MonitorRepository) *MonitorService {
	s.repo = repo
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
// P-3 (pass 39): thread-safe LRU — Get() сам промоутит элемент, ручные лок/evict не нужны.
func (s *MonitorService) GetOrFetchSnapshot(ctx context.Context, gameID uint) ([]TeamProgress, error) {
	if cached, ok := s.cache.Get(gameID); ok && time.Since(cached.timestamp) < s.cacheTTL {
		// DEEP-REVIEW MEDIUM #14 (pass 46): возвращаем копию — мутация
		// результата вызывающим не должна портить кэш.
		return copyTeamProgress(cached.data), nil
	}

	// Используем singleflight для группировки одновременных запросов
	key := fmt.Sprintf("snapshot:%d", gameID)
	// MEDIUM #2 (PASS-13): фиксируем эпоху до загрузки — если за время
	// вычисления была инвалидация (эпоха изменилась), результат не пишем.
	epoch := s.epochCurrent(gameID)
	result, err, _ := s.sfGroup.Do(key, func() (any, error) {
		// Повторная проверка кэша (без промоушена) — защита от гонки.
		if cached, ok := s.cache.Peek(gameID); ok && time.Since(cached.timestamp) < s.cacheTTL {
			return cached.data, nil
		}

		// Загрузка из БД
		snapshot, err := s.GameSnapshot(ctx, gameID)
		if err != nil {
			return nil, err
		}

		// F-2 (pass 36): маршалим сразу при загрузке из БД и кэшируем байты —
		// поллер GetOrFetchSnapshotJSON не будет сериализовать каждые 5с.
		jsonData, marshalErr := json.Marshal(snapshot)
		if marshalErr != nil {
			return nil, marshalErr
		}
		// MEDIUM #2 (PASS-13): если во время расчёта была инвалидация — не
		// перезаписываем кэш устаревшими данными (Forget не отменяет in-flight).
		if s.epochCurrent(gameID) != epoch {
			log.Debug().Uint("game_id", gameID).Msg("MonitorService: skipping cache write (epoch changed during compute)")
			return snapshot, nil
		}
		// lru.Add сам вытесняет самый старый элемент при превышении maxMonitorCacheSize.
		s.cache.Add(gameID, &cachedSnapshot{
			data:      snapshot,
			timestamp: time.Now(),
			json:      jsonData,
		})

		return snapshot, nil
	})

	if err != nil {
		return nil, err
	}
	teamProgress, ok := result.([]TeamProgress)
	if !ok {
		return nil, fmt.Errorf("unexpected type for result")
	}
	// DEEP-REVIEW MEDIUM #14 (pass 46): копия на выходе singleflight.
	return copyTeamProgress(teamProgress), nil
}

// copyTeamProgress возвращает копию слайса TeamProgress, чтобы вызывающий не
// мог мутировать кэшированные данные (DEEP-REVIEW MEDIUM #14).
func copyTeamProgress(src []TeamProgress) []TeamProgress {
	if src == nil {
		return nil
	}
	dst := make([]TeamProgress, len(src))
	copy(dst, src)
	return dst
}

// GetOrFetchSnapshotJSON возвращает JSON-представление снапшота, кэшируя
// маршалнутые байты вместе с данными (F-2, pass 36). Поллер SSE вызывает
// именно его, чтобы не сериализовать полный снапшот каждые 5с.
func (s *MonitorService) GetOrFetchSnapshotJSON(ctx context.Context, gameID uint) ([]byte, error) {
	// P-3 (pass 39): thread-safe LRU — Get() промоутит элемент без ручных локов.
	if cached, ok := s.cache.Get(gameID); ok && time.Since(cached.timestamp) < s.cacheTTL {
		if cached.json != nil {
			bytes := cached.json
			return bytes, nil
		}
	}

	// Кэш устарел или json ещё не заполнен (старая запись) — загружаем.
	snapshot, err := s.GetOrFetchSnapshot(ctx, gameID)
	if err != nil {
		return nil, err
	}

	// P-4 (pass 37): GetOrFetchSnapshot мог вернуть данные из кэша-хита без
	// заполнения json (старая запись) — тогда cached.json == nil и метод
	// вернул бы (nil, nil). Маршалим сами и обновляем кэш.
	if cached, ok := s.cache.Get(gameID); ok && cached.json != nil {
		return cached.json, nil
	}
	jsonData, marshalErr := json.Marshal(snapshot)
	if marshalErr != nil {
		return nil, marshalErr
	}
	// H-1 (pass 40): НЕ мутируем cached.json — на объект ссылаются другие
	// читатели LRU (data race). Добавляем целиком новое значение.
	// P-43-10 (pass 43): если запись эвиктнута между Get и Peek — не падаем
	// с ошибкой (SSE-клиент получил бы 500); возвращаем данные без кэша.
	if cached, ok := s.cache.Peek(gameID); ok {
		s.cache.Add(gameID, &cachedSnapshot{
			data:      cached.data,
			timestamp: cached.timestamp,
			json:      jsonData,
		})
	}
	return jsonData, nil
}

// InvalidateCache удаляет кэшированный снимок игры (вызывается при изменениях).
func (s *MonitorService) InvalidateCache(gameID uint) {
	s.cache.Remove(gameID)
	// P-4 (pass 39): форсим singleflight — иначе пересчёт, начатый до
	// инвалидации, перезапишет кэш данными, прочитанными до изменений.
	s.sfGroup.Forget(fmt.Sprintf("snapshot:%d", gameID))
	// MEDIUM #2 (PASS-13): Forget не отменяет in-flight вычисление. Инкремент
	// эпохи заставляет его пропустить запись в кэш (см. epochCurrent в Do).
	s.epochMu.Lock()
	s.epochs[gameID]++
	s.epochMu.Unlock()
}

// epochCurrent возвращает текущую эпоху снапшота игры (0 если не инвалидировался).
func (s *MonitorService) epochCurrent(gameID uint) uint64 {
	s.epochMu.Lock()
	defer s.epochMu.Unlock()
	return s.epochs[gameID]
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
	aggregated, err := s.repo.AggregateGameSnapshot(ctx, gameID)
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
		// DEEP-REVIEW MEDIUM #15 (pass 46): при рассинхроне таймстампов
		// (clock skew, пропущенный уровень) длительность может быть
		// отрицательной — клампим к 0, чтобы не показывать мусор.
		if totalDuration < 0 {
			totalDuration = 0
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
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
				// M5 (PASS-8): клампим каждую составляющую к >=0 (единая семантика
				// с GameSnapshot, DEEP-REVIEW MEDIUM #15). Раньше clock-skew
				// (FinishedAt раньше StartedAt) давал отрицательную сумму в БД.
				d := pr.FinishedAt.Sub(pr.StartedAt)
				if d < 0 {
					d = 0
				}
				penalty := time.Duration(pr.PenaltySeconds) * time.Second
				if penalty < 0 {
					penalty = 0
				}
				durationMap[pr.GamePassingID] += d + penalty
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

		idPlaceholders := util.JoinPlaceholders(len(results))

		// Первый UPDATE: длительность
		durQuery := fmt.Sprintf(
			"UPDATE game_passings SET result_duration = CASE id %s ELSE result_duration END WHERE id IN (%s)",
			strings.Join(durationCases, " "),
			idPlaceholders,
		)
		allDurationArgs := append(durationArgs, util.ToAnySlice(ids)...)
		if err := tx.Exec(durQuery, allDurationArgs...).Error; err != nil {
			return fmt.Errorf("обновление длительности: %w", err)
		}

		// Второй UPDATE: места
		placeQuery := fmt.Sprintf(
			"UPDATE game_passings SET place = CASE id %s ELSE place END WHERE id IN (%s)",
			strings.Join(placeCases, " "),
			idPlaceholders,
		)
		allPlaceArgs := append(placeArgs, util.ToAnySlice(ids)...)
		if err := tx.Exec(placeQuery, allPlaceArgs...).Error; err != nil {
			return fmt.Errorf("обновление места: %w", err)
		}

		return nil
	})
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
	attempts, err := s.repo.ListRecentAttempts(ctx, allPassingIDs, fiveMinAgo)
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
