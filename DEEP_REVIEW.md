# DEEP_REVIEW — Gengine-0 (PASS 8)

> Глубокое ревью после закрытия PASS-7.
> Метод: pprof-профилирование (PPROF_ENABLED=true, :6060, loopback) + 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая проверка каждого HIGH/MEDIUM finding.
> Архивы: `DEEP_REVIEW_2026-08-11_pass{1,2,3,4}.md`, `DEEP_REVIEW_2026-08-12_pass5.md`, `DEEP_REVIEW_2026-08-12_pass7.md`.

---

## 🔬 pprof-результаты (PASS 8)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 18 в покое | ✅ Без утечек (стабильно с PASS-5/6/7). |
| **heap inuse** | 20.6 MB | ✅ Норма. 46.9% — инициализационная память `golang.org/x/net/webdav` (не код приложения). |
| **heap alloc** | 96.9 MB cumulative | ⚠️ `text/template evalCall` 21.2%, `reflect.Value.call` 13.4% — рендер шаблонов доминирует. |
| **cpu** | 47.9% cgocall (network I/O), 21.5% template | ⚠️ Рендер HTML — главная статья CPU. |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback. |

**Вывод**: профиль чистый от утечек; бутылочное горлышко — рендер HTML-шаблонов (ожидаемо для SSR) и DB-запросы на горячих маршрутах (см. perf-раздел).

---

## 🔴 HIGH (reviewer — корректность)

### H1. `CloseVoting`: тай-брейк выбирает лексикографически ПОСЛЕДНИЙ вариант 🔍✅ (подтверждено)
- **Файл**: `internal/domain/monitor/service.go:302-308`.
- **Проблема**: `if count >= maxVotes` (стр. 304) — при равенстве голосов вариант, идущий ПОЗЖЕ в отсортированном списке, перезаписывает победителя. Комментарий (стр. 295-296) обещает «лексикографически первый».
- **Фикс**: `>=` → `>` (первый лексикографически выигрывает, как заявлено) + unit-тест на равенство голосов.

### H2. Экспорт CSV: ошибка `csvWriter.Flush()` не проверяется 🔍✅ (подтверждено)
- **Файл**: `internal/domain/export/service.go:253` и `:400` (`defer csvWriter.Flush()` без `Error()`).
- **Проблема**: при обрыве соединения клиент получает «успешный» ответ с неполным файлом. `ExportGameToCSV` (стр. 176-177) уже исправлен, но два других метода пропущены.
- **Фикс**: единый паттерн — явный `Flush()` + `return csvWriter.Error()`.

### H3. `Cache.DeleteByPrefix`: гонка — потеря инвалидации навсегда 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/cache/cache.go:220-252`.
- **Проблема**: между копированием ключей под `RLock` (стр. 221-232) и `delete(c.prefixKeys, prefix)` (стр. 242) конкурентный `Set` может добавить новый ключ в `prefixKeys[prefix]`; удаление всей записи сотрёт трекинг и нового ключа → ключ останется в LRU без инвалидации (stale-кэш навсегда).
- **Фикс**: удаление `prefixKeys[prefix]` внутри той же критической секции, что и удаление ключей из LRU (без окна между чтением и чисткой).

---

## 🔴 HIGH (security)

### S-H1. Вебхук ЮKassa: единственная аутентификация — IP-allowlist при опциональном Basic 🔍
- **Файл**: `internal/domain/payment/service.go:373-425`, `handler.go:127-135`.
- **Проблема**: `Authorization` опционален (ЮKassa не шлёт Basic), защита держится только на `isYooKassaIP(ClientIP())`. При неверной конфигурации `TRUSTED_PROXIES` атакующий подделывает `XFF` и шлёт произвольное тело.
- **Рекомендация**: обязательная подпись вебхука, документирование конфигурации прокси (`$proxy_add_x_forwarded_for`), rate-limit на `/payments/webhook`.

### S-H2. CSS-injection (stored) через `style`-атрибут rich-text 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/sanitize/sanitize.go:17`.
- **Проблема**: `AllowAttrs("style")` без `AllowStyles()` — bluemonday пропускает сырой `style` как есть; рендерится как `template.HTML` → CSS-injection (оверлей/фишинг/трекинг) в доверенном origin.
- **Рекомендация**: `AllowStyles()` с конкретным списком свойств или убрать `style` вовсе.

---

## 🟠 MEDIUM (security)

### S-M1. Rate-limiter: переданные лимиты игнорируются, общие бюджеты 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/middleware/rate_limiter.go:363-378`, `cmd/server/main.go:246,259`.
- **Проблема**: `OAuthRateLimit(5m, 10)` передаёт limit=10, но используется глобальный `oauthRateLimiter` с `RateLimitLoginRequests` (5) — мёртвый параметр. `/api/users/search`, webauthn, 2FA, refresh делят общий `login:<ip>` бюджет → спам дешёвым эндпоинтом блокирует вход всем за NAT.
- **Рекомендация**: отдельные limiter-инстансы на эндпоинт, свои ключи (`search:`, `webauthn:`, `2fa:`).

### S-M2. In-memory rate limiter: обход при нескольких инстансах 🔍
- **Файл**: `rate_limiter.go:229-231,385-416`.
- **Проблема**: `NewRateLimiter` — per-process; `PersonalChatRateLimit`/`CreateRoomRateLimit` всегда in-memory даже при Valkey. При N инстансах лимиты умножаются.
- **Рекомендация**: Valkey для критичных per-user лимитов.

### S-M3. DoS через lockout ротацией IP 🔍
- **Файл**: `internal/domain/user/service.go:179-208`.
- **Проблема**: 5 неверных паролей с разных IP блокируют аккаунт (backoff до 24ч) — гарантированный DoS конкретного пользователя.
- **Рекомендация**: мягкий троттлинг account+IP вместо жёсткого лока, CAPTCHA на логин.

### S-M4. Спам в чате через множество соединений 🔍
- **Файл**: `internal/domain/monitor/handler.go:639-644,731,833-836`.
- **Проблема**: лимит 10 сообщений/5с на соединение; 50 сокетов с IP → ~100 сообщений/сек.
- **Рекомендация**: общий per-user token bucket, лимит активных чат-сокетов на пользователя.

### S-L1. `ChangePassword`: гонка расчёта блокировки 🔍
- **Файл**: `internal/domain/user/service.go:580-582`.
- **Проблема**: LockCount читается до атомарного инкремента (в Login исправлено, здесь нет).
- **Рекомендация**: использовать `LockAccountWithBackoff`.

### S-L2. `WebhookKey` по умолчанию = `SecretKey` 🔍
- **Файл**: `internal/config/config.go:369-371`.
- **Проблема**: при утечке подписи вебхука компрометируется API-ключ.
- **Рекомендация**: отдельный `YKASSA_WEBHOOK_KEY` в strict-режиме.

---

## 🟠 MEDIUM (reviewer)

### M1. WS read-loop MonitorWS/LogsWS: контекст наблюдается раз в 60с 🔍
- **Файл**: `internal/pkg/websocket/client.go:184-209`, `monitor/handler.go:555,1359`.
- **Проблема**: старый паттерн (неблокирующий select + ReadMessage с deadline 60с) — при тихом обрыве соединение/горутины живут до 60с. В ChatWS эта же проблема исправлена (строки 739-765).
- **Рекомендация**: вынести чтение в горутину с каналом (паттерн ChatWS).

### M2. Perm-кэш чата: окно до 5с для удалённого из команды 🔍
- **Файл**: `internal/domain/monitor/repository.go:409-431`.
- **Проблема**: исключённый из команды может писать до 5с (инвалидация только при AddRoomMember).
- **Рекомендация**: инвалидировать при изменении членства команды.

### M3. OAuth VK: `email` через type-assertion без `extraString` 🔍
- **Файл**: `internal/domain/user/oauth_service.go:181`.
- **Проблема**: `token.Extra("email").(string)` молча теряет float64.
- **Рекомендация**: использовать `extraString`.

### M4. GetOrCreate* комнат: полагание на неявный unique-индекс 🔍
- **Файл**: `internal/domain/monitor/repository.go:148-270`.
- **Рекомендация**: `ON CONFLICT DO NOTHING` + повторное чтение, сохранять исходную ошибку.

