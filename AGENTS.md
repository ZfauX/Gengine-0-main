# Gengine-0 Agent Guide

## Commands

```bash
make build          # CSS + go build -ldflags (...) -o gengine ./cmd/server
make dev            # go run ./cmd/server
make test           # go test -v -race -coverprofile=coverage.out ./...
make test-short     # go test -v -short -race -cover ./...  (no DB needed)
make test-integration # go test -v -race -tags=integration ./...  (requires PostgreSQL)
make test-e2e       # npx playwright test  (Playwright E2E; needs e2e DB + server on :8081)
make lint           # golangci-lint run ./...
make swagger        # swag init -g ./cmd/server/main.go -o ./docs
make css            # tailwindcss -i ./static/css/app.css -o ./static/css/output.css --minify
go generate ./...   # re-run google/wire DI codegen (needed after constructor changes)
```

E2E-тесты (Playwright, `e2e/`):
- Сервер для E2E запускается на `:8081` с БД `gengine_e2e` (см. `.env.e2e`; не коммитится).
- **Запуск сервера E2E**: `APP_ENV_FILE=.env.e2e go run ./cmd/server` (или `APP_ENV_FILE=.env.e2e ./gengine`) — переменная
  окружения указывает серверу, какой env-файл читать. Без неё main.go читает `.env` по умолчанию, и E2E-сервер
  подхватывает из `.env` ключи, которых нет в `.env.e2e` (`TLS_CERT_FILE`, `TRUSTED_PROXIES`) — это форсирует
  Secure-флаг CSRF-куки и ломает все HTML-формы по HTTP (403 «CSRF token mismatch»).
- Лимиты rate-limiter в E2E подняты (`RATE_LIMIT_*=100000`), `TRUSTED_PROXIES` пуст, `FORCE_SECURE_COOKIE=false` —
  иначе CSRF-кука станет Secure, а регистрация упрётся в дефолтный лимит 3/10мин из routes.go.
- Первый запуск: `npm install`, `npx playwright install chromium`, применить миграции к `gengine_e2e`
  (`go run ./cmd/server -migrate` с env из `.env.e2e`), запустить сервер, затем `make test-e2e`.
- Service Workers блокируются в playwright.config.ts (`serviceWorkers: 'block'`) — SW кэширует
  HTML-страницы и подставляет устаревший CSRF-токен.
- Known E2E-ловушки (исправлены PASS-7):
  - Глобальный fetch-перехватчик в `layout.html` проверяет CSRF-заголовок регистронезависимо (`toLowerCase()`);
    иначе `X-CSRF-Token` (верблюжий регистр) дублировал токен → 403.
  - Inline-скрипт `chat-global.html` инициализируется на `DOMContentLoaded` — app.js подключён с `defer`,
    `createReconnectingWebSocket` определён только после парсинга документа.
- CI: `.github/workflows/ci.yml` — jobs `unit` (lint + `-short`), `integration` (PostgreSQL service),
  `e2e` (PostgreSQL service + Playwright + сервер на :8081 с `APP_ENV_FILE=.env.e2e`).

After changing any constructor signature in `internal/domain/*/service.go`, run:
```bash
go generate ./internal/app/   # regenerates wire_gen.go
```

Pre-commit: `golangci-lint run ./...` then `go test -short ./...`.

## Architecture

```
cmd/server/main.go          ← entrypoint (godotenv → config → DB → cache → hub → deps → router)
internal/
  config/                   ← env-based config with strict validation
  db/                       ← Connect(), EnsureAdmin(), RunMigrations()
  app/                      ← DI wiring (google/wire), Router setup
  domain/{user,game,level,team,tournament,monitor,calendar,social,notification,admin,export}/
    model.go                ← GORM models + types
    service.go              ← business logic (no HTTP)
    handler.go              ← gin handlers (forms, templates)
    routes.go               ← route registration + middleware
    templates/              ← HTML (layout + per-page)
  pkg/
    cache/                  ← CacheStore (composite: Getter+Setter+Deleter+GetOrSetter+Extender)
    websocket/              ← RoomHub (rooms, broadcast, connection limits)
    middleware/              ← auth, rate-limiter, CSRF, gzip, bodylimit
    i18n/                   ← T()/TF() for 256 strings in ru+en
storage.FileStorage        ← filesystem abstraction
```

## Key Details

### DI (google/wire)
- `internal/app/init.go` has `//go:generate go run github.com/google/wire/cmd/wire`
- `wire.go` defines `initializeRepositories` and `initializeServices` (wire.Build)
- `wire_providers.go` has `wrap*` functions for services with method-chaining
- **Must run `go generate ./internal/app/`** after adding/changing constructor params, otherwise runtime panic

### Caching
- `CacheStore` is 5 composed interfaces: `Getter`, `Setter`, `Deleter`, `GetOrSetter`, `Extender`
- In-memory LRU (`Cache`) preserves Go types; Valkey uses JSON → loses types
- **Valkey cache never hits for `*Game` objects** — `cacheGetGame()` helper handles JSON→struct conversion
- DeleteByPrefix invalidates all cached entries under a key prefix

### Auth & Security
- JWT in httpOnly cookie named `jwt`; refresh token in `refresh_token` cookie
- Middleware: `AuthRequired` (redirects to `/auth/login`), `OptionalAuth` (passthrough)
- OAuth state validated via session (`subtle.ConstantTimeCompare`)
- CSRF via `gorilla/csrf` on HTML forms; skipped for `/api/`, `/static/`, `/uploads/`, `/ws/`, `/auth/webauthn`
- Rate limiters: global(100/min), login(5/min), register(3/min), code_submission(10/min)
- 2FA enforced only on `/admin/*` routes

