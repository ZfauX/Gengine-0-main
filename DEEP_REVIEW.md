# Deep Review (pass 15) — июль 2026

## Покрытие

| Инструмент | Результат |
|-----------|-----------|
| `golangci-lint run ./...` | ✅ Чисто |
| `go vet ./...` | ✅ Чисто |
| `go build ./...` | ✅ OK |
| `go test -short ./...` | ✅ 35/35 |

> Новые направления: deployment readiness, бизнес-логика edge cases,
> i18n покрытие, интеграционные точки и data flow.

---

## 🔴 CRITICAL

### C1. Tournament Upsert — обход транзакции

**Файл:** `internal/domain/tournament/service.go:252-297`

**Проблема:** `Upsert` использует `ctx`, а не `tx`. Вся транзакция — no-op:
```go
err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
    for _, p := range passings {
        if upsErr := s.tournamentResultRepo.Upsert(ctx, result); upsErr != nil {
            //          ^^^^^^^^^^^^ использует ctx (оригинальный db), НЕ tx!
```

**Upsert** в `repository.go:198`:
```go
func (r *gormTournamentResultRepo) Upsert(ctx context.Context, result *TournamentResult) error {
    return r.db.WithContext(ctx).Save(result).Error  // пишет напрямую, не через tx
}
```

**Следствие:**
- Два конкурентных вызова читают один `resultMap` → оба инкрементят от одной базы → последний перезаписывает
- При ошибке транзакции Upsert-ы не откатываются
- **Очки турнира теряются под нагрузкой**

---

### C2. WebSocket — нет лимита на размер сообщения

**Файл:** `internal/pkg/websocket/client.go:155`, `room_hub.go:268`

**Проблема:** `SetReadLimit` не вызван. Дефолтный лимит gorilla/websocket — 32KB (`32769`). `BroadcastToRoom` не проверяет размер данных. Клиент может отправить 32KB сообщение, которое будет буферизировано у каждого получателя.

---

### C3. Pagination — нет верхней границы perPage

**Файл:** `internal/domain/game/game_passing_service.go:107-129`

```go
if perPage < 1 {
    perPage = 20
}
// НЕТ проверки perPage > max!
```

Запрос `perPage=1000000` выгрузит миллион записей в память. Для сравнения: `game_handler.go:108-111` и `admin/handler.go` имеют лимит 100.

---

### C4. Rate limits — compile-time константы

**Файл:** `internal/pkg/middleware/constants.go:10-16`

| Лимитер | Значение |
|---------|----------|
| Global | 100/min |
| Login | 5/min |
| Register | 3/min |
| Code submission | 10/min |
| SSE | 10/min |
| API | 60/min |

**Ни один не настраивается через env.** Для изменения нужно перекомпилировать.

---

### C5. `.env` закоммичен с секретами

**Файл:** `.env` (tracked in git)

Содержит `JWT_SECRET`, `SESSION_SECRET`, `DB_PASSWORD`, `ADMIN_PASSWORD`, VAPID keys в открытом виде. Никогда не должен быть в репозитории.

---

### C6. `TrustedProxies` никогда не загружается из env

**Файл:** `internal/config/config.go:61`

Поле `ServerConfig.TrustedProxies` определено в struct (line 61), но **никогда не присваивается** в `LoadConfig()`. Всегда пустая строка → `r.SetTrustedProxies(nil)` → `c.ClientIP()` видит только IP прокси. Rate limiting, audit logs, геолокация сломаны за reverse proxy.

---

### C7. `StopPasswordResetRateLimiter()` никогда не вызывается

**Файл:** `internal/pkg/middleware/rate_limiter.go:419-431`, `cmd/server/main.go:366-371`

Rate limiter создаётся в `PasswordResetRateLimit()` (lazy init), но cleanup-горутина никогда не останавливается — утечка горутины при shutdown.

---

### C8. Очки турнира не пересчитываются при удалении игры

**Файл:** `internal/domain/tournament/service.go:100-109`

