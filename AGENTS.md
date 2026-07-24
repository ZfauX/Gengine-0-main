# Gengine-0 Agent Guide

## Commands

```bash
make build          # go build -ldflags (...) -o gengine ./cmd/server
make dev            # go run ./cmd/server
make test           # go test -v -race -coverprofile=coverage.out ./...
make test-short     # go test -v -short -race -cover ./...  (no DB needed)
make lint           # golangci-lint run ./...
make swagger        # swag init -g ./cmd/server/main.go -o ./docs
go generate ./...   # re-run google/wire DI codegen (needed after constructor changes)
```

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
- CSRF via `gorilla/csrf` on HTML forms; skipped for `/api/`, `/static/`, `/uploads/`, `/ws/`
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