### Real-time
- **SSE** (`SSEManager`): one-directional game notifications (start, level, hint, finish); per-game connection limits
- **WebSocket** (`RoomHub`): bidirectional chat; rooms by gameID; max total + per-IP limits
- SSE sessions have `sync.Mutex` for safe concurrent writes

### i18n
- `i18n.T("domain.key")` returns Russian string; `i18n.TF("domain.key", args...)` with formatting
- English strings also available; use middleware to switch via `i18n.Middleware(lang)`

### WebAuthn
- Passkey login via `/auth/webauthn/login/begin` + `/auth/webauthn/login/finish`
- Registration via `/auth/webauthn/register/begin` + `/auth/webauthn/register/finish` (auth required)
- Credentials stored in `webauthn_credentials` table (migration 000016)

### Tests
- `-short` skips DB-dependent tests; `-tags=integration` enables PostgreSQL tests
- PostgreSQL tests use isolated schemas via `testutil.SetupPostgresDB(t, models...)`
- Mock generation: `//go:generate go run go.uber.org/mock/mockgen -source=...`

### Repo-specific Gotchas
- `GetLogsByGameID` requires JOIN through `game_passings` — `logs` table has no `game_id` column
- `LevelService.Create` — use `ExistsByPosition()` repo method, NOT `GetByGameID()` (N+1)
- Template glob `internal/domain/*/templates/*.html` — all 60+ templates parsed at startup; dev mode re-parses on every request via Lock()
- `GamePassing.ResultDuration` stored as `bigint` nanoseconds in DB
- `EmailVerificationToken.UserID` has regular index (not unique) — old tokens deleted before creating new ones
- `DeleteByUserID` available on `EmailVerificationRepository` for cleanup

## Proactive Skills

Загружай следующие навыки через `skill("<name>")` автоматически при начале соответствующих задач:

### Всегда (любая задача с Go-кодом)
- `go-code-style` — форматирование, early-return, switch вместо if-else
- `go-error-handling` — wrapping, errors.Is/As, sentinel-ошибки
- `go-naming` — MixedCaps, no-Get, -er интерфейсы
- `go-declarations` — var vs :=, iota, struct/map литералы
- `go-functions` — порядок функций, pointer-vs-value receivers
- `go-control-flow` — guard clauses, if-with-init, range
- `go-packages` — структура пакетов, imports, blank-imports

### Архитектура и проектирование
- `go-clean-architecture` — domain/service/handler слои, dependency rule
- `go-interfaces` — CacheStore, сервисные интерфейсы, DI через wire
- `go-functional-options` — With* method-chaining паттерн (широко используется)
- `go-defensive` — копирование слайсов/мапов, defer, crypto/rand, clock injection

### База данных
- `go-database` — GORM, PostgreSQL, транзакции, миграции, N+1
- `postgres-pro` — продвинутые PostgreSQL паттерны, оптимизация запросов

### Контекст и конкурентность
- `go-context` — context.Context как первый параметр, дедлайны, отмена
- `go-concurrency` — goroutines, errgroup, sync.RWMutex/Mutex, каналы

### Фронтенд: JavaScript, HTML, CSS
- `tailwind-patterns` — Tailwind CSS классы, кастомные компоненты, dark mode, responsive
- `frontend-design` — UI/UX дизайн, визуальная композиция, типографика, цветовые схемы
- `ui-ux-pro-max` — продвинутый UX, взаимодействие, анимации, доступность (a11y)
- `web-design-guidelines` — веб-дизайн гайдлайны, семантическая вёрстка
- `javascript-pro` — Vanilla JS паттерны (ES5/6), DOM API, fetch, async, события
- `websocket-engineer` — WebSocket паттерны (reconnecting, комнаты, heartbeat)
- `fullstack-guardian` — fullstack разработка, связь фронтенда и бэка
- `webapp-testing` — тестирование веб-приложений (Playwright, E2E)

### Локализация
- `i18n-localization` — интернационализация, русский/английский, плюрализация

### Тестирование
- `go-testing` — table-driven тесты, go.uber.org/mock, go-sqlmock, fuzz
- `testing-patterns` — общие паттерны тестирования
- `test-master` — мастер тестирования, покрытие, интеграционные тесты

### Качество кода
- `go-code-review` — систематический ревью diff перед merge
- `code-reviewer` — ревью кода, поиск багов и анти-паттернов
- `go-linting` — golangci-lint, //nolint директивы
- `clean-code` — чистый код, рефакторинг, устрашение сложности

### Отладка
- `systematic-debugging` — систематическая отладка, трассировка, профилирование

### Документация
- `go-documentation` — godoc, Example-тесты
- `go-swagger` — swaggo/swag аннотации (@Summary, @Param, @Router)

### Производительность
- `go-performance` — pprof, бенчмарки, аллокации
- `go-data-structures` — слайсы, мапы, LRU-кэш
- `performance-profiling` — профилирование производительности, бутылочные горлышки

### Observability
- `go-observability` — Prometheus метрики, Sentry
- `go-logging` — zerolog, структурированное логирование

### Не загружать
- `go-graphql` — в проекте не используется
- `go-grpc` — в проекте не используется
- `customize-opencode` — только при редактировании конфигурации opencode

## Quality checklist before responding
- [ ] Code compiles (`make build` succeeds)
- [ ] All errors are handled (no `_` ignored)
- [ ] No hardcoded secrets or debug prints
- [ ] New methods have unit tests (if logic)
- [ ] Changes in constructors are followed by `go generate`

- For unit tests (no DB): `go test -short ./...`
- For integration tests: `go test -tags=integration -v ./...`
- Always mock dependencies using `go.uber.org/mock`; use `//go:generate` directive.