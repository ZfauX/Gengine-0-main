# Архитектура Gengine-0

> Обновлено в PASS-16 (UX-7). Обзор слоёв, потоков данных и ключевых подсистем.
> Дополнительно: [ERD](erd.md), [ADR](adr/), [Swagger](swagger.yaml).

---

## 1. Общая схема

```
Browser (Tailwind, Vanilla JS, WebSocket/SSE)
        │ HTTP/HTTPS
        ▼
   Gin Router (internal/app/router.go)
   │   ├── middleware: security headers, CSP, gzip, bodylimit
   │   ├── CSRF (gorilla/csrf, HTML-формы)
   │   ├── Auth: AuthRequired / OptionalAuth (JWT cookie)
   │   ├── Rate-limit: глобальный (in-memory) + per-endpoint (Valkey, fail-closed для login/register/2FA)
   │   └── RUM: POST /api/rum (Web Vitals)
   │
   ▼
Domain handlers (internal/domain/*/handler.go)
   │  └── валидация входных данных, рендер HTML, JSON API
   ▼
Domain services (internal/domain/*/service.go)  ← бизнес-логика (без HTTP)
   │  ├── транзакции, sentinel-ошибки
   │  └── интеграции: email, webpush, OAuth, payment, recaptcha
   ▼
Repositories (internal/domain/*/repository.go)  ← GORM, parameterized SQL
   ▼
PostgreSQL 18  +  Valkey (cache, rate-limit, pub/sub)  +  FS (uploads/backups)
```

## 2. Слои и правила

| Слой | Что содержит | Кто зависит |
|---|---|---|
| **Handler** | gin-обработчики, формы, шаблоны, JSON API | Service, render, middleware |
| **Service** | бизнес-логика, транзакции, кэш-инвалидация | Repository, cache, email, hub |
| **Repository** | GORM-запросы, raw SQL | только gorm.DB |
| **Model** | GORM-модели | — |

- **Направление зависимостей**: handler → service → repository. Репозиторий не
  знает о сервисах; сервис не знает об HTTP (тестируем с моками).
- **DI (google/wire)**: граф в `internal/app/wire.go`; после изменения
  конструктора — `go generate ./internal/app/`.

## 3. Аутентификация и безопасность

- **JWT** (HS256) в httpOnly cookie `jwt`; refresh — в `refresh_token`; проверка
  `iss/aud/nbf/iat`, JTI-blacklist для logout.
- **Сессии** (`internal/pkg/sessionstore`): server-side (Valkey/in-memory),
  cookie — только подписанный ID; `RenewGinSession` защищает от fixation.
- **2FA** (TOTP) — step-up только на `/admin/*`; trusted-device cookie
  (`2fa_trusted`, HMAC) отзывается при смене пароля/отключении 2FA.
- **CSRF** на всех HTML-формах (кроме `/api`, `/static`, `/uploads`, `/ws`).
- **Rate-limit**: критичные (login/register/2FA/коды/reset) — fail-closed при
  недоступности Valkey; глобальный/SSE/API — fail-open (сайт доступен).
- **CSP** с nonce (без unsafe-inline), HSTS+preload, nosniff, frame-ancestors 'none'.

## 4. Real-time подсистемы

```
RoomHub (WebSocket, internal/pkg/websocket)
  ├── rooms: map[roomID]map[*Client]
  ├── per-room очередь + воркер (рассылка не блокирует другие комнаты)
  ├── лимиты: total / per-IP / per-user (token bucket для чата)
  └── presence: onRoomChange → дебаунс 500мс → {type:"presence", count, user_ids}

SSEManager (Server-Sent Events, internal/domain/game)
  ├── подписки на gameID (уведомления: start/level/hint/finish)
  └── per-game лимит соединений

realtimebus (Valkey pub/sub, internal/pkg/realtimebus)  ← multi-instance
  ├── WSChannel  (gengine:ws)   → RoomHub cross-instance рассылка
  ├── SSEChannel (gengine:sse)  → SSEManager cross-instance рассылка
  ├── синхронная подписка (waitReady: не теряем первые сообщения)
  └── динамическое добавление каналов (WS и SSE подписываются последовательно)
```

Поток WS-сообщения в multi-instance:
```
Client → инстанс A → RoomHub.BroadcastToRoom
   ├── локально: per-room воркер → клиенты инстанса A
   └── realtimebus.Publish(WSChannel) → инстанс B (anti-echo по instanceID)
        → RoomHub.enqueueLocal → воркер комнаты → клиенты инстанса B
```

## 5. Кэширование

| Кэш | Где | TTL | Инвалидация |
|---|---|---|---|
| HTML-кэш анонимов (`/`, `/games`) | render/htmlcache | 30с | не требуется (анонимы) |
| Листинг игр (`games:list:vN:...`) | game/svc_listing | 30с | `games:list:version` bump |
| Лидерборд (`leaderboard:vN:...`) | game/svc_rating | 5м | версия в памяти (atomic) |
| Рейтинг игры (`rating:game:%d`) | game/svc_rating | 5м | DeleteByPrefix |
| Unread-счётчик | notification/service | 30с | increment/invalidate |
| Роли | pkg/rolecache | TTL | Invalidate при смене роли |
| Push-подписки | notification/service | 10с | при удалении подписки |

- **Контракт иммутабельности**: `Cache.Get` возвращает тот же указатель —
  не мутируйте кэшированный объект, копируйте на границе.
- Для листинга в кэше хранится **DTO `GameCard`** (только поля карточек).

## 6. Email-очередь

```
Enqueue → БД (status=pending) → batch-воркер (каждые 10с)
   ├── claim (status='sending', атомарно)
   ├── SMTP-отправка (таймаут 30с)
   └── status='sent' | retry (с экспоненциальной задержкой)
Shutdown: ждёт до 2×smtpTimeout (60с) — воркер успевает до закрытия БД.
```

## 7. Миграции

- `migrations/` — поштучные SQL (до 000069), применяются на свежих БД.
- `migrations_squashed/` — наборы для быстрой накатки (несамодостаточны —
  свежие БД всегда идут по `migrations/`).
- Поддержка `down`-миграций; при деплое на больших таблицах индексы создаются
  отдельно (CONCURRENTLY нельзя в golang-migrate batch).

## 8. Развёртывание (целевое: pod через podman)

```
deploy/pod/gengine-pod.yaml   ← pod: app + postgres + valkey (внутренняя сеть)
deploy/systemd/               ← systemd-юниты (gengine, gengine-db)
Dockerfile                    ← golang:1.25.13-alpine → alpine:3.23 (GOMEMLIMIT=384MiB)
```

- HTTPS: самоподписанный сертификат в `deploy/certs/` (НЕ в git — .gitignore).
- pprof: `127.0.0.1:6060` (loopback, не проброшен наружу).
- CI: lint (vet + golangci-lint + govulncheck + gitleaks) → unit → integration
  (PostgreSQL) → build → e2e (Playwright) → docker.

## 9. Ключевые решения (кратко)

- **Синхронная pub/sub-подписка** (PASS-16): первый `Subscribe` ждёт
  подтверждения Redis — нет потери первых сообщений и флейков тестов.
- **Версионные ключи кэша** вместо DeleteByPrefix/SCAN: листинг игр,
  лидерборд — O(1) инвалидация.
- **HTML-кэш до загрузки данных**: анонимный кэш-хит `/games` не бьёт в
  БД/Valkey.
- **Дебаунс presence** (500мс/комнату): нет лавины WS-сообщений при штурме
  подключений.
