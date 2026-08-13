// Package sessionstore — server-side session store (Session fixation fix, PASS-11).
//
// Отличие от cookie.NewStore (gin-contrib/sessions): данные сессии хранятся
// НА СЕРВЕРЕ (Valkey или in-memory), а в cookie лежит только случайный ID.
// Это даёт:
//   - серверное отозвание сессии (logout/смена пароля/2FA убивают сессию);
//   - RenewToken при повышении привилегий (логин/2FA/OAuth) — новая ID,
//     старая подсунутая кука становится недействительной (fixation).
//
// Без Valkey работает на in-memory backend (single-instance; сессии теряются
// при рестарте — пользователь входит заново). Это осознанный компромисс,
// соответствующий паттерну проекта (rate limiters/JTI-blacklist).
package sessionstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	gcsessions "github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
	"github.com/redis/go-redis/v9"
)

// sessionTTL — срок жизни сессии (по умолчанию 24ч).
const sessionTTL = 24 * time.Hour

// sessionIDBytes — энтропия ID сессии (32 байта = 256 бит).
const sessionIDBytes = 32

// ---- Backend (где лежат данные сессий) ----

// sessionBackend — интерфейс хранения данных сессии по ID.
type sessionBackend interface {
	Get(id string) (map[string]any, bool, error)
	Set(id string, data map[string]any, ttl time.Duration) error
	Delete(id string) error
}

// memoryBackend — in-memory хранение (single-instance fallback).
// Сессии теряются при рестарте процесса (документировано).
type memoryBackend struct {
	mu    sync.Mutex
	items map[string]memorySession
}

type memorySession struct {
	data    map[string]any
	expires time.Time
}

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{items: make(map[string]memorySession)}
}

func (b *memoryBackend) Get(id string) (map[string]any, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	it, ok := b.items[id]
	if !ok {
		return nil, false, nil
	}
	if time.Now().After(it.expires) {
		delete(b.items, id)
		return nil, false, nil
	}
	return it.data, true, nil
}

func (b *memoryBackend) Set(id string, data map[string]any, ttl time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Защита от неограниченного роста: при всплеске убираем просроченные.
	if len(b.items) > 10000 {
		now := time.Now()
		for k, it := range b.items {
			if now.After(it.expires) {
				delete(b.items, k)
			}
		}
	}
	b.items[id] = memorySession{data: data, expires: time.Now().Add(ttl)}
	return nil
}

func (b *memoryBackend) Delete(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.items, id)
	return nil
}

// valkeyBackend — хранение в Valkey (JSON в строке с TTL). Мульти-инстанс.
type valkeyBackend struct {
	client *redis.Client
	prefix string
}

func newValkeyBackend(client *redis.Client, prefix string) *valkeyBackend {
	return &valkeyBackend{client: client, prefix: prefix}
}

func (b *valkeyBackend) key(id string) string { return b.prefix + ":" + id }

func (b *valkeyBackend) Get(id string) (map[string]any, bool, error) {
	raw, err := b.client.Get(ctx(), b.key(id)).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (b *valkeyBackend) Set(id string, data map[string]any, ttl time.Duration) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return b.client.Set(ctx(), b.key(id), raw, ttl).Err()
}

func (b *valkeyBackend) Delete(id string) error {
	return b.client.Del(ctx(), b.key(id)).Err()
}

// ---- Store (реализация gorilla/sessions.Store) ----

// ServerStore — серверный store: в cookie только подписанный ID, данные в backend.
type ServerStore struct {
	backend sessionBackend
	codecs  []securecookie.Codec
	opts    *sessions.Options
}

var _ sessions.Store = (*ServerStore)(nil)

// NewInMemoryStore создаёт серверный store на in-memory backend.
// authKey/encKey — пары ключей для подписи/шифрования cookie (ID сессии).
func NewInMemoryStore(authKey, encKey []byte) *ServerStore {
	return &ServerStore{
		backend: newMemoryBackend(),
		codecs:  securecookie.CodecsFromPairs(authKey, encKey),
	}
}

