package middleware

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type RateLimitResult struct {
	Allowed   bool
	Limit     int
	Remaining int
	ResetUnix int64
}

type RateLimiterStore interface {
	Allow(key string) RateLimitResult
	Stop()
}

type inMemoryShard struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	dirty    int32
}

const shardCount = 16

type inMemoryStore struct {
	shards      [shardCount]*inMemoryShard
	window      time.Duration
	limit       int
	stopCh      chan struct{}
	cleanupOnce sync.Once
	stopOnce    sync.Once
}

type visitor struct {
	lastSeen time.Time
	count    int
}

func newInMemoryStore(window time.Duration, limit int) *inMemoryStore {
	s := &inMemoryStore{
		window: window,
		limit:  limit,
		stopCh: make(chan struct{}),
	}
	for i := 0; i < shardCount; i++ {
		s.shards[i] = &inMemoryShard{
			visitors: make(map[string]*visitor),
		}
	}
	return s
}

func (s *inMemoryStore) Allow(key string) RateLimitResult {
	s.cleanupOnce.Do(func() {
		go s.cleanupLoop()
	})

	shard := s.getShard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	v, exists := shard.visitors[key]
	now := time.Now()

	if !exists || now.Sub(v.lastSeen) > s.window {
		shard.visitors[key] = &visitor{lastSeen: now, count: 1}
		atomic.StoreInt32(&shard.dirty, 1)
		return RateLimitResult{Allowed: true, Limit: s.limit, Remaining: s.limit - 1, ResetUnix: now.Add(s.window).Unix()}
	}

	if v.count >= s.limit {
		return RateLimitResult{Allowed: false, Limit: s.limit, Remaining: 0, ResetUnix: v.lastSeen.Add(s.window).Unix()}
	}

	v.lastSeen = now
	v.count++

	return RateLimitResult{Allowed: true, Limit: s.limit, Remaining: s.limit - v.count, ResetUnix: v.lastSeen.Add(s.window).Unix()}
}

func (s *inMemoryStore) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *inMemoryStore) cleanupLoop() {
	interval := time.Minute
	if s.window > 0 && s.window/4 < interval {
		interval = s.window / 4
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			for _, shard := range s.shards {
				if atomic.LoadInt32(&shard.dirty) == 0 {
					continue
				}
				shard.mu.Lock()
				now := time.Now()
				for key, v := range shard.visitors {
					if now.Sub(v.lastSeen) > s.window {
						delete(shard.visitors, key)
					}
				}
				if len(shard.visitors) == 0 {
					atomic.StoreInt32(&shard.dirty, 0)
				}
				shard.mu.Unlock()
			}
		}
	}
}

func (s *inMemoryStore) getShard(key string) *inMemoryShard {
	f := fnv.New32a()
	f.Write([]byte(key))
	return s.shards[f.Sum32()%shardCount]
}

var rateLimitLua = redis.NewScript(`
	local key = KEYS[1]
	local limit = tonumber(ARGV[1])
	local window = tonumber(ARGV[2])

	local count = redis.call('INCR', key)
	if count == 1 then
		redis.call('EXPIRE', key, window)
	end

	if count <= limit then
		return {1, count, limit - count}
	else
		local ttl = redis.call('TTL', key)
		if ttl < 0 then ttl = 0 end
		return {0, count, 0, ttl}
	end
`)

type valkeyStore struct {
	client *redis.Client
	window time.Duration
	limit  int
	// failClosed (S-46-1, pass 46): при недоступности Valkey отклонять запросы
	// (Allows=false → 429), а не пропускать. Используется для критичных лимитеров
	// (логин, регистрация, 2FA, коды, сброс пароля), где fail-open отключает
	// защиту от брутфорса при outage кэша. Глобальный/API-лимитеры остаются
	// fail-open — их задача защищать от флуда, а не от подбора учётных данных.
	failClosed bool
}

func newValkeyStore(client *redis.Client, window time.Duration, limit int) *valkeyStore {
	return &valkeyStore{client: client, window: window, limit: limit}
}

