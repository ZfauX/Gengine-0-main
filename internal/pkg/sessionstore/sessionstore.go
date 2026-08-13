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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	gcsessions "github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
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

// typedValue — типизированная сериализация значения в Valkey (JSON).
// Стандартный json.Unmarshal в map[string]any превращает uint/int64/float в
// float64, что ломает сессии (pending_user_id.(uint) и т.п.) — см. известное
// ограничение с *Game в cache. Храним JSON-массив [тип, значение].
type typedValue struct {
	T string `json:"t"`
	V string `json:"v"`
}

func encodeTypedValue(v any) typedValue {
	switch val := v.(type) {
	case uint:
		return typedValue{T: "uint", V: strconv.FormatUint(uint64(val), 10)}
	case uint64:
		return typedValue{T: "uint64", V: strconv.FormatUint(val, 10)}
	case int:
		return typedValue{T: "int", V: strconv.Itoa(val)}
	case int64:
		return typedValue{T: "int64", V: strconv.FormatInt(val, 10)}
	case string:
		return typedValue{T: "string", V: val}
	case bool:
		return typedValue{T: "bool", V: strconv.FormatBool(val)}
	case float64:
		return typedValue{T: "float64", V: strconv.FormatFloat(val, 'g', -1, 64)}
	case time.Time:
		return typedValue{T: "time", V: val.Format(time.RFC3339Nano)}
	case []byte:
		return typedValue{T: "bytes", V: base64.StdEncoding.EncodeToString(val)}
	case nil:
		return typedValue{T: "nil", V: ""}
	default:
		// Неизвестный тип (например struct) — JSON-сериализация.
		raw, err := json.Marshal(val)
		if err != nil {
			return typedValue{T: "nil", V: ""}
		}
		return typedValue{T: "json", V: string(raw)}
	}
}

func (tv typedValue) Decode() (any, error) {
	switch tv.T {
	case "uint":
		v, err := strconv.ParseUint(tv.V, 10, 64)
		if err != nil {
			return nil, err
		}
		return uint(v), nil
	case "uint64":
		return strconv.ParseUint(tv.V, 10, 64)
	case "int":
		v, err := strconv.Atoi(tv.V)
		if err != nil {
			return nil, err
		}
		return v, nil
	case "int64":
		return strconv.ParseInt(tv.V, 10, 64)
	case "string":
		return tv.V, nil
	case "bool":
		return strconv.ParseBool(tv.V)
	case "float64":
		return strconv.ParseFloat(tv.V, 64)
	case "time":
		return time.Parse(time.RFC3339Nano, tv.V)
	case "bytes":
		return base64.StdEncoding.DecodeString(tv.V)
	case "nil":
		return nil, nil
	case "json":
		var v any
		if err := json.Unmarshal([]byte(tv.V), &v); err != nil {
			return nil, err
		}
		return v, nil
	default:
		return nil, fmt.Errorf("sessionstore: unknown typed value type %q", tv.T)
	}
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
	var typed map[string]typedValue
	if err := json.Unmarshal(raw, &typed); err != nil {
		return nil, false, err
	}
	data := make(map[string]any, len(typed))
	for k, tv := range typed {
		v, err := tv.Decode()
		if err != nil {
			return nil, false, err
		}
		data[k] = v
	}
	return data, true, nil
}

func (b *valkeyBackend) Set(id string, data map[string]any, ttl time.Duration) error {
	typed := make(map[string]typedValue, len(data))
	for k, v := range data {
		typed[k] = encodeTypedValue(v)
	}
	raw, err := json.Marshal(typed)
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

// New создаёт новую сессию. ID НЕ генерируется сразу (M5, PASS-13): для
// невалидной/отсутствующей куки Get вернёт сессию с пустым ID, и запись в
// backend появится ТОЛЬКО при реальном Save (когда приложение что-то записало).
// Иначе каждая попытка с мусорной кукой создавала бы новую запись с TTL 24ч.
func (s *ServerStore) New(_ *http.Request, name string) (*sessions.Session, error) {
	sess := sessions.NewSession(s, name)
	sess.Options = s.opts
	if sess.Options == nil {
		sess.Options = &sessions.Options{Path: "/", MaxAge: int(sessionTTL.Seconds()), HttpOnly: true}
	}
	sess.IsNew = true
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

// oldIDKey — служебный ключ в Values, хранящий ID предыдущей сессии при
// RenewToken (M2, PASS-13). Save извлекает и удаляет его перед записью,
// затем удаляет старую запись в backend после успешного сохранения новой.
const oldIDKey = "__session_old_id__"

// Save сохраняет сессию и пишет cookie.
func (s *ServerStore) Save(r *http.Request, w http.ResponseWriter, sess *sessions.Session) error {
	// Если ID пуст (должно быть из New/Get) — генерируем.
	if sess.ID == "" {
		sess.ID = newSessionID()
	}

	// M2 (PASS-13): старая сессия (от RenewToken) удаляется ТОЛЬКО после
	// успешной записи новой — при сбое backend старые данные не теряются.
	var oldID string
	if v, ok := sess.Values[oldIDKey]; ok {
		if str, strOK := v.(string); strOK {
			oldID = str
		}
		delete(sess.Values, oldIDKey)
	}

	// M1 (PASS-13): MaxAge < 0 — семантика gorilla «удалить куку» (logout/clear).
	// Раньше cookie получала MaxAge=-1, но backend-запись жила полный TTL (24ч):
	// данные сессии оставались на сервере после «удаления». Теперь удаляем
	// запись из backend и не пишем новую.
	if sess.Options != nil && sess.Options.MaxAge < 0 {
		_ = s.backend.Delete(sess.ID)
		if oldID != "" && oldID != sess.ID {
			_ = s.backend.Delete(oldID)
		}
		cookie := &http.Cookie{
			Name:     sess.Name(),
			Value:    "",
			Path:     sess.Options.Path,
			Domain:   sess.Options.Domain,
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   sess.Options.Secure,
			SameSite: sess.Options.SameSite,
		}
		http.SetCookie(w, cookie)
		return nil
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
	// Новая сессия записана — теперь можно безопасно удалить старую.
	if oldID != "" && oldID != sess.ID {
		if err := s.backend.Delete(oldID); err != nil {
			log.Warn().Err(err).Msg("sessionstore: failed to delete old session after renew")
		}
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
		}
		cookie.Secure = sess.Options.Secure
		cookie.SameSite = sess.Options.SameSite
	}
	http.SetCookie(w, cookie)
	return nil
}

// RenewToken (Session fixation, PASS-11): перевыпускает session ID ТОГО ЖЕ
// объекта сессии (данные сохраняются). Мутирует переданную сессию — критично
// для совместимости с gin-contrib, который держит ссылку на объект
// gorilla-сессии. Вызывающий должен затем вызвать Save (перезапишет cookie).
//
// M2 (PASS-13): старая сессия удаляется из backend ТОЛЬКО после успешной
// записи новой (в Save). Раньше Delete выполнялся здесь ДО Save — при сбое
// backend данные сессии терялись без возможности отката.
func (s *ServerStore) RenewToken(_ *http.Request, _ http.ResponseWriter, sess *sessions.Session) (*sessions.Session, error) {
	oldID := sess.ID
	// Новая сессия с новым ID; переносим Options и Values.
	news, err := s.New(nil, sess.Name())
	if err != nil {
		return nil, err
	}
	// RenewToken обязан выдать новый ID (fixation) — генерируем явно, т.к.
	// New теперь откладывает генерацию до Save (M5).
	news.ID = newSessionID()
	news.IsNew = true
	news.Options = sess.Options
	news.Values = sess.Values
	// Запоминаем старый ID (Save удалит его после успешной записи новой).
	if oldID != "" && oldID != news.ID {
		news.Values[oldIDKey] = oldID
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