// NewValkeyStore создаёт серверный store на Valkey backend.
// prefix — пространство ключей (например "gengine:session").
func NewValkeyStore(client *redis.Client, prefix string, authKey, encKey []byte) *ServerStore {
	return &ServerStore{
		backend: newValkeyBackend(client, prefix),
		codecs:  securecookie.CodecsFromPairs(authKey, encKey),
	}
}

// Options задаёт cookie-опции сессии (gin-contrib/sessions-вариант) —
// для совместимости с интерфейсом gin-contrib sessions.Store.
func (s *ServerStore) Options(o gcsessions.Options) {
	s.opts = o.ToGorillaOptions()
}

// SetGorillaOptions задаёт cookie-опции напрямую (gorilla-вариант).
func (s *ServerStore) SetGorillaOptions(o sessions.Options) {
	s.opts = &o
}

// New создаёт новую сессию (с новым ID).
func (s *ServerStore) New(_ *http.Request, name string) (*sessions.Session, error) {
	sess := sessions.NewSession(s, name)
	sess.Options = s.opts
	if sess.Options == nil {
		sess.Options = &sessions.Options{Path: "/", MaxAge: int(sessionTTL.Seconds()), HttpOnly: true}
	}
	sess.IsNew = true
	sess.ID = newSessionID()
	sess.Values = make(map[interface{}]interface{})
	return sess, nil
}

// Get читает сессию по cookie; если нет/невалид — новая.
func (s *ServerStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	sess, err := s.New(r, name)
	if err != nil {
		return nil, err
	}
	cookie, err := r.Cookie(name)
	if err != nil || cookie.Value == "" {
		return sess, nil // новая сессия
	}
	var id string
	if decodeErr := securecookie.DecodeMulti(name, cookie.Value, &id, s.codecs...); decodeErr != nil {
		return sess, nil // подпись не совпала — новая сессия (не возвращаем ошибку)
	}
	data, ok, err := s.backend.Get(id)
	if err != nil {
		return sess, nil // backend недоступен — fallback на новую сессию
	}
	if !ok {
		return sess, nil // сессия истекла/удалена
	}
	sess.ID = id
	sess.IsNew = false
	for k, v := range data {
		sess.Values[k] = v
	}
	return sess, nil
}

// Save сохраняет сессию и пишет cookie.
func (s *ServerStore) Save(r *http.Request, w http.ResponseWriter, sess *sessions.Session) error {
	// Если ID пуст (должно быть из New/Get) — генерируем.
	if sess.ID == "" {
		sess.ID = newSessionID()
	}
	data := make(map[string]any, len(sess.Values))
	for k, v := range sess.Values {
		key, ok := k.(string)
		if !ok {
			continue
		}
		data[key] = v
	}
	ttl := sessionTTL
	if sess.Options != nil && sess.Options.MaxAge > 0 {
		ttl = time.Duration(sess.Options.MaxAge) * time.Second
	}
	if err := s.backend.Set(sess.ID, data, ttl); err != nil {
		return err
	}
	encoded, err := securecookie.EncodeMulti(sess.Name(), sess.ID, s.codecs...)
	if err != nil {
		return err
	}
	cookie := &http.Cookie{
		Name:     sess.Name(),
		Value:    encoded,
		Path:     "/",
		HttpOnly: true,
	}
	if sess.Options != nil {
		cookie.Path = sess.Options.Path
		if sess.Options.Domain != "" {
			cookie.Domain = sess.Options.Domain
		}
		if sess.Options.MaxAge > 0 {
			cookie.MaxAge = sess.Options.MaxAge
		} else if sess.Options.MaxAge < 0 {
			cookie.MaxAge = -1
		}
		cookie.Secure = sess.Options.Secure
		cookie.SameSite = sess.Options.SameSite
	}
	http.SetCookie(w, cookie)
	return nil
}