func (s *valkeyStore) Allow(key string) RateLimitResult {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	windowSec := int64(s.window.Seconds())
	if windowSec < 1 {
		windowSec = 1
	}
	resetUnix := time.Now().Add(s.window).Unix()

	result, err := rateLimitLua.Run(ctx, s.client, []string{key}, s.limit, windowSec).Result()
	if err != nil {
		if s.failClosed {
			// S-46-1 (pass 46): fail-closed — при сбое Valkey блокируем запрос.
			// Осознанный trade-off для критичных лимитеров: outage кэша временно
			// отключает вход/регистрацию, но не открывает брутфорс без лимита.
			log.Error().Err(err).Str("key", key).Msg("valkey: Allow check failed, rejecting request (fail-closed)")
			return RateLimitResult{Allowed: false, Limit: s.limit, Remaining: 0, ResetUnix: resetUnix}
		}
		// P3: fail-open при сбое Valkey. Если Redis недоступен, каждый запрос
		// получал 429 (все лимитеры — глобальный, логин, API — держатся на нём),
		// т.е. outage кэша превращался в полный outage сайта. Доступность
		// важнее строгого лимита при кратковременном сбое.
		log.Error().Err(err).Str("key", key).Msg("valkey: Allow check failed, allowing request (fail-open)")
		return RateLimitResult{Allowed: true, Limit: s.limit, Remaining: s.limit, ResetUnix: resetUnix}
	}

	vals, ok := result.([]any)
	if !ok || len(vals) < 3 {
		if s.failClosed {
			log.Error().Str("key", key).Interface("result", result).Msg("valkey: unexpected script result, rejecting request (fail-closed)")
			return RateLimitResult{Allowed: false, Limit: s.limit, Remaining: 0, ResetUnix: resetUnix}
		}
		log.Error().Str("key", key).Interface("result", result).Msg("valkey: unexpected script result, allowing request (fail-open)")
		return RateLimitResult{Allowed: true, Limit: s.limit, Remaining: s.limit, ResetUnix: resetUnix}
	}

	allowed, _ := vals[0].(int64)
	remaining, _ := vals[2].(int64)
	if remaining < 0 {
		remaining = 0
	}

	return RateLimitResult{
		Allowed:   allowed == 1,
		Limit:     s.limit,
		Remaining: int(remaining),
		ResetUnix: resetUnix,
	}
}

func (s *valkeyStore) Stop() {}

type RateLimiter struct {
	store RateLimiterStore
}

func NewRateLimiter(window time.Duration, limit int) *RateLimiter {
	return &RateLimiter{store: newInMemoryStore(window, limit)}
}

func NewValkeyRateLimiter(client *redis.Client, window time.Duration, limit int) *RateLimiter {
	return &RateLimiter{store: newValkeyStore(client, window, limit)}
}

// ---------- S-M2 (PASS-8): shared Valkey-клиент для per-user лимитеров ----------

// sharedValkeyClient — общий Valkey-клиент, устанавливается из main.go, когда
// Valkey доступен. Per-user лимитеры (личный чат, создание комнат, поиск,
// WebAuthn, платежи) используют его для меж-инстансной координации бюджета;
// при отсутствии — работают на in-memory (single-instance).
var sharedValkeyClient *redis.Client

// SetSharedValkeyClient (S-M2, PASS-8) регистрирует общий Valkey-клиент для
// per-user лимитеров. Вызывается из main.go при успешном подключении к Valkey.
// Безопасно вызывать до регистрации маршрутов (middleware создают лимитеры
// при первом вызове фабрики).
func SetSharedValkeyClient(client *redis.Client) {
	sharedValkeyClient = client
}

// newSharedLimiter (S-M2, PASS-8): фабрика для per-user лимитеров — Valkey,
// если клиент зарегистрирован, иначе in-memory. Работает БЕЗ Valkey (fallback),
// лимиты просто per-instance.
func newSharedLimiter(window time.Duration, limit int) *RateLimiter {
	if sharedValkeyClient != nil {
		return NewValkeyRateLimiter(sharedValkeyClient, window, limit)
	}
	return NewRateLimiter(window, limit)
}

// NewSharedLimiterForTest (S-M2, PASS-8): экспортируемая обёртка для unit-тестов.
func NewSharedLimiterForTest(window time.Duration, limit int) *RateLimiter {
	return newSharedLimiter(window, limit)
}