`RemoveGame` удаляет `TournamentGame`, но **не откатывает очки** из `TournamentResult`. Команды, уже заработавшие очки на этой игре, сохраняют их навсегда. `UpdateScoresForGame` молча возвращается, если игра уже не в турнире (line 223-226).

---

### C9. Удалённый уровень в `AdvanceToNextLevel` — преждевременное завершение игры

**Файл:** `internal/domain/level/level_progress_service.go:182-185`

Если `AdvanceToNextLevel` вызван с ID удалённого уровня, `foundCurrent` остаётся `false` → игра завершается досрочно, пропуская все оставшиеся уровни.

---

### C10. Утечка файла при ошибке `io.Copy`

**Файл:** `internal/pkg/storage/local_storage.go:147-162`

При ошибке `io.Copy` (line 160) `fileErr` остаётся `nil`, поэтому deferred cleanup (line 152-157, проверяющий `fileErr != nil`) **не удаляет** частично записанный файл.

---

### C11. `i18n.T()` не используется нигде

**Файл:** Все `handler.go`, `service.go`, `*.html`

Пакет `i18n` содержит **357 ключей** с русскими и английскими переводами. Функции `T()` и `TF()` зарегистрированы в `template.FuncMap` как `{{ T }}`/`{{ TF }}`. 

**Но:** ни один Go-хендлер не вызывает `i18n.T()`, и ни один HTML-шаблон не использует `{{ T }}`. Все строки хардкодом на русском (~200+ уникальных фраз в 15 файлах хендлеров, 60+ шаблонов, JS во встроенных скриптах). **Вся инфраструктура i18n — мёртвый код.**

**Топ-5 самых частых хардкодных строк:**
- `"Неверный ID игры"` — 35+ раз
- `"Неверный ID команды"` — 10+ раз
- `"Неверный email или пароль"` — 5+ раз
- `"Внутренняя ошибка сервера"` — 8+ раз
- Flash-сообщения в admin/handler.go — 8 шт.

---

## 🟠 HIGH

### H1. Нет `LOG_LEVEL` env var

**Файл:** `cmd/server/main.go:77`, `internal/config/config.go`

`zerolog.SetGlobalLevel()` не вызывается. Все `Debug()` логи невидимы в production. Для отладки нужна перекомпиляция.

### H2. Cache `SCAN+DEL` race — stale keys

**Файл:** `internal/pkg/cache/valkey.go:196-223`

Между `SCAN` (возврат ключей) и `DEL` (удаление) конкурентная горутина может создать новый ключ с тем же префиксом. Stale key живёт до TTL (по умолчанию 5 минут).

### H3. GameListingService — `OFFSET` может стать отрицательным

**Файл:** `internal/domain/game/game_listing_service.go:122`

```go
b.WriteString(" LIMIT " + strconv.Itoa(perPage) + " OFFSET " + strconv.Itoa(offset))
```

`strconv.Itoa` не позволяет SQL injection, но при `page=0` (в обход хендлера) `offset = (0-1)*perPage = -perPage`. PostgreSQL вернёт синтаксическую ошибку — DoS.

### H4. OAuth callback — GET (login CSRF)

**Файл:** `internal/domain/user/auth_handler.go:554`

`OAuthCallback` — GET handler. Атакующий может инициировать OAuth flow и заставить жертву завершить его через подставную ссылку. Частично защищено `state` параметром, но login CSRF возможен.

### H5. `GetLogsByGameID` требует JOIN через `game_passings`

Это уже документировано в AGENTS.md как gotcha.

### H6. `isStopped()` — `Lock()` вместо `RLock()`

Уже исправлено в pass 12 (`decConnection` удалена).

### H7. Нет request-ID middleware

**Файл:** `internal/pkg/logging/` (correlation ID существует), `internal/app/router.go` (не подключён)

Корреляция логов по запросу отсутствует.

---

## 🟡 MEDIUM