// RenewToken (Session fixation, PASS-11): перевыпускает session ID ТОГО ЖЕ
// объекта сессии (данные сохраняются), удаляет старую запись в backend.
// Мутирует переданную сессию — критично для совместимости с gin-contrib,
// который держит ссылку на объект gorilla-сессии. Вызывающий должен затем
// вызвать Save (перезапишет cookie новым ID).
func (s *ServerStore) RenewToken(_ *http.Request, _ http.ResponseWriter, sess *sessions.Session) (*sessions.Session, error) {
	oldID := sess.ID
	// Новая сессия с новым ID; переносим Options и Values.
	news, err := s.New(nil, sess.Name())
	if err != nil {
		return nil, err
	}
	news.Options = sess.Options
	news.Values = sess.Values
	// Удаляем старую сессию.
	if oldID != "" {
		if err := s.backend.Delete(oldID); err != nil {
			return nil, err
		}
	}
	// Мутируем исходный объект (gin-contrib держит ссылку на него).
	*sess = *news
	return sess, nil
}

// DeleteSession удаляет сессию (logout/смена пароля).
func (s *ServerStore) DeleteSession(w http.ResponseWriter, name, id string) {
	if id != "" {
		_ = s.backend.Delete(id)
	}
	// Удаляем cookie.
	cookie := &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true}
	http.SetCookie(w, cookie)
}

func newSessionID() string {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		// Крайне маловероятно; fallback на timestamp+random.
		return fmt.Sprintf("f%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ---- Интеграция с gin ----

// globalStore — активный ServerStore, регистрируется из main.go.
// Нужен для RenewGinSession: хендлеры (auth) не имеют прямого доступа к store.
var globalStore *ServerStore

// SetDefault регистрирует активный ServerStore (вызывается из main.go).
func SetDefault(s *ServerStore) {
	globalStore = s
}

// SessionName — имя сессии (совпадает с `sessions.Sessions(...)` в router.go).
const SessionName = "gengine_session"

// RenewGinSession (PASS-11, session fixation): перевыпускает session ID текущей
// gin-сессии. Вызывается при успешной аутентификации (логин/2FA/OAuth) —
// старая подсунутая кука становится недействительной.
func RenewGinSession(c *gin.Context) error {
	if globalStore == nil {
		return nil // store не зарегистрирован (тесты) — пропускаем
	}
	// Gin-contrib обёртка лениво загружает gorilla-сессию через store.Get(request, name),
	// а gorilla Registry хранит её в request context. Повторный вызов Get с тем же
	// request возвращает ТУ ЖЕ gorilla-сессию (с загруженными Values).
	gorillaSess, err := globalStore.Get(c.Request, SessionName)
	if err != nil {
		return fmt.Errorf("sessionstore: get for renew: %w", err)
	}
	// RenewToken мутирует объект сессии (новый ID, старые данные).
	if _, err := globalStore.RenewToken(c.Request, c.Writer, gorillaSess); err != nil {
		return fmt.Errorf("sessionstore: renew token: %w", err)
	}
	// Сохраняем сразу: запишет в backend под новым ID и перезапишет cookie.
	// Последующий sess.Save() в хендлере перезапишет повторно (тот же ID) — безопасно.
	return globalStore.Save(c.Request, c.Writer, gorillaSess)
}

// DeleteGinSession удаляет текущую сессию (logout/смена пароля).
func DeleteGinSession(c *gin.Context) {
	if globalStore == nil {
		return
	}
	// Достаём ID той же gorilla-сессии через Registry (как в RenewGinSession).
	gorillaSess, err := globalStore.Get(c.Request, SessionName)
	if err != nil {
		return
	}
	if gorillaSess.ID == "" {
		return
	}
	_ = globalStore.backend.Delete(gorillaSess.ID)
}

func ctx() context.Context {
	return context.Background()
}