// NewValkeyRateLimiterFailClosed создаёт Valkey-лимитер с fail-closed поведением:
// при недоступности Valkey запросы отклоняются (S-46-1, pass 46). Используется
// для критичных лимитеров (логин, регистрация, 2FA, коды, сброс пароля).
func NewValkeyRateLimiterFailClosed(client *redis.Client, window time.Duration, limit int) *RateLimiter {
	return &RateLimiter{store: &valkeyStore{client: client, window: window, limit: limit, failClosed: true}}
}

func (rl *RateLimiter) Allow(key string) RateLimitResult {
	return rl.store.Allow(key)
}

func (rl *RateLimiter) Stop() {
	rl.store.Stop()
}

func setRateLimitHeaders(c *gin.Context, result RateLimitResult) {
	c.Header("X-RateLimit-Limit", strconv.Itoa(result.Limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(result.ResetUnix, 10))
	if !result.Allowed {
		retryAfter := int(time.Until(time.Unix(result.ResetUnix, 0)).Seconds())
		if retryAfter < 0 {
			retryAfter = 0
		}
		c.Header("Retry-After", strconv.Itoa(retryAfter))
	}
}

func respondRateLimitError(c *gin.Context, message error, result RateLimitResult) {
	setRateLimitHeaders(c, result)
	if strings.Contains(c.GetHeader("Accept"), "text/html") {
		c.HTML(http.StatusTooManyRequests, "errors-429.html", gin.H{"Error": message.Error()})
		c.Abort()
		return
	}
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": message.Error()})
}

var (
	globalRateLimiter *RateLimiter
	globalRLOnce      sync.Once
)

func InitGlobalRateLimiter(window time.Duration, limit int) {
	globalRLOnce.Do(func() {
		globalRateLimiter = NewRateLimiter(window, limit)
	})
}

func InitGlobalRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	globalRLOnce.Do(func() {
		globalRateLimiter = NewValkeyRateLimiter(client, window, limit)
	})
}

func StopGlobalRateLimiter() {
	if globalRateLimiter != nil {
		globalRateLimiter.Stop()
	}
}

func GlobalRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	// Лимит не настроен (<=0) — middleware пропускает все запросы.
	// Иначе лимитер с limit=0 блокировал бы повторные запросы из одного окна.
	if limit <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	globalRLOnce.Do(func() {
		globalRateLimiter = NewRateLimiter(window, limit)
	})
	rl := globalRateLimiter
	return func(c *gin.Context) {
		ip := c.ClientIP()
		result := rl.Allow("global:" + ip)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitGlobal, result)
			return
		}
		c.Next()
	}
}

var loginRateLimiter *RateLimiter

func InitLoginRateLimiter(window time.Duration, limit int) {
	loginRateLimiter = NewRateLimiter(window, limit)
}

func InitLoginRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	loginRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

// InitLoginRateLimiterWithValkeyFailClosed — login с fail-closed поведением (S-46-1).
func InitLoginRateLimiterWithValkeyFailClosed(client *redis.Client, window time.Duration, limit int) {
	loginRateLimiter = NewValkeyRateLimiterFailClosed(client, window, limit)
}

func StopLoginRateLimiter() {
	if loginRateLimiter != nil {
		loginRateLimiter.Stop()
	}
}

func LoginRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := loginRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		ip := c.ClientIP()
		result := rl.Allow("login:" + ip)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitLogin, result)
			return
		}
		c.Next()
	}
}

// OAuthRateLimit — отдельный бюджет для OAuth redirect/callback (S-44-2, pass 44):
// раньше OAuth делил ключ "login:"+ip с парольным логином — спам редиректами
// блокировал и парольный вход с того же IP. Свой ключ развязывает бюджеты.
// A-02 (pass 45) + S-M1 (PASS-8): создаём СВОЙ инстанс с переданными
// window/limit на каждый вызов — раньше брался глобальный oauthRateLimiter,
// инициализированный с RateLimitLoginRequests (5), и переданный limit
// (например 10) игнорировался (мёртвый параметр).
func OAuthRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := newSharedLimiter(window, limit)
	return func(c *gin.Context) {
		ip := c.ClientIP()
		result := rl.Allow("oauth:" + ip)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitLogin, result)
			return
		}
		c.Next()
	}
}

