// internal/domain/user/profile_service.go
package user

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// ErrEmailTaken — email уже занят другим пользователем.
var ErrEmailTaken = errors.New("email уже используется другим пользователем")

// UserStats содержит статистику для публичного профиля.
type UserStats struct {
	GamesCreated int64
	GamesPlayed  int64
	Wins         int64
	Rating       int
}

// RecentGame содержит данные о последней игре.
type RecentGame struct {
	ID        uint
	Name      string
	IsDraft   bool
	CoverPath string
	CreatedAt string
}

// ProfileService отвечает за статистику и данные публичного профиля.
// A-2 (pass 35): данные через ProfileRepository, а не raw *gorm.DB.
// DEEP-REVIEW PASS-4 M11: статистика кэшируется на 60с (счётчики меняются
// редко — только при финише игры); публичный профиль просматривается часто.
type ProfileService struct {
	repo ProfileRepository

	statsMu    sync.Mutex
	statsCache map[uint]statsCacheEntry

	// gamesCache (PASS-5 P3): кэш последних игр (60с) — данные меняются только
	// при публикации; профиль-страница делает лишний запрос на каждый просмотр.
	gamesMu        sync.Mutex
	gamesCache     map[uint]gamesCacheEntry
	gamesLastSweep time.Time

	// gamesViewCache (P-H1, PASS-8): кэш предпочтения вида списка игр (60с).
	// Раньше GetGamesView делал отдельный SELECT на каждый просмотр /games —
	// самый горячий авторизованный маршрут. Значение меняется редко (SaveGamesView).
	gamesViewMu    sync.Mutex
	gamesViewCache map[uint]gamesViewCacheEntry

	// sfGroup (PASS-6 P5): singleflight для stats/games — на TTL-истечении
	// популярного профиля не все запросы бьют в БД одновременно (stampede).
	sfGroup singleflight.Group
}

type statsCacheEntry struct {
	stats   UserStats
	expires time.Time
}

type gamesCacheEntry struct {
	games   []RecentGame
	expires time.Time
}

// gamesViewCacheEntry (P-H1, PASS-8): значение + TTL.
type gamesViewCacheEntry struct {
	view    string
	expires time.Time
}

// statsCacheTTL — время жизни кэша статистики профиля и последних игр.
const statsCacheTTL = 60 * time.Second

// NewProfileService создаёт новый ProfileService.
func NewProfileService(repo ProfileRepository) *ProfileService {
	return &ProfileService{
		repo:           repo,
		statsCache:     make(map[uint]statsCacheEntry),
		gamesCache:     make(map[uint]gamesCacheEntry),
		gamesViewCache: make(map[uint]gamesViewCacheEntry),
	}
}

// GetPublicProfileStats загружает статистику пользователя (с TTL-кэшем, M11).
// PF-3 (pass 29): 3 COUNT + rating ранее были 4 round-trip; теперь один
// запрос с агрегатами через подзапросы.
func (s *ProfileService) GetPublicProfileStats(ctx context.Context, userID uint) (*UserStats, error) {
	now := time.Now()
	s.statsMu.Lock()
	if e, ok := s.statsCache[userID]; ok && now.Before(e.expires) {
		stats := e.stats
		s.statsMu.Unlock()
		return &stats, nil
	}
	s.statsMu.Unlock()

	// P5 (PASS-6): singleflight — на TTL-истечении только один запрос к БД.
	sfCtx := context.WithoutCancel(ctx)
	sfKey := fmt.Sprintf("stats:%d", userID)
	val, sfErr, _ := s.sfGroup.Do(sfKey, func() (any, error) {
		return s.repo.GetPublicProfileStats(sfCtx, userID)
	})
	if sfErr != nil {
		return nil, sfErr
	}
	stats, ok := val.(*UserStats)
	if !ok {
		return nil, fmt.Errorf("profile: unexpected stats type")
	}

	s.statsMu.Lock()
	// Lazy sweep: не даём map расти неограниченно.
	if len(s.statsCache) > 512 {
		for id, e := range s.statsCache {
			if !now.After(e.expires) {
				continue
			}
			delete(s.statsCache, id)
		}
	}
	s.statsCache[userID] = statsCacheEntry{stats: *stats, expires: now.Add(statsCacheTTL)}
	s.statsMu.Unlock()
	return stats, nil
}

// InvalidateProfileStats сбрасывает кэш статистики пользователя.
// NOTE (PASS-5 M1): сейчас не вызывается из game-домена (нет DI-связи) —
// используется осознанный TTL 60с; метод оставлен как точка интеграции,
// если понадобится мгновенная инвалидация при финише игры/обновлении рейтинга.
func (s *ProfileService) InvalidateProfileStats(userID uint) {
	s.statsMu.Lock()
	delete(s.statsCache, userID)
	s.statsMu.Unlock()
}