| # | Область | Файл | Описание |
|---|---------|------|----------|
| M1 | Stripe — dead config | `.env.example:73-78` | Переменные Stripe описаны, но не загружаются в config.go |
| M2 | `InitPasswordResetRateLimiter` не вызван при старте | `main.go:194-211` | Rate limiter лениво инициализируется, cleanup не останавливается |
| M3 | Valkey health — `IsAvailable()` а не ping | `health.go:175` | Не обнаружит падение Valkey после старта |
| M4 | `/metrics` требует admin | `router.go:105-111` | Prometheus обычно нужен неаутентифицированный доступ |
| M5 | Нет liveness vs readiness | `health.go` | Один эндпоинт для обоих probes |
| M6 | `getEnvAsInt` молча глотает ошибки | `config.go:482-508` | `DB_MAX_OPEN_CONNS=abc` → fallback без лога |
| M7 | 14 env vars не документированы в `.env.example` | `config.go` vs `.env.example` | CORS, LOG_*, MAX_*, WS_*, UPLOADS, STATIC |
| M8 | Duplicate migration entry points | `main.go:172`, `makefile:26` | `cmd/server -migrate` + `cmd/migrate` |
| M9 | DB connection close не ждёт запросы | `main.go:411` | `sql.DB.Close()` не ждёт активные запросы |
| M10 | SMTP health — только статистика | `health.go:196` | Не проверяет SMTP-соединение |
| M11 | `RemoveGame` не пересчитывает очки | `tournament/service.go:100-109` | Stale оски остаются после удаления игры |
| M12 | `DeleteLevelFromActiveGame` пропускает `StatusTesting` | `game_admin_service.go:178-183` | Test passings на уровне не продвигаются |
| M13 | Наносекундный коллизионный window | `local_storage.go:126` | Два одновременных upload могут перезаписать друг друга |
| M14 | `BackupService` — PGPASSWORD в environ | `backup_service.go:76-81` | Виден в `/proc/PID/environ` |
| M15 | Flash-сообщения хардкодом | `admin/handler.go:213-398` | 8 flash-сообщений: `i18n.T()` не используется |
| M16 | Нет `make production` target | `makefile` | Нет production-сборки с CGO_ENABLED=0 |

---

## ✅ Исправления pass 13-14 — подтверждены

| ID | Проблема | Статус |
|----|----------|--------|
| C1-C10 (pass 13) | CSP onclick, 2FA, Admin flash, etc. | ✅ Все 10 |
| C1-C2 (pass 14) | 2FA Disable field + CSRF | ✅ |
| H1-H4 (pass 14) | SubmitFile, profile 2FA, backup codes | ✅ |
| M1-M2 (pass 14) | VerificationCode test, audit.Service | ✅ |

---

## 📊 Статистика

| Приоритет | Количество |
|-----------|-----------|
| 🔴 CRITICAL | 11 |
| 🟠 HIGH | 7 |
| 🟡 MEDIUM | 16 |
| 🔵 LOW | 5 |

---

## Резюме

После 15 раундов ревью проект в хорошем состоянии, но есть **11 критических проблем**:

### Топ-5 по реальному impact:

1. **Tournament Upsert bypass (C1)** — очки теряются под нагрузкой, транзакция не работает
2. **i18n — мёртвый код (C11)** — 357 ключей перевода существуют, 0 используются. Вся локализация — хардкод на русском
3. **TrustedProxies не загружается (C6)** — `c.ClientIP()` сломан за reverse proxy
4. **`.env` закоммичен (C5)** — все секреты в репозитории
5. **Rate limits — compile-time (C4)** — нельзя настроить без перекомпиляции

### Что бросается в глаза:
- **Безопасность**: Tournament Upsert bypass — самый опасный баг. Под нагрузкой очки теряются без возможности восстановления.
- **Deployment**: `.env` с секретами в git и `TrustedProxies` — блокеры для production.
- **i18n**: 357 ключей перевода — мёртвый код. Если мультиязычность не нужна, стоит удалить i18n инфраструктуру.
- **Edge cases**: Tournament score recalculation, partial file leak, deleted level crash — редкие, но разрушительные.