// SearchRateLimit (S-M1, PASS-8): отдельный бюджет для публичного поиска
// пользователей — раньше /api/users/search делил login:<ip> лимитер с парольным
// входом (5/5мин), и спам дешёвым поиском блокировал вход всем за NAT.
func SearchRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := newSharedLimiter(window, limit)
	return func(c *gin.Context) {
		ip := c.ClientIP()
		result := rl.Allow("search:" + ip)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitAPI, result)
			return
		}
		c.Next()
	}
}

// WebAuthnRateLimit (S-M1, PASS-8): отдельный бюджет для публичных WebAuthn
// begin/finish — раньше делил login:<ip> бюджет (спам begin блокировал вход).
func WebAuthnRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := newSharedLimiter(window, limit)
	return func(c *gin.Context) {
		ip := c.ClientIP()
		result := rl.Allow("webauthn:" + ip)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitLogin, result)
			return
		}
		c.Next()
	}
}

// PaymentRateLimit (PASS-8 #2): отдельный per-user бюджет на создание платежей.
// Раньше /payments/create делил codeSubmissionRateLimiter с вводом игровых кодов
// (общий бюджет + мёртвые параметры 5/мин — фактически применялся 10/мин).
func PaymentRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := newSharedLimiter(window, limit)
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			c.Next()
			return
		}
		key := fmt.Sprintf("payment:%d", userID)
		result := rl.Allow(key)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitCode, result)
			return
		}
		c.Next()
	}
}

// PersonalChatRateLimit (DEEP-REVIEW PASS-4 M5 / PASS-5 M3): per-USER лимит на
// создание личного чата. Раньше ключ был per-IP — за reverse-proxy без
// TRUSTED_PROXIES все пользователи делили один бюджет (429 для всех), и
// аутентифицированный атакующий обходил ротацией IP. Действие аутентифицировано —
// лимитируем по userID.
func PersonalChatRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := newSharedLimiter(window, limit)
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			userID = 0 // аноним — общий бюджет (не должно быть, маршрут под AuthRequired)
		}
		result := rl.Allow(fmt.Sprintf("personal_chat:%d", userID))
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitLogin, result)
			return
		}
		c.Next()
	}
}

// CreateRoomRateLimit (DEEP-REVIEW PASS-6 L7): per-USER лимит на создание
// комнат чата — менеджер не должен плодить неограниченное число комнат.
func CreateRoomRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := newSharedLimiter(window, limit)
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		result := rl.Allow(fmt.Sprintf("create_room:%d", userID))
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitLogin, result)
			return
		}
		c.Next()
	}
}

var oauthRateLimiter *RateLimiter

func InitOAuthRateLimiter(window time.Duration, limit int) {
	oauthRateLimiter = NewRateLimiter(window, limit)
}

func InitOAuthRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	oauthRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

// InitOAuthRateLimiterWithValkeyFailClosed — OAuth redirect/callback с fail-closed (S-46-1).
func InitOAuthRateLimiterWithValkeyFailClosed(client *redis.Client, window time.Duration, limit int) {
	oauthRateLimiter = NewValkeyRateLimiterFailClosed(client, window, limit)
}

func StopOAuthRateLimiter() {
	if oauthRateLimiter != nil {
		oauthRateLimiter.Stop()
	}
}

var registrationRateLimiter *RateLimiter

func InitRegistrationRateLimiter(window time.Duration, limit int) {
	registrationRateLimiter = NewRateLimiter(window, limit)
}

func InitRegistrationRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	registrationRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

// InitRegistrationRateLimiterWithValkeyFailClosed — регистрация с fail-closed (S-46-1).
func InitRegistrationRateLimiterWithValkeyFailClosed(client *redis.Client, window time.Duration, limit int) {
	registrationRateLimiter = NewValkeyRateLimiterFailClosed(client, window, limit)
}

func StopRegistrationRateLimiter() {
	if registrationRateLimiter != nil {
		registrationRateLimiter.Stop()
	}
}

func RegistrationRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := registrationRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		ip := c.ClientIP()
		result := rl.Allow("register:" + ip)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitRegister, result)
			return
		}
		c.Next()
	}
}

var codeSubmissionRateLimiter *RateLimiter

func InitCodeSubmissionRateLimiter(window time.Duration, limit int) {
	codeSubmissionRateLimiter = NewRateLimiter(window, limit)
}

func InitCodeSubmissionRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	codeSubmissionRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