// IsFollowing проверяет, подписан ли пользователь на другого.
func (s *ProfileService) IsFollowing(ctx context.Context, followerID, authorID uint) (bool, error) {
	return s.repo.IsFollowing(ctx, followerID, authorID)
}

// GetRecentGames загружает последние игры автора (с TTL-кэшем, PASS-5 P3).
func (s *ProfileService) GetRecentGames(ctx context.Context, authorID uint) ([]RecentGame, error) {
	now := time.Now()
	s.gamesMu.Lock()
	if e, ok := s.gamesCache[authorID]; ok && now.Before(e.expires) {
		// M1 (PASS-6): возвращаем КОПИЮ — consumer мог бы мутировать слайс
		// кэша (data race на общем slice).
		games := append([]RecentGame(nil), e.games...)
		s.gamesMu.Unlock()
		return games, nil
	}
	s.gamesMu.Unlock()

	// P5 (PASS-6): singleflight для games.
	sfCtx := context.WithoutCancel(ctx)
	sfKey := fmt.Sprintf("games:%d", authorID)
	val, sfErr, _ := s.sfGroup.Do(sfKey, func() (any, error) {
		return s.repo.GetRecentGames(sfCtx, authorID)
	})
	if sfErr != nil {
		return nil, sfErr
	}
	games, ok := val.([]RecentGame)
	if !ok {
		return nil, fmt.Errorf("profile: unexpected games type")
	}

	s.gamesMu.Lock()
	// H2 (PASS-6): sweep не чаще 1/с — при активном сайте map не платит O(n)
	// под блокировкой на каждый промах.
	if len(s.gamesCache) > 512 {
		if now.Sub(s.gamesLastSweep) >= time.Second {
			s.gamesLastSweep = now
			for id, e := range s.gamesCache {
				if now.After(e.expires) {
					delete(s.gamesCache, id)
				}
			}
		}
	}
	s.gamesCache[authorID] = gamesCacheEntry{games: games, expires: now.Add(statsCacheTTL)}
	s.gamesMu.Unlock()
	return games, nil
}

// UpdateProfile обновляет имя и email пользователя.
func (s *ProfileService) UpdateProfile(ctx context.Context, userID uint, name, email string) error {
	return s.repo.UpdateProfile(ctx, userID, name, email)
}

// GetThemeSettings возвращает настройки темы пользователя (с дефолтами при пустой записи).
func (s *ProfileService) GetThemeSettings(ctx context.Context, userID uint) (ThemeSettings, error) {
	return s.repo.GetThemeSettings(ctx, userID)
}

// SaveThemeSettings сохраняет настройки темы пользователя.
func (s *ProfileService) SaveThemeSettings(ctx context.Context, userID uint, ts ThemeSettings) error {
	return s.repo.SaveThemeSettings(ctx, userID, ts)
}

// GetGamesView возвращает сохранённое предпочтение вида списка игр.
// P-H1 (PASS-8): TTL-кэш 60с — раньше отдельный SELECT на каждый просмотр /games.
func (s *ProfileService) GetGamesView(ctx context.Context, userID uint) (string, error) {
	now := time.Now()
	s.gamesViewMu.Lock()
	if e, ok := s.gamesViewCache[userID]; ok && now.Before(e.expires) {
		s.gamesViewMu.Unlock()
		return e.view, nil
	}
	s.gamesViewMu.Unlock()

	view, err := s.repo.GetGamesView(ctx, userID)
	if err != nil {
		return "table", err
	}
	s.gamesViewMu.Lock()
	s.gamesViewCache[userID] = gamesViewCacheEntry{view: view, expires: now.Add(statsCacheTTL)}
	s.gamesViewMu.Unlock()
	return view, nil
}

// SaveGamesView сохраняет предпочтение вида списка игр.
// M6 (PASS-4): allowlist — раньше сохранялась любая строка (>10 символов давала
// 500 на каждый запрос, self-DoS; произвольное значение в varchar(10)).
func (s *ProfileService) SaveGamesView(ctx context.Context, userID uint, view string) error {
	if view != "table" && view != "cards" {
		return fmt.Errorf("недопустимое значение вида списка игр")
	}
	if err := s.repo.SaveGamesView(ctx, userID, view); err != nil {
		return err
	}
	// P-H1 (PASS-8): инвалидируем кэш при сохранении.
	s.gamesViewMu.Lock()
	delete(s.gamesViewCache, userID)
	s.gamesViewMu.Unlock()
	return nil
}

// GetNotifyGameDays возвращает период уведомлений о предстоящих играх (D-1).
func (s *ProfileService) GetNotifyGameDays(ctx context.Context, userID uint) (int, error) {
	return s.repo.GetNotifyGameDays(ctx, userID)
}

func (s *ProfileService) SaveNotifyGameDays(ctx context.Context, userID uint, days int) error {
	return s.repo.SaveNotifyGameDays(ctx, userID, days)
}
