# AGENTS.md — Gengine-0

## Project Overview

Gengine-0 is a Go-based platform for creating and running urban/field/online quest games (like "Encounter"). It is a server-side rendered web application (no SPA frontend) using Gin, GORM, PostgreSQL, and WebSockets.

## Essential Commands

```bash
# Build
CGO_ENABLED=0 go build -o gengine -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty) -X main.buildDate=$(date -u '+%Y-%m-%d_%H:%M:%S')" ./cmd/server

# Run (requires .env or env vars)
go run ./cmd/server

# Run all domain + integration tests
go test ./internal/domain/... ./cmd/server/...

# Test with coverage
go test ./internal/domain/... -coverprofile=coverage.out && go tool cover -html=coverage.out

# Run only PostgreSQL-backed tests (filter by name)
go test ./internal/domain/level -run "Postgres"

# Docker
docker-compose up
```

## Architecture & Data Flow

```
cmd/server/main.go          — Entry point: loads env → config → DB → migrations → router → background jobs → HTTP server
  internal/config/config.go — Strict env-var loading with min-length checks, no defaults for secrets
  internal/app/router.go    — Wires all domain services, middleware, and routes into a single gin.Engine
  internal/domain/*/         — Business logic organized by feature domain
    model.go   — GORM models + filter/sort/status types
    service.go — Business logic (pure Go, no HTML/HTTP concerns)
    handler.go — HTTP handlers (gin.Context, form binding, template rendering)
    routes.go  — Route registration (creates services, applies middleware)
    templates/ — HTML templates for that domain
  internal/pkg/              — Shared infrastructure
    middleware/ — auth (JWT cookie), CSRF, security headers, gzip, game_manager, team_access, permissions
    websocket/  — Room-based WS hub for real-time monitor/chat
    storage/    — Local filesystem storage interface + implementation
    email/      — SMTP email sending
  internal/testutil/         — Test helpers: SQLite in-memory DB + PostgreSQL isolated-schema DB
  cmd/server/integration_test.go — External integration tests (package main_test)
```

**Key data flow:**
1. `main.go` loads config, opens PostgreSQL, runs GORM AutoMigrate for all models
2. Creates a single `ws.RoomHub`, starts it (`go hub.Run()` — note: `Run()` is intentionally a no-op)
3. Calls `app.SetupRouter(db, storage, hub, cfg, ".")` which creates all domain services and mounts routes
4. Services are manually composed via dependency injection (no DI framework)
5. Auth uses JWT stored in cookies, extracted by `middleware.AuthRequired` / `middleware.OptionalAuth`
6. `userID` is stored in `gin.Context` via `c.Set("userID", userID)` and read via `c.GetUint("userID")`

## Domain Structure Pattern

Every domain follows the same pattern. When adding a new domain, replicate this:

- **`model.go`**: GORM models with struct tags (`form:"..." binding:"..."` for validation, `gorm:"..."` for DB). Always embed `gorm.Model`.
- **`service.go`**: Stateless service struct holding `*gorm.DB` and dependencies. Constructed with `NewXxxService(db, ...)`.
- **`handler.go`**: Handler struct holding service references. Methods receive `*gin.Context`. Input is bound with `c.ShouldBind(&input)`. Templates are rendered with `c.HTML(http.StatusOK, "layout.html", gin.H{"ContentBlock": "domain_template.html", ...data})`.
- **`routes.go`**: Single exported `RegisterRoutes(router *gin.Engine, db *gorm.DB, ...)` function. Creates local services and handlers, then mounts routes with appropriate middleware groups.

**Template rendering**: All templates use a two-level layout — `c.HTML(200, "layout.html", gin.H{"ContentBlock": "...", ...})`. The layout embeds the content block. Templates are loaded via `r.LoadHTMLGlob(filepath.Join(baseDir, "internal", "domain", "*", "templates", "*.html"))`.

## Naming Conventions

