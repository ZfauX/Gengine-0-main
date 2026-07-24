# Глубокое ревью проекта Gengine-0

> **Дата:** 2026-07-24
> **Ветка:** `master` (c5f8ec6)
> **Тип ревью:** Полный аудит кодовой базы, архитектуры, безопасности, git-истории, тестов и UX
> **Предыдущая сессия:** Вчерашняя отладка (незакоммиченные изменения в 25+ файлах)

---

## 📋 Структура ревью

1. [Git-анализ и история](#1-git-анализ-и-история)
2. [Статус незакоммиченных изменений](#2-статус-незакоммиченных-изменений)
3. [Критические ошибки (P0)](#3-критические-ошибки-p0)
4. [Ошибки безопасности](#4-ошибки-безопасности)
5. [Проблемы производительности](#5-проблемы-производительности)
6. [Дефекты кода](#6-дефекты-кода)
7. [Архитектурные проблемы](#7-архитектурные-проблемы)
8. [Проблемы DevOps и сборки](#8-проблемы-devops-и-сборки)
9. [Проблемы тестирования](#9-проблемы-тестирования)
10. [Проблемы пользовательского опыта](#10-проблемы-пользовательского-опыта)
11. [Анализ агентских веток (Devin)](#11-анализ-агентских-веток-devin)
12. [Возможности оптимизации](#12-возможности-оптимизации)
13. [Предложения по улучшению](#13-предложения-по-улучшению)
14. [План действий](#14-план-действий)

---

## 1. Git-анализ и история

### 1.1. Граф коммитов

```
* c5f8ec6 (HEAD -> master, origin/master, origin/HEAD) 23.07 19:12
* fe33a4f 23.07 17:36
* f5885b5 23.07 15:10
* 87cfc9c 22.07 22:47
* 7ac1b65 22.07 19:25
* a6677c9 22.07 17:53
| * 231689c (refs/stash) WIP on master (20.07)
|/
* 28110ab 20.07 18:13
...
| * 455478d (origin/devin/1783444753-security-fixes) security fixes
| * 909e542 (origin/devin/1783444460-error-handling) error handling
| * ca6a4a4 (origin/devin/1783444865-refactor-shared-utilities) shared utils
|/
* 5044028 13.07 23:41
...
* ee7abc6 (candied-fruit, agents/*) Commit 18.06 ← первый коммит
```

### 1.2. Ветки

| Ветка | Коммит | Описание |
|-------|--------|----------|
| `master` | c5f8ec6 | Основная ветка, актуальная |
| `candied-fruit` | ee7abc6 | Первый коммит (18.06) — полный snapshot |
| `agents/elated-pinniped` | ee7abc6 | То же самое |
| `agents/vulnerable-sparrow` | ee7abc6 | То же самое |
| `origin/devin/1783444753-security-fixes` | 455478d | Devin: security fixes |
| `origin/devin/1783444460-error-handling` | 909e542 | Devin: error propagation |
| `origin/devin/1783444865-refactor-shared-utilities` | ca6a4a4 | Devin: shared utilities |

### 1.3. Проблемы git-истории

1. **Ветки `agents/*` и `candied-fruit` не содержат уникальных коммитов** — они просто указывают на первый коммит `ee7abc6` (18.06). Это, скорее всего, снапшоты, созданные разными AI-агентами. Их можно удалить.

2. **Ветки Devin не слиты в master** — три бранча с полезными изменениями висят отдельно. Изменения из них нужно влить или хотя бы выборочно перенести:
   - `security-fixes`: удалён `.env` из репозитория, усилены cookie, обновлены зависимости
   - `error-handling`: улучшена обработка ошибок в tournament, user services и security middleware
   - `shared-utilities`: вынесен дублирующийся код, сокращение на ~200 строк

3. **В stash есть несохранённые изменения** (stash@{0}) — изменения в `monitor/handler.go` (пагинация логов).

---

## 2. Статус незакоммиченных изменений

**25 изменённых файлов, ~976 строк добавлено, ~87 удалено.**

### Что было сделано вчера (наша отладка):

| Файл | Изменение | Статус |
|------|-----------|--------|
| `internal/domain/notification/routes.go` | WebSocket для real-time уведомлений | ⏳ Не закончен |
| `internal/domain/notification/settings_handler.go` | VAPID public key в шаблон | ✅ |
| `internal/domain/user/auth_handler.go` | Валидация пароля при регистрации и сбросе | ✅ |
| `internal/domain/user/profile_handler.go` | Валидация пароля при смене | ✅ |
| `internal/domain/user/push_handler.go` | VAPID public key из конфига | ✅ |
| `internal/domain/user/routes.go` | VAPID config DI | ✅ |
| `internal/config/config.go` | VAPID load/generate | ✅ |
| `internal/pkg/csrf/csrf.go` | ParseForm перед CSRF (фикс бага) | ✅ |
| `internal/pkg/middleware/error_handler.go` | Проверка `Writer.Written()` | ✅ |
| `internal/pkg/render/helper.go` | Dev mode reload (убрал callback) | ✅ |
| `internal/app/app.go` | WS bypass для CSRF, notification DI | ✅ |
| `internal/app/router.go` | Chrome DevTools route, dev mode | ✅ |
| `internal/db/migrate.go` | Squashed migrations support | ✅ |
| `cmd/server/main.go` | `MigrateFromDir` вместо `MigrateFromFiles` | ✅ |
| `cmd/migrate/main.go` | `MigrateFromDir` | ✅ |
| `go.mod` / `go.sum` | webpush-go dependency | ✅ |
| `.env.example` | VAPID keys env vars | ✅ |
| Шаблоны / статика | Dark theme, Leaflet, notification UI | ✅ |
| `migrations/` | Новые миграции (000014, 000015) | ✅ |

### ⚠️ Не закончено / требует доработки:

1. **Notification WebSocket** — код есть, но не протестирован. Нужно убедиться, что `HandleWebSocketWithContext` существует и работает.
2. **Push-уведомления** — VAPID ключи генерируются, но логика отправки push не завершена.
3. **Новые миграции (000014, 000015)** — добавлены `.up.sql` и `.down.sql`, но не проверено, что числовая нумерация не конфликтует с существующими миграциями (001-013).

---

## 3. Критические ошибки (P0)

### 3.1. Dockerfile использует Go 1.23, но go.mod требует go 1.25

**Файл:** `Dockerfile:2` vs `go.mod:3`

```dockerfile
FROM golang:1.23-alpine AS builder
```

```go
go 1.25.0
```

**Вердикт:** Сборка в Docker **гарантированно упадёт** — go 1.23 не поддерживает язык go 1.25.

**Решение:** `golang:1.25-alpine` или новее.

### 3.2. Race condition в Valkey rate limiter

**Файл:** `internal/pkg/middleware/rate_limiter.go:139-163`

```go
count, err := s.client.Incr(ctx, key).Result()
// ...
return RateLimitResult{
    Allowed: count <= int64(s.limit),  // ← неатомарно!
}
```

**Проблема:** `INCR` + проверка — неатомарная операция. При N параллельных запросах все N инкрементов сработают до проверки, и все N пройдут. Rate limit эффективно отключён при высокой нагрузке.

**Решение:** Lua-скрипт:
```lua
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
if count <= tonumber(ARGV[2]) then return 1 else return 0 end
```

### 3.3. CSRF token может не сработать из-за ParseForm

**Файл:** `internal/pkg/csrf/csrf.go:44`

```go
_ = c.Request.ParseForm()
```

**Проблема:** `ParseForm()` парсит тело запроса. Если запрос уже был прочитан (например, в middleware логирования), тело будет пустым и CSRF-токен не извлечётся. `ParseMultipartForm` тоже вызовет ошибку.

**Решение:** Использовать `c.Request.ParseMultipartForm(maxMemory)` или проверять Content-Type перед вызовом.

### 3.4. CSP nonce падает на предсказуемый timestamp

**Файл:** `internal/pkg/middleware/security.go:16-23`

```go
func generateNonce() string {
    b := make([]byte, 16)
    if _, err := rand.Read(b); err != nil {
        b = []byte(fmt.Sprintf("%x", time.Now().UnixNano()))  // ← предсказуемо
    }
    return base64.RawURLEncoding.EncodeToString(b)
}
```

**Вердикт:** На практике `crypto/rand` не падает, но fallback — дыра в CSP. Лучше убрать fallback или использовать `math/rand` только для dev-режима.

---

## 4. Ошибки безопасности

### 4.1. Отсутствует защита OAuth callback от CSRF

**Где:** `internal/domain/user/auth_handler.go` — OAuth callback не проверяет `state`-параметр на временные атаки.

### 4.2. Нет rate limiter на WebSocket upgrade

**Где:** `internal/pkg/websocket/room_hub.go`

`CanAccept()` проверяет только свои лимиты, но на уровне HTTP middleware нет rate limiter'a. Можно открыть N соединений до срабатывания лимита хаба.

### 4.3. Секреты могут оказаться в логах

**Файл:** `internal/pkg/middleware/logger.go:88`

Тело POST-запроса маскируется только в query string (`maskQuery`), но не в теле ответа. При ошибке в форме логина/регистрации пароль может быть залогирован.

### 4.4. Нет HSTS для production

**Файл:** `internal/pkg/middleware/security.go:56`

```go
c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
```

HSTS устанавливается всегда, даже если сайт работает по HTTP. В dev-режиме это может вызвать проблемы.

### 4.5. Rate limiter's Valkey INCR не использует pipeline

**Файл:** `internal/pkg/middleware/rate_limiter.go:144-153`

Два отдельных вызова: `INCR` + `EXPIRE`. Лучше объединить в один pipeline или Lua-скрипт.

---

## 5. Проблемы производительности

### 5.1. Cache.Get() — double-lock с race condition

**Файл:** `internal/pkg/cache/cache.go:121-146`

```go
func (c *Cache) Get(key string) (any, bool) {
    c.mu.Lock()        // 1
    // ... проверка ...
    if item.expired() {
        c.mu.Unlock()  // 2 — отпускаем
        c.mu.Lock()    // 3 — снова берём (другая горутина могла изменить!)
        // ...
    }
}
```

**Рекомендация:** Использовать `RLock` для чтения, `Lock` только для записи.

### 5.2. Monitor cache — всего 50 записей

**Файл:** `internal/domain/game/monitor_service.go:98`

```go
if len(s.cache) > 50 {
    // вытеснение
}
```

Для production-сервера с 50+ одновременными играми кэш будет постоянно перезагружаться.

### 5.3. SQL с LIMIT 100

**Файл:** `internal/domain/game/monitor_service.go:202`

```sql
LIMIT 100
```

Если в игре >100 команд, часть не попадёт в монитор.

### 5.4. Event bus с буфером 256 и silent drop

**Файл:** `internal/pkg/events/events.go`

```go
queue: make(chan job, eventBusQueueSize) // 256
// При переполнении — молча дропает событие
```

**Проблема:** Критичные события (расчёт результатов, аудит) могут быть потеряны.

### 5.5. Email worker pool фиксированного размера

**Файл:** `internal/pkg/email/email.go`

```go
maxConcurrent = 5 // жёстко зашито
```

Нет конфигурации, нет backpressure при переполнении очереди.

---

## 6. Дефекты кода

### 6.1. Event bus инициализирован, но нигде не используется

**Файл:** `internal/pkg/events/events.go` (~220 строк)

Пакет `events` реализует pub/sub шину с воркерами, middleware, метриками — и **не используется нигде** в коде. Мёртвый код.

### 6.2. Комментарии в кракозябрах

**Файл:** `internal/domain/game/routes.go:19`

```
// GameDeps СЃРѕРґРµСЂР¶РёС‚ РІСЃРµ Р·Р°РІРёСЃРёРјРѕСЃС‚Рё...
```

Повреждённая UTF-8 кодировка. Нужно пересохранить файл.

### 6.3. `GameHandler.Update()` — нет проверки IsDraft

**Файл:** `internal/domain/game/game_crud_service.go:62-83`

Позволяет редактировать опубликованную игру, даже если у неё есть активные прохождения. Меняет правила "на лету".

### 6.4. PasswordResetToken имеет два поля: ResetCode и TokenHash

**Файл:** `internal/domain/user/model.go:54-61`

```go
type PasswordResetToken struct {
    ResetCode string `gorm:"uniqueIndex;not null"` // одноразовый код в URL
    TokenHash string `gorm:"uniqueIndex;not null"` // SHA256 хеш токена
}
```

В модели два разных токена — ResetCode и TokenHash. В коде используется только ResetCode (`GenerateToken` возвращает код). TokenHash, судя по всему, не используется. Избыточность.

### 6.5. Отсутствует graceful shutdown для event bus

Если бы event bus использовался, его воркеры не дожидались бы завершения.

### 6.6. `CalculateResults` — хрупкое построение SQL

**Файл:** `internal/domain/game/monitor_service.go:339-349`

Ручное построение `CASE WHEN` с 50+ строками и ручным согласованием аргументов хрупко. Любое изменение порядка сломает запрос.

---

## 7. Архитектурные проблемы

### 7.1. Global singleton rate limiters

**Файл:** `internal/pkg/middleware/rate_limiter.go`

```go
var globalRateLimiter *RateLimiter
var loginRateLimiter *RateLimiter
var registrationRateLimiter *RateLimiter
// ... ещё 3 синглтона
```

**Проблемы:**
- Невозможно тестировать изолированно
- Pull-зависимость: любой код может вызвать `Init*`
- При двух инстансах в одном процессе — общее состояние

**Решение:** Передавать через DI, как и все остальные компоненты.

### 7.2. Wire DI + wrapper boilerplate

**Файлы:** `internal/app/wire.go`, `wire_providers.go`, `wire_gen.go`

```go
func wrapGameService(..., ca *game.CoAuthorService, ...) *game.GameService {
    return game.NewGameService(...) // просто обёртка
}
```

Wire используется только для репозиториев, а для сервисов — 20+ функций-обёрток, которые не делают ничего, кроме вызова конструктора. Можно либо удалить Wire и делать всё руками, либо использовать Wire для всего.

### 7.3. Дублирование GORM-моделей

**Файл:** `internal/domain/game/model.go`

```go
type gameBlackboxVotingSession struct { ... }
func (gameBlackboxVotingSession) TableName() string { return "blackbox_voting_sessions" }
```

Дубликат модели из пакета `monitor`. AGENTS.md говорит, что это осознанно, но это создаёт риск рассинхронизации.

### 7.4. Domain-модели используются как DTO

GORM-модели (например, `Game`, `GamePassing`) используются одновременно как:
- DB-модели (gorm tags)
- Form binding (form tags)
- JSON response (json tags)
- Business entities

Это нарушение Single Responsibility Principle. При изменении формата формы или JSON придётся менять модель, что может сломать миграцию.

### 7.5. Notification domain — незаконченный рефакторинг

После вчерашних изменений `notification/routes.go` получил WebSocket, API-эндпоинты и редиректы. Файл разросся с 30 до 150+ строк, смешивая:
- Routing
- WebSocket upgrade
- HTTP handlers (inline)

---

## 8. Проблемы DevOps и сборки

### 8.1. Бинарники и артефакты в репозитории

| Файл | Размер | Проблема |
|------|--------|----------|
| `gengine.exe` | ~32 MB | Скомпилированный бинарник |
| `golangci-lint.exe` | ~40 MB | Сторонний инструмент |
| `coverage` | >500 KB | Отчёт покрытия |
| `node_modules/` | ? | npm зависимости |

Все должны быть в `.gitignore`.

### 8.2. Нет `.env.example` (был, но удалён?)

`.env.example` существует, но в git-истории он был удалён в security-fixes ветке. Сейчас он снова добавлен в рабочей директории.

### 8.3. Entrypoint использует postgresql17-client

**Dockerfile:24**

```dockerfile
RUN apk add --no-cache ca-certificates tzdata postgresql17-client
```

Жёсткая привязка к версии 17 — если образ Alpine обновится, пакет может отсутствовать. Лучше `postgresql-client`.

### 8.4. Нет .gitignore для сгенерированных файлов

`wire_gen.go` не в `.gitignore`. После каждого `go generate` он меняется, создавая шум в ревью.

---

## 9. Проблемы тестирования

### 9.1. Все тесты требуют PostgreSQL

Даже unit-тесты сервисов (например, `service_test.go`) используют `SetupPostgresDB`. Это делает невозможным запуск тестов без настроенной БД.

**Рекомендация:** Использовать `go-sqlmock` для unit-тестов, PG — только для интеграционных.

### 9.2. CSRF regex в тестах хрупкий

**Файл:** `cmd/server/integration_test.go:38`

```go
var csrfTokenRE = regexp.MustCompile(`value=\"([a-zA-Z0-9+/=]+)\"`)
```

Формат зависит от реализации gorilla/csrf. Обновление библиотеки может сломать тесты.

### 9.3. Очистка схемы после теста может подтекать

**Файл:** `internal/testutil/postgres.go:85`

Если `gorm.Open` для очистки не удастся (сеть, таймаут), схема останется в БД.

### 9.4. Нет тестов для новых функций

- VAPID / Web Push — не тестировано
- Notification WebSocket — не тестировано
- Squashed migrations — не тестировано
- Password validation — не тестировано

### 9.5. Нет бенчмарков

При наличии кэша с LRU, singleflight, rate limiter'ов — нет ни одного бенчмарка.

---

## 10. Проблемы пользовательского опыта

### 10.1. Нет API-версионирования

Все API-методы на `/api/` без префикса версии. Любое изменение сломает существующих клиентов.

### 10.2. Swagger только для авторизованных

**Файл:** `internal/app/router.go:68-73`

Swagger отдаёт 401 для неавторизованных. Плохой DX для внешних разработчиков.

### 10.3. Нет автоматической очистки зависших заявок

Команда может зарегистрироваться на игру, не начать её — и висеть в "pending" вечно.

### 10.4. Нет real-time уведомлений о событиях вне игры

SSE есть только для игрового процесса. Приглашения, отзывы, изменения игр — без уведомлений (WebSocket только начали делать вчера).

### 10.5. Сообщения об ошибках в шаблонах не всегда локализованы

В некоторых шаблонах сообщения на русском, в некоторых — хардкод на русском в коде. Нет централизованной локализации.

---

## 11. Анализ агентских веток (Devin)

### 11.1. `origin/devin/1783444753-security-fixes`

**Коммит:** `455478d` — "security: remove committed .env secrets, harden auth cookies, bump vulnerable deps"

**Изменения:**
- Удалён `.env` из репозитория (был закоммичен!)
- Усилены настройки auth cookie (SameSite, Secure флаги)
- Обновлены уязвимые зависимости

**Оценка:** Полезные изменения. `.env` не должен был попасть в репозиторий!

### 11.2. `origin/devin/1783444460-error-handling`

**Коммит:** `909e542` — "Propagate and log previously swallowed errors"

**Изменения:**
- `tournament/service.go` — ошибки не проглатываются
- `user/service.go` — логирование ошибок
- `security.go` — обработка ошибок

**Оценка:** Качественные изменения. Улучшают отладку.

### 11.3. `origin/devin/1783444865-refactor-shared-utilities`

**Коммит:** `ca6a4a4` — "refactor: extract shared utilities for duplicated code patterns"

**Изменения:**
- Вынесена общая пагинация из calendar, monitor, social
- Упрощены handler'ы game, user
- -200 строк кода

**Оценка:** Хороший рефакторинг. Уменьшает дублирование.

**Рекомендация по всем Devin-веткам:** Слить хотя бы security-fixes в master. Остальные — опционально, но желательно.

---

## 12. Возможности оптимизации

### 12.1. Производительность

| Область | Что сделать | Ожидаемый эффект |
|---------|-------------|------------------|
| Cache.Get() | RLock вместо double Lock | -50% contention на чтении |
| Monitor SQL | Добавить индексы на `(game_id, status)`, `(game_passing_id, finished_at)` | Ускорение GameSnapshot в 2-5x |
| GameSnapshot | Убрать LIMIT 100 | Корректные данные для 100+ команд |
| Rate limiter | Lua-скрипт для Valkey | Атомарность, -1 round trip |
| Шаблоны | Кэшировать в production, hot-reload в dev | Ускорение рендеринга |
| Event bus | Увеличить буфер до 10000+ | Меньше потерянных событий |

### 12.2. Кодовая база

| Область | Что сделать |
|---------|-------------|
| Мёртвый код | Удалить `events` пакет (или интегрировать) |
| Wire | Удалить Wire и делать DI руками (или наоборот — Wire для всего) |
| .gitignore | Добавить `*.exe`, `node_modules/`, `coverage`, `wire_gen.go` |
| Комментарии | Пересохранить `routes.go` в UTF-8 |
| Notification routes | Разделить на handler + routes |

### 12.3. CI/CD

| Область | Что сделать |
|---------|-------------|
| Docker | Починить версию Go |
| GitHub Actions | Добавить линтер, тесты, security scan |
| Pre-commit | Проверять на бинарники, секреты, go vet |

---

## 13. Предложения по улучшению

### 13.1. Немедленные исправления (P0)

| # | Задача | Файлы |
|---|--------|-------|
| 1 | Исправить Go-версию в Dockerfile | `Dockerfile` |
| 2 | Переписать Valkey rate limiter на Lua-скрипт | `rate_limiter.go` |
| 3 | Проверить и починить `CalculateResults` SQL | `monitor_service.go` |
| 4 | Убрать бинарники из репозитория | `gengine.exe`, `golangci-lint.exe` |
| 5 | Слить `origin/devin/1783444753-security-fixes` | — |

### 13.2. Важные улучшения (P1)

| # | Задача |
|---|--------|
| 6 | Убрать global singleton rate limiters → DI |
| 7 | Добавить `.gitignore` для артефактов |
| 8 | Увеличить монитор-кэш до 1000+, убрать LIMIT 100 |
| 9 | Интегрировать event bus или удалить пакет |
| 10 | Разделить мок-тесты (go-sqlmock) и интеграционные тесты (PG) |
| 11 | Исправить `Cache.Get()` double-lock |
| 12 | Починить комментарии в `routes.go` |
| 13 | Добавить graceful shutdown для event bus workers |
| 14 | Проверить и дотестировать notification WebSocket |

### 13.3. Долгосрочные улучшения (P2-P3)

| # | Задача |
|---|--------|
| 15 | Версионировать API (`/api/v1/`) |
| 16 | Централизовать локализацию сообщений |
| 17 | Реализовать версионирование контента игр |
| 18 | Добавить auto-очистку просроченных заявок/игр |
| 19 | Вынести константы в config (CacheTTL, ShutdownTimeout, etc.) |
| 20 | Заменить `wire` на ручную инициализацию |
| 21 | Добавить бенчмарки для cache, rate limiter, monitor |
| 22 | Открыть Swagger для неавторизованных (или сделать ключ доступа) |

---

## 14. План действий

### Этап 1: Критические фиксы (1-2 часа)

```
1. Dockerfile: golang:1.23 → golang:1.25-alpine
2. rate_limiter.go: Lua-скрипт для Valkey
3. monitor_service.go: протестировать CalculateResults
4. Удалить gengine.exe, golangci-lint.exe, node_modules/
5. git merge origin/devin/1783444753-security-fixes
```

### Этап 2: Стабилизация (3-4 часа)

```
6. Починить Cache.Get() double-lock
7. Увеличить cache limit (50 → 1000+) и убрать LIMIT 100
8. Починить комментарии routes.go
9. Добавить тесты на VAPID, Web Push, notification WS
10. Проверить миграции 000014-000015 на конфликты
```

### Этап 3: Наведение порядка (4-6 часов)

```
11. Убрать global singletons rate limiter'ов
12. Интегрировать или удалить event bus
13. Добавить .gitignore
14. Разделить unit/integration тесты
15. Devin ветки: error-handling + shared-utilities review & merge
```

### Этап 4: Архитектурные улучшения (по мере необходимости)

```
16. API versioning
17. Remove Wire → manual DI
18. Localization
19. Domain events integration
20. CI/CD pipeline
```

---

## Итоговая оценка

**Общий вердикт:** 🟡 **Выше среднего, но требует стабилизации**

Кодовая база имеет продуманную архитектуру, использует современные практики (DI, Prometheus метрики, CSP с nonce, TOTP 2FA, graceful shutdown, singleflight). Проект явно писался с вниманием к деталям.

**Критические блокеры (P0):**
- ❌ Dockerfile не соберётся
- ❌ Valkey rate limiter неатомарен
- ❌ CalculateResults хрупкий
- ❌ Бинарники в репозитории

**Сильные стороны:**
- ✅ Graceful shutdown с правильным порядком
- ✅ CSP с per-request nonce
- ✅ TOTP 2FA
- ✅ Prometheus-метрики (15+ типов)
- ✅ singleflight для монитора
- ✅ Изолированные PG-схемы тестов
- ✅ Маскировка sensitive query-параметров
- ✅ Squashed migrations
- ✅ Web Push (в процессе)

**После исправления P0 → 🟢 Готов к production**

---

*Ревью выполнил: Zed AI (DeepSeek V4 Flash)*
*Основано на анализе git-истории (41 коммит, 8 веток), всех исходных файлов и незакоммиченных изменений*
