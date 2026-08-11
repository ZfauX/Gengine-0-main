// internal/pkg/rolecache/rolecache.go
// DEEP-REVIEW PASS-3 M9: единый TTL-кэш ролей пользователей.
//
// Раньше роль кэшировалась в ДВУХ независимых местах с разными TTL и раздельной
// инвалидацией:
//   - internal/pkg/middleware/auth.go (TTL 5с);
//   - internal/domain/game/svc_coauthor.go (TTL 15с).
//
// При смене роли в админке нужно было вызвать оба InvalidateRoleCache — если
// вызывался только один, второй до истечения TTL продолжал считать пониженного
// админа админом (окно неконсистентных прав). Теперь оба потребителя используют
// этот пакет с единым TTL и одной инвалидацией.
package rolecache

import (
	"context"
	"sync"
	"time"
)

// TTL — время жизни записи роли. Выбрано 5с (минимальное из прежних) —
// понижение/удаление роли применяется быстрее, БД не бомбардируется SELECT role.
const TTL = 5 * time.Second

// maxEntries — верхняя граница размера кэша (lazy sweep, P-2 паттерн).
const maxEntries = 512

// Provider возвращает актуальную роль пользователя из источника (БД).
type Provider func(ctx context.Context, userID uint) (string, error)

type entry struct {
	role    string
	expires time.Time
}

// Cache — потокобезопасный TTL-кэш ролей.
type Cache struct {
	mu    sync.RWMutex
	items map[uint]entry
}

// New создаёт кэш ролей.
func New() *Cache {
	return &Cache{items: make(map[uint]entry)}
}

// Get возвращает роль пользователя: из кэша (если свежая) либо из provider
// (результат кэшируется). Ошибки (в т.ч. «пользователь не найден») НЕ
// кэшируются — удалённый пользователь отзывается немедленно при следующем промахе.
func (c *Cache) Get(ctx context.Context, userID uint, provider Provider) (string, error) {
	if provider == nil {
		return "", nil
	}
	now := time.Now()

	c.mu.RLock()
	if e, ok := c.items[userID]; ok && now.Before(e.expires) {
		c.mu.RUnlock()
		return e.role, nil
	}
	c.mu.RUnlock()

	role, err := provider(ctx, userID)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	// Lazy sweep: не даём map расти неограниченно.
	if len(c.items) > maxEntries {
		for id, e := range c.items {
			if !now.Before(e.expires) {
				delete(c.items, id)
			}
		}
	}
	c.items[userID] = entry{role: role, expires: now.Add(TTL)}
	c.mu.Unlock()
	return role, nil
}

// Invalidate сбрасывает кэш роли пользователя. Вызывается после смены роли в
// админке — понижение/повышение применяется без ожидания TTL.
func (c *Cache) Invalidate(userID uint) {
	c.mu.Lock()
	delete(c.items, userID)
	c.mu.Unlock()
}

// InvalidateAll сбрасывает весь кэш (используется при массовых сменах ролей).
func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	c.items = make(map[uint]entry)
	c.mu.Unlock()
}

// Len возвращает текущее число записей (для тестов/метрик).
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}