- All comments and user-facing strings are in Russian
- Models: PascalCase (`Game`, `GamePassing`, `LevelProgress`)
- Services: `NewXxxService`, struct fields exported (`DB *gorm.DB`)
- Handlers: `XxxHandler` with methods like `Create`, `Show`, `List`, `EditForm`
- Route params: `:id` for game/level/team IDs, `:passing_id` for gameplay, `:user_id` in nested routes
- Form binding structs defined per-handler input (like `ApplyInput`, `SubmitCodeInput`)

## Testing

All tests use PostgreSQL isolated schemas (`testutil.SetupPostgresDB`) — creates a unique schema per test, auto-cleans via `t.Cleanup`. Requires a local `gengine_test` database with user `test`/password `test`.

**Integration tests** (`cmd/server/integration_test.go`):
- Use package `main_test` (external test package)
- Create a test router via `setupTestRouter` using `app.SetupRouter`
- Require `gin.SetMode(gin.TestMode)` before router creation
- CSRF tokens must be scraped from HTML forms using a regex (`csrfTokenRE`)
- The `baseDir` passed to `SetupRouter` is `"../.."` (relative from `cmd/server/`)

## Auth & Middleware Stack

The middleware order in `router.go`:
1. `gin.Recovery()` → sessions → CSRF
2. Custom FuncMap → LoadHTMLGlob
3. Security headers → Gzip → Static cache
4. Static file routes
5. Domain routes (each applies auth middleware as needed)

Auth middleware flavors:
- `AuthRequired(authService)` — reads JWT cookie, sets `userID` in context, redirects to `/auth/login` on failure (or JSON 401 for `/api/` routes)
- `OptionalAuth(authService)` — same extraction but no redirect on failure
- `GameManager(coAuthorSvc)` — checks `:id` param, validates user is author or co-author
- `RequirePermission(authorizer, role)` — checks `:game_id` param, calls `IsUserManager`

## Gotchas & Non-Obvious Patterns

1. **`interfaces.go`** in middleware is a comment-only reference file. The `TokenParser` interface is actually declared in `auth.go`. `GameAuthorizer` and `TeamAccessChecker` are declared here.

2. **`ws.RoomHub.Run()` is a no-op** — the method body is empty (`func (h *RoomHub) Run() {}`), but it's still called as `go hub.Run()` in both main and tests. Do not remove this call.

3. **Duplicate model wrappers in `game/model.go`**: `gameLevel`, `gameQuestion`, `gameAnswer`, `gameBlackboxVotingSession` shadow the real models from `level/` and `monitor/` packages by overriding `TableName()` to point at the canonical tables. This is intentional — the game domain uses its own lightweight versions of these types.

4. **Config validation is aggressive**: `requireStrongSecret` checks not only min length but also rejects common weak prefixes like `"change-me"`, `"secret"`, `"password"`. The app **will crash on startup** if secrets are weak.

5. **Form-based CSRF**: Every HTML form needs a hidden `_csrf` field. The CSRF token is provided by the `gin-csrf` middleware. Integration tests must scrape the token from form HTML using a regex before POSTing.

6. **Hardcoded PostgreSQL test credentials**: `testutil/postgres.go` hardcodes `host=localhost port=5432 user=test password=test dbname=gengine_test`. Tests requiring PostgreSQL will fail silently if this DB doesn't exist.

7. **Go module name is `gengine-0`** (with a hyphen, not underscore). All imports use `gengine-0/internal/...`.

8. **The `static/` directory** contains a PWA manifest and service worker (`sw.js`, `manifest.json`) — the app supports install-to-home-screen.

9. **Build flags**: The Dockerfile and README both use `CGO_ENABLED=0` for production builds. The binary embeds version/date via `-ldflags`.

10. **Two separate route registration functions** for game: `RegisterRoutes` (public + auth) and `RegisterGameplayRoutes` (actual gameplay under `/game/:passing_id`). The gameplay routes use a separate `GameplayHandler` and are registered in `router.go` after the main game routes.