// InitCodeSubmissionRateLimiterWithValkeyFailClosed — ввод кодов с fail-closed (S-46-1).
func InitCodeSubmissionRateLimiterWithValkeyFailClosed(client *redis.Client, window time.Duration, limit int) {
	codeSubmissionRateLimiter = NewValkeyRateLimiterFailClosed(client, window, limit)
}

func StopCodeSubmissionRateLimiter() {
	if codeSubmissionRateLimiter != nil {
		codeSubmissionRateLimiter.Stop()
	}
}

func CodeSubmissionRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := codeSubmissionRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			c.Next()
			return
		}
		key := fmt.Sprintf("code:%d", userID)
		result := rl.Allow(key)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitCode, result)
			return
		}
		c.Next()
	}
}

var sseRateLimiter *RateLimiter

func InitSSERateLimiter(window time.Duration, limit int) {
	sseRateLimiter = NewRateLimiter(window, limit)
}

func InitSSERateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	sseRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

func StopSSERateLimiter() {
	if sseRateLimiter != nil {
		sseRateLimiter.Stop()
	}
}

func SSERateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := sseRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		key := fmt.Sprintf("sse:%d", c.GetUint("userID"))
		result := rl.Allow(key)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitSSE, result)
			return
		}
		c.Next()
	}
}

// uploadRateLimiter — per-user лимитер загрузок файлов (M6, PASS-13):
// аватары, фото игр. Использует shared Valkey-клиент (меж-инстансный) через
// newSharedLimiter, при его отсутствии — in-memory. Ключ — userID.
var uploadRateLimiter *RateLimiter

func InitUploadRateLimiter(window time.Duration, limit int) {
	uploadRateLimiter = newSharedLimiter(window, limit)
}

func UploadRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := uploadRateLimiter
	if rl == nil {
		rl = newSharedLimiter(window, limit)
	}
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			c.Next()
			return
		}
		key := fmt.Sprintf("upload:%d", userID)
		result := rl.Allow(key)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitUpload, result)
			return
		}
		c.Next()
	}
}

var passwordResetRateLimiter *RateLimiter

func InitPasswordResetRateLimiter(window time.Duration, limit int) {
	passwordResetRateLimiter = NewRateLimiter(window, limit)
}

func InitPasswordResetRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	passwordResetRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

// InitPasswordResetRateLimiterWithValkeyFailClosed — сброс пароля с fail-closed (S-46-1).
func InitPasswordResetRateLimiterWithValkeyFailClosed(client *redis.Client, window time.Duration, limit int) {
	passwordResetRateLimiter = NewValkeyRateLimiterFailClosed(client, window, limit)
}

func StopPasswordResetRateLimiter() {
	if passwordResetRateLimiter != nil {
		passwordResetRateLimiter.Stop()
	}
}

func PasswordResetRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := passwordResetRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		key := c.ClientIP()
		result := rl.Allow("reset:" + key)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitPasswordReset, result)
			return
		}
		c.Next()
	}
}

var apiRateLimiter *RateLimiter

func InitAPIRateLimiter(window time.Duration, limit int) {
	apiRateLimiter = NewRateLimiter(window, limit)
}

func InitAPIRateLimiterWithValkey(client *redis.Client, window time.Duration, limit int) {
	apiRateLimiter = NewValkeyRateLimiter(client, window, limit)
}

func StopAPIRateLimiter() {
	if apiRateLimiter != nil {
		apiRateLimiter.Stop()
	}
}

func APIRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := apiRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			c.Next()
			return
		}
		key := fmt.Sprintf("api:%d", userID)
		result := rl.Allow(key)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitAPI, result)
			return
		}
		c.Next()
	}
}

// IPRateLimit — per-IP лимитер для публичных эндпоинтов без аутентификации
// (S-1, pass 31). APIRateLimit пропускает анонимов (userID==0), поэтому для
// RUM и аналогичных публичных API нужен ключ по ClientIP.
func IPRateLimit(window time.Duration, limit int) gin.HandlerFunc {
	rl := apiRateLimiter
	if rl == nil {
		rl = NewRateLimiter(window, limit)
	}
	return func(c *gin.Context) {
		key := "ip:" + c.ClientIP()
		result := rl.Allow(key)
		setRateLimitHeaders(c, result)
		if !result.Allowed {
			respondRateLimitError(c, ErrRateLimitAPI, result)
			return
		}
		c.Next()
	}
}