### M5. `CalculateResults`: отрицательная длительность не клампится 🔍
- **Файл**: `internal/domain/game/svc_monitor.go:311-316`.
- **Проблема**: в GameSnapshot отрицательная длительность клампится (MEDIUM #15), в CalculateResults — нет → мусор в БД.
- **Рекомендация**: клампить при накоплении.

---

## 🔴 HIGH (perf)

### P-H1. `GetGamesView`: отдельный SELECT на каждый `/games` без кэша 🔍✅ (подтверждено)
- **Файл**: `internal/domain/user/repository.go:364-374`, `game/hnd_game.go:169`.
- **Проблема**: для каждого авторизованного пользователя при просмотре списка игр — синхронный SELECT. Рядом есть `themeCache` 60s по той же природе (редко меняется, часто читается).
- **Ожидаемый выигрыш**: −1 DB round-trip на самый горячий авторизованный GET (100 RPS → −100 SELECT/сек).
- **Рекомендация**: кэш `games_view` на 60с по образцу `themeCache`, инвалидация в `SaveGamesView`.

### P-H2. Full-preview игры не кэшируется 🔍
- **Файл**: `internal/domain/game/repository.go:143`, `level/repository.go:122`, `hnd_fullpreview.go:90`.
- **Проблема**: `Preload("Levels.Questions.Answers")` (включая коды ответов) на каждый GET `/games/{id}/full-preview` — тысячи строк/мегабайт на запрос; `GetByID` кэшируется, full-preview нет.
- **Рекомендация**: кэш `game:fullpreview:%d`, инвалидация при обновлении игры.

---

## 🟠 MEDIUM (perf)

### P-M1. SSE `Broadcast`: глобальный мьютекс на все игры 🔍✅ (подтверждено)
- **Файл**: `internal/domain/game/hnd_sse.go:313-346`.
- **Проблема**: каждый broadcast берёт глобальный `m.mu` (сериализация всех игр), копирует слайс, аллоцирует payload/map/[]byte.
- **Рекомендация**: per-game lock (`map[uint]*sync.RWMutex`), `sync.Pool` для буферов события.

### P-M2. WS `dispatchToRoom`: аллокация слайса на каждое сообщение 🔍
- **Файл**: `internal/pkg/websocket/room_hub.go:310-361`.
- **Проблема**: RLock → `make([]*Client, ...)` → RUnlock → отправка → повторный Lock; 2 лока + аллокация на сообщение.
- **Рекомендация**: кэшировать слайс клиентов комнаты (инвалидация на register/unregister).

### P-M3. Рендер: 2 прохода шаблона + копия буфера, нет фрагментного кэша 🔍
- **Файл**: `internal/pkg/render/helper.go:224-244`.
- **Проблема**: `buf.String()` копирует HTML; layout (шапка/навигация) рендерится на каждый запрос; CPU-профиль подтверждает (evalCommand/evalCall 21%).
- **Рекомендация**: `sync.Pool` для `bytes.Buffer`, кэш layout-фрагмента (ключ lang+theme+isAdmin), кэш статических публичных страниц 60с.

### P-M4. Unbounded `Find()` без LIMIT 🔍
- **Файл**: `user/repository.go:333,415`, `team/repository.go:388`, `notification/repository.go:135`.
- **Рекомендация**: `Limit` + пагинация (паттерн `ListPaginated` уже есть).

### P-L1. `i18n.TF` всегда `fmt.Sprintf` 🔍
- **Файл**: `internal/pkg/i18n/i18n.go:56-58`.
- **Рекомендация**: fast-path `len(args)==0` → без Sprintf.

### P-L2. `GetGameplayData`: 5 round-trip на страницу уровня 🔍
- **Файл**: `internal/domain/game/svc_play.go:733-861`.
- **Рекомендация**: короткий TTL-кэш (5-10с) для progress+attempts.

### P-L3. `wsMessageLimiter`: O(n) переписывание слайса 🔍
- **Файл**: `internal/domain/monitor/handler.go:111-132`.
- **Рекомендация**: классический token bucket O(1).

---

## ⚪ LOW (reviewer)

- **L1** `svc_play.go:207-214` — дублированное условие `if result.GameID != 0`.
- **L2** `svc_progress.go:546` — мёртвая проверка `firstLevel.ID == 0` (GORM уже возвращает ErrRecordNotFound).
- **L3** `room_hub.go:256-265` — orphan-воркер очереди при гонке broadcast/удаление (утечка ограничена 30с).

---

## ✅ Проверено — проблем НЕ найдено

- **JWT**: HMAC-метод закреплён, iss/aud/nbf/iat, jti-blacklist, отзыв при logout/reset.
- **Роли**: перечитываются из БД (TTL 5с), fail-closed, удалённый пользователь отзывается.
- **OAuth**: state через `subtle.ConstantTimeCompare`, привязка к провайдеру, TTL 10 мин.
- **WebAuthn**: привязка session-ключа к userID, `userHandle`, отклонение CloneWarning.
- **Refresh-токены**: хэш в БД, ротация с атомарным `ClaimAndCreate`, отзыв семьи при reuse.
- **Uploads**: блок `..`, отбрасывание абсолютных путей, проверка прав, nosniff.
- **Trusted proxies**: при пустом `TRUSTED_PROXIES` → `SetTrustedProxies(nil)` (fail-closed).
- **CSRF**: SameSite=Strict + nonce-CSP, регистронезависимый X-CSRF-Token (PASS-7).
- **N+1**: batch-запросы, `COUNT(*) OVER()`, `EXISTS`, singleflight в listing/calendar/monitor — образцово.
- **Шаблоны**: парсятся один раз (`ParseGlob`), dev-режим через fsnotify.

---

## 💡 Предложения по улучшению кодовой базы

1. **Закрыть H1-H3 + S-H2 + P-H1** (бизнес-логика, безопасность, самый горячий маршрут) — приоритет 1.
2. **Привести к единому паттерну**: WS read-loop (ChatWS vs MonitorWS/LogsWS), CSV Flush, клампинг длительности (GameSnapshot vs CalculateResults) — устранить расхождения-близнецы.
3. **Развести rate-limit бюджеты**: отдельные инстансы/ключи на эндпоинт (убрать мёртвые параметры).
4. **Инвалидация perm-кэша чата** при изменении членства команды.
5. **Add `go vet` и `golangci-lint` в pre-commit** (сейчас только в CI) + `go test -race` в dev-цикл.

---

## 💡 Предложения по пользовательскому опыту (UX)

1. **404/ошибки**: единый, дружелюбный шаблон ошибки с поиском по сайту вместо голого «404» (сейчас `errors-429.html` отдельно, остальные — дефолт).
2. **Скелетоны при загрузке**: уже есть в чате (`chat-skeleton`), распространить на списки игр/турниров.
3. **Оптимистичный UI**: отправка сообщения без ожидания WS-подтверждения (сейчас — только после серверного broadcast).
4. **Уведомления о статусе**: toasts при принятии личного чата уже есть; добавить при успешном создании игры/уровня.
5. **Пагинация/бесконечная лента** на списках (сейчас unbounded Find → и UX страдает при росте данных).
6. **Тёмная тема**: уже реализована; добавить auto-переключение по системной теме.
7. **A11y**: проверить контраст баннера согласия чата (yellow-50 на белом), добавить aria-live для тостов.
8. **Производительность UX**: кэшировать full-preview → мгновенный повторный предпросмотр игры при редактировании.

---

## 📋 Статус

- 3 аудита: ✅ проведены; HIGH/MEDIUM findings — ✅ эмпирически проверены (H1-H3, S-H2, S-M1, P-H1, P-M1).
- **Исправлено в этом проходе (после ревью): 18/18 findings** ✅
  - H1 tie-break, H2 CSV Flush, H3 DeleteByPrefix race, S-H2 AllowStyles, S-M1 rate-limit бюджеты,
    M1 WS read-loop, M2 perm-cache инвалидация (wire), M3 VK email, M4 unique-индексы (миграция 000067),
    M5 клампинг длительности, P-H1 games-view кэш, P-H2 full-preview кэш, P-M1 SSE RLock, P-M2 roomClients кэш,
    P-M4 LIMIT, P-L1 i18n fast-path, P-L3 token bucket, L1/L2/L3.
  - Бонус: найден и исправлен предсуществующий флаки-баг `SkipLevelTest` (HasPermission без tx внутри транзакции → HasPermissionTx).
  - Проверки: build ✅, test-short ✅, test-integration ✅, golangci-lint ✅ (0 issues), E2E 14/14 ✅.
- **Дополнительный аудит (admin+payment+IDOR) — исправлено:**
  - IDOR CRITICAL: `DeleteLevelFromActiveGame` не проверял `lvl.GameID == gameID` (удаление чужого уровня) → фикс.
  - IDOR HIGH: `GetTeamRoute` без сверки `passing.GameID == gameID` (чтение маршрута чужой команды) → фикс + интерфейс/mock.
  - Payment #1 (TOCTOU CreatePayment): уникальный индекс уже был (PASS-6 H1); добавлена идемпотентная обработка 23505 (возврат существующего pending вместо 500).
  - Payment #2: отдельный `PaymentRateLimit` (общий бюджет с кодами + мёртвые параметры) → фикс.
  - Payment #3: `CancelIfPending` (canceled не откатывает succeeded).
  - Payment #7: `pendingExpiry` (зависший pending >2ч помечается canceled, создаётся новый платёж).
  - Payment #8: валидация суммы/длины в сервисе (defense-in-depth).
  - S-L1: ChangePassword/SetLockedUntil/2FA lockUser → `LockAccountWithBackoff` (гонка lock_count).
  - S-L2: `YKASSA_WEBHOOK_KEY` обязателен в strict-режиме (не fallback на SecretKey).
  - S-M3: max backoff 24ч→1ч (меньше DoS-ущерб) + мягкий троттлинг (300мс) на неверный пароль.
  - S-M4: per-user token bucket в чате (агрегирует соединения).
- Ограничения: аудиторы упёрлись в лимит шагов — не покрыты admin-домен, часть game svc_*, team/tournament/payment полностью, полная инвентаризация шаблонов на XSS, IDOR game/level-маршрутов.
