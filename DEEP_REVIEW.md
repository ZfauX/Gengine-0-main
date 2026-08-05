# Deep Review Gengine-0 — 5 августа 2026 (pass 21)

Повторный глубокий аудит после волны исправлений pass 20 (C7 i18n, UX/PWA, миграции 000025–000029, репозиторный слой, безопасность, кэш).

**Методология:** 5 параллельных ревью-агентов (безопасность, производительность, корректность/архитектура, фронтенд/UX, тестовое покрытие) + **выборочная ручная верификация каждого критического/высокого пункта по коду и по внутренностям GORM v1.26.0**. Ложные срабатывания агентов исключены и перечислены отдельно.

**Легенда:** 🔴 критично · 🟠 высоко · 🟡 средне · 🟢 хорошо · ✅ проверено-исправлено/подтверждено

---

## 1. 🔴 Критические проблемы

### 1.1 Функциональность (мёртвый код)

**H1. Детекция подозрительных команд (`analyzeTeamsBehavior`) никогда не срабатывает** — подтверждено
- `internal/domain/game/svc_monitor.go:153-158, 424-441`
- SQL: `SELECT attempts.level_progress_id, attempts.code, attempts.success, attempts.created_at` → в структуру `attemptRecord{PassingID uint; ...}`. GORM мапит колонки **по имени**: `level_progress_id` не соответствует полю `PassingID` (нужна колонка `passing_id`). `PassingID` всегда `0`, все попытки группируются в `attemptsByPassing[0]`, а `passingToTeam[0]` не существует → `suspiciousMap` никогда не содержит реальный `TeamID`.
- **Результат:** функция «подозрительная частота попыток» и «серия одинаковых неверных кодов» в мониторинге — мёртвая. UI вечно показывает `Suspicious: false`.
- **Фикс:** переименовать поле в `LevelProgressID uint` и группировать через `level_progresses.game_passing_id`, либо добавить в SELECT алиас `attempts.level_progress_id AS passing_id` (после джойна с `level_progresses` это `lp.game_passing_id`). После включения — **пересмотреть порог** `checkSuspiciousAttempts` (`:478`, считает и успешные попытки, 10/мин — жёсткий).

### 1.2 Безопасность

**S1. Глобальный rate limiter инициализируется, но никогда не регистрируется** — подтверждено
- `cmd/server/main.go:216,225` вызывает `InitGlobalRateLimiter`, но в `internal/app/router.go:67-126` среди `r.Use(...)` нет `middleware.GlobalRateLimit`. В кодовой базе `GlobalRateLimit` встречается только в определении (`rate_limiter.go:270`) и тестах.
- **Результат:** `RATE_LIMIT_GLOBAL=100` — мёртвая конфигурация. Нет глобального per-IP лимита: бесконечные подключения SSE/WS (до per-manager-кэпов), скрейпинг поиска, дорогие листинги.
- **Фикс:** `r.Use(middleware.GlobalRateLimit(cfg.Server.RateLimitWindow, cfg.Server.RateLimitGlobalRequests))` в `setupEngine` (перед gzip).

**S2. Роль доверяется JWT-claims и не перечитывается из БД** — подтверждено
- `middleware/auth.go:31,43` (`userID, role := parser.ParseToken(...)` → `c.Set("role", role)`); `user/service.go:361-364` кладёт `role` в claims.
- **Результат:** пониженный/заблокированный/удалённый админ сохраняет admin-права до истечения access-токена (TTL 15 мин). `repository.go:272` есть `GetUserRole`, но он не используется в middleware.
- **Фикс:** в `AuthRequired`/`AdminRequired` перечитывать `users.role` из БД (короткий TTL-кэш) и перезаписывать контекст; в `DeleteUser`/`ToggleAdmin` — `RevokeJWT` всех токенов пользователя.

### 1.3 Производительность

**S3. `broadcastSnapshot` + `CalculateResults` синхронно на КАЖДУЮ попытку кода** — подтверждено
- `svc_play.go:159-172`: после каждого `SubmitCode` (даже неверного): `broadcastLevelComplete` → `broadcastSnapshot` (инвалидация кэша мониторинга → полный пересчёт CTE `GameSnapshot` → `json.Marshal` → broadcast) → `CalculateResults` (два больших `UPDATE ... CASE`). Всё внутри HTTP-запроса. Комментарий явно фиксирует решение «синхронно из-за гонки с закрытием БД в тестах».
- `svc_monitor.go:424-429` — в каждый снапшот входит оконный запрос по attempts за 5 мин.
- `broadcastSnapshot` (`:573-578`) маршалит снапшот заново на каждое событие, даже при пустой комнате.
- **Фикс:** (1) вызывать `CalculateResults` в `SubmitCode` только если уровень не последний (колбэк финиша уже делает это) — убирает двойной проход на финише; (2) broadcast только при верном ответе/изменении видимых полей; (3) вынести снапшот в фоновый per-game worker с debounce ~300–500 мс и singleflight, отдавая подписчикам закэшированные байты снапшота; (4) в тестах закрывать БД после остановки воркера (через WaitGroup), а не через «синхронность».

---

## 2. 🟠 Высокие проблемы

### Безопасность

**S4. Refresh-ротация не атомарна — гонка отменяет детект reuse** — подтверждено
- `user/service.go:237-279`: `GetByTokenHash` → `Revoke(ctx, stored.ID)` → `Create`. Два параллельных запроса с одним токеном оба проходят `GetByTokenHash` до revoke, оба получают новые токены в той же семье — кража не детектится, сессия жертвы не убивается.
- **Фикс:** атомарный claim: `UPDATE refresh_tokens SET revoked_at=now() WHERE id=? AND revoked_at IS NULL` и проверка `RowsAffected == 1` до генерации наследника (в транзакции).

**S5. Fingerprint-binding обходится пустой строкой** — подтверждено
- `user/service.go:256`: `stored.ClientFingerprint != "" && clientFingerprint != "" && stored != client` — если клиент не присылает fingerprint, проверка пропускается.
- **Фикс:** при непустом `stored.ClientFingerprint` требовать совпадения (отклонять пустой/несовпадающий).

**S6. Просмотр соавторов любой игры (IDOR/информация)** — подтверждено
- `game/hnd_coauthor.go:45-76`, `svc_coauthor.go:139-144`: `GET /games/:id/co-authors` вызывает `List` без проверки прав менеджера (в отличие от Add/Remove).
- **Фикс:** гейтить `IsUserManager` (403 иначе).

**S7. Session-cookie `Secure` зависит только от локального TLS-сертификата** — подтверждено
- `app/app.go:88-101`: `gengine_session` получает `Secure` только при `Config.TLS.CertFile != ""`, тогда как JWT-куки учитывают `X-Forwarded-Proto`/`FORCE_SECURE_COOKIE`. За TLS-терминирующим прокси сессионная кука уходит по HTTP.
- **Фикс:** использовать то же определение HTTPS, что и для JWT-кук.

**S8. CSP `style-src 'unsafe-inline'`** — подтверждено
- `middleware/security.go:63`. Остальной CSP сильный (nonce, `form-action 'self'`, без `'unsafe-eval'`). Инлайн-стили ослабляют containment (стилизация/фишинг через инъекцию атрибута). Принять риск или ужесточить.

**S9. Deleted-пользователь: JWT валиден до expiry; `/api/users/search` без лимита** — подтверждено
- `user/service.go:337-366` `ParseToken` не проверяет существование/статус пользователя; `/api/users/search` (`user/routes.go:142`, `handler.go:79-103`) — публичный, без rate limit, отдаёт маскированные email + ID (enumeration).

### Корректность / транзакции

**B1. `checkTimeoutsImpl`: колбэк финиша вызывается ДО коммита** — подтверждено
- `svc_progress.go:366-370`: `defer onCommitCopy()` внутри closure `db.Transaction` — вызывается при возврате из closure, т.е. **до** commit GORM. Противоречит паттерну `svc_play.go:98-156` (вызов `onCommitFn` после возврата `db.Transaction`). Турнирные очки могут считаться по незакоммиченным данным; при провале коммита очки уже начислены.
- **Фикс:** собирать колбэки в слайс и вызывать после возврата `db.Transaction`.

**B2. `CalculateResults` не сериализован с параллельными финишами** — подтверждено
- `svc_monitor.go:270-374`: постит-коммит без транзакции/блокировки; два команды финишируют одновременно → второе выполнение перезаписывает `place` первого, лидерборд/уведомления видят транзиентный неверный порядок.
- **Фикс:** `db.Transaction` + `FOR UPDATE` на finished passings (паттерн `pg_advisory_xact_lock(gameID)` уже есть в `level/service.go:223`).

**B3. `UpdateRatingsForGame` не идемпотентен и глотает ошибки** — подтверждено
- `svc_rating.go:29-138`: начисляет автору 5 очков при каждом вызове независимо от того, сколько раз сработал финишный путь; вне транзакции; ошибки логируются и возвращаются как `nil`.
- **Фикс:** привязать начисление автору и командам к тому же guard'у, что турнирные очки (флаг `tournament_scored`/отдельный `ratings_scored`), в одной транзакции.

**B4. Маскировка ошибок БД как пользовательских** — подтверждено
- `hnd_gameplay.go:359-361` (`AcceptBlackboxAnswer` → 403 с сырым сообщением любой ошибки), `svc_play.go:340-345`.
- `hnd_gameplay.go:567-582`: `ok, _ := isTeamMember(...)` — ошибка БД трактуется как «не член команды» → 403 вместо 500.
- `level/service.go:234-247`: `Move` — любая ошибка sibling-запроса → «некуда двигать».
- **Фикс:** различать `gorm.ErrRecordNotFound`/sentinel-ошибки (403/404) от прочих (500, generic).

**B5. Расхождение дефолтов настроек геймплея** — подтверждено
- `svc_play.go:596-602` (`GetGameplayData`) падает на zero-value `GameSetting` (hints off), а `svc_play.go:251-258` (`UseHint`) — на `AllowHints:true, HintPenaltySeconds:300, MaxHints:3`. Страница геймплея врёт про доступность подсказок.
- **Фикс:** общий `defaultGameSetting()` (совпадает с `service.go:486-503`).

**B6. `GetByID` — общий мутируемый указатель из кэша** — подтверждено
- `service.go:177-178`: in-memory cache возвращает тот же `*Game` всем запросам. Хендлеры заполняют `Author`, `GameSetting` и т.п. → гонка между запросами + порча кэша.
- **Фикс:** возвращать копию (`g := *game`) на cache hit (или immutable snapshot).

**B7. `GameService.Delete`: файлы удаляются до удаления строки** — подтверждено
- `service.go:251-268`: если `crudService.Delete` падает (FK), файлы уже удалены, строка остаётся с битой `cover_path`.
- **Фикс:** сначала DB, затем best-effort файлы.

**B8. `ManageCoAuthors`/`VotingSession` check-then-insert** — подтверждено
- `monitor/service.go:59-77` `StartVoting`: `GetSessionByPassingAndLevel` → `CreateSession` без уникального ограничения (в модели нет `uniqueIndex` на `(game_passing_id, level_id)`). Два параллельных открытия → две сессии.
- `monitor/service.go:238-300` `CloseVoting`: голоса читаются до `UpdateSession(IsOpen=false)` — голос, попавший между чтением и закрытием, не учтён.
- **Фикс:** частичный unique-индекс `WHERE is_open` + `ON CONFLICT DO NOTHING`; `FOR UPDATE` на сессию и перечитывание голосов в одной транзакции.

### Производительность

**P1. `SubmitCodeWithTx` дважды грузит уровень+ответы** — подтверждено
- `svc_play.go:102` (`GetCurrentProgressForUpdate` уже `Preload("Level.Questions.Answers")`) + `svc_attempt.go:36` повторно `Preload("Questions.Answers")`. На каждом submit двойные round-trip/аллокации.
- **Фикс:** принимать уже загруженный Level или грузить только нужные колонки.

**P2. `GetByID` без singleflight на cache-miss** — подтверждено
- `service.go:196-238`: ручной `cacheGetGame`+`Set`, нет `GetOrSet`. Горячая только что опубликованная игра с 100 параллельными первыми просмотрами = 100 одинаковых полных запросов с preload.
- **Фикс:** обернуть fill в `cache.GetOrSetWithCtx` (singleflight), проверку `CanViewGame` после.

**P3. Листинг: кэш фрагментируется и не инвалидируется на запись** — подтверждено
- `svc_listing.go:53`: ключ включает сырую search-строку — каждый уникальный поиск создаёт ключ, вытесняя анонимный листинг из LRU (10k).
- `service.go:145,246,321,330,534`: записи инвалидируют `game:%d`/`rating:game:%d`, но не `games:list:*` — до 30 с устаревшие страницы после публикации/редактирования.
- **Фикс:** кэшировать только безпоисковый анонимный листинг; `DeleteByPrefixWithCtx(ctx, "games:list:")` на запись.

**P4. Статика без long-lived cache-заголовков** — подтверждено
- `app/router.go:216-217`: `r.Static` без `Cache-Control`; `static_cache.go` ставит `immutable` для **всех** `/static/*`, включая неверсионированные `leaflet.js/css` и иконки — устаревшие копии навсегда.
- **Фикс:** `immutable` только для `?v=`-версионированных URL; `max-age=3600` для остальных; sw.js — `no-cache`.

**P5. `GetLogsByGameID` без LIMIT** — подтверждено
- `service.go:457-483`: непагинированная версия грузит все логи игры (пагинированная есть, но не используется на странице).

**P6. `notifications` ORDER BY без индекса `(user_id, created_at)`** — подтверждено
- `migrations/000017`, `notification/service.go:333`: индекс `(user_id, read)` не покрывает сортировку `created_at DESC` → filesort при пагинации.
- **Фикс:** индекс `(user_id, created_at DESC)`.

**P7. `sendWebSocketNotification` — COUNT на каждый вызов** — подтверждено
- `notification/service.go:300,313-319`: `getUnreadCount` в каждый push. Кэшировать или не слать в payload.

### Архитектура / качество

**C1. `StartTesting` накапливает orphan-тестовые команды** — подтверждено
- `svc_play.go:428-435`: каждый запуск создаёт `_test_<userID>` и не чистит старые.

**C2. Дублирование/мёртвый код**
- `GamePassingService.ListByGame` deprecated, но в интерфейсе (`interfaces.go:43`).
- `GameService` держит поля `hub`, `cfg`, `coAuthorSvc`, `db`, `gameRepo`, часть не используется (`service.go:73-87`).
- `GamePassingRepository` определён (`repository.go:33`) — проверить wire.
- `cacheGetGame` fallback `default:` (`service.go:179-190`) — json round-trip на cache hit, только для legacy-значений.

**C3. `ValkeyCache.GetOrSetStringWithTTLWithCtx` расходится с типизированными accessor'ами** — подтверждено
- `valkey.go:291-313`: читает через `Get(key)` (не `GetWithCtx`), без singleflight, JSON-маршалит строку → `[]LeaderboardEntry` в Valkey никогда не хитнет (`svc_rating.go:162`).

**C4. `Cache.Close()` — double-close panic** — подтверждено
- `cache.go:296-300`: `close(c.stop)` без `sync.Once`.

**C5. Миграция 000028 зависит от имени constraint'а**
- `000028_deferrable_level_position.up.sql`: `DROP CONSTRAINT IF EXISTS levels_game_id_position_key` — если GORM создал `idx_levels_game_position`, DROP — no-op, ADD дублирует. Добавить `pg_constraint`-проверку как в 000029.

**C6. `email`-рассылка из `auth_handler.go:611-628` — untracked goroutine**
- Вместо глобальной очереди `email.Enqueue` — сырая горутина на запрос, не отслеживается, не ожидается.

---

## 3. 🟡 Средние проблемы (UX/фронтенд, тесты)

### UX / фронтенд

**UX1. ~7 мёртвых JS-модулей** — подтверждено
- `app.js`: `initInlineValidation` (нет `[data-inline-validation]`), `initAutoSaveDrafts`/`initAutoSaveIndicator` (нет `[data-autosave]`), `initFileUploadProgress` (нет `[data-progress]`), `initSSEIndicator`/`initSSEGameNotifications` (нет `#sse-status`/`data-sse-game-id` нигде), `initTeamRatingIndicators` (нет `.team-row`), `initCodeCopy` (нет `[data-copy]`), `initSearchAutocomplete` (нет `#search`), `showSkeleton`/`hideSkeleton` + `skeleton-table.html` (не вызываются). ~200 строк мёртвого JS; SSE-фича «игра началась» фактически не подключена.
- **Фикс:** либо завести data-атрибуты/вызовы, либо удалить функции и роут `/game/sse`.

**UX2. `alert()` в 7 шаблонах** — подтверждено
- `monitor-page.html:240`, `games-photos.html:222,227`, `notes-manage.html:71,95`, `webauthn-login-button.html:14,56,63`, `webauthn-manage.html:93,100`, `profile-public.html:153,158`. Заменить на `showToast`.

**UX3. Сырые WebSocket-подключения** — подтверждено
- `layout.html:600` (`/ws/notifications` — без reconnect), `logs-list.html:75` + дублированный самодельный backoff (`logs-list.html:105-115`); `static/js/ws-client.js` — мёртвый код (~2 КБ/страницу).
- **Фикс:** мигрировать на `createReconnectingWebSocket`, удалить ws-client.js.

**UX4. A11y-дефициты** — подтверждено
- Чаты без `aria-live`/`role="log"`, `#connection-status` без `role="status"` (`team-chat.html:7`, `chat-page.html:15`).
- Модалка подтверждения без `role="dialog"`/`aria-modal` (`app.js:170-181`).
- Закрытие lightbox — `<span>` без кнопки/aria (`games-photos.html:54`).
- `layout.html:168` — `role="img" aria-hidden="true"` противоречиво.
- `layout.html:179`, `games-list.html:6` — bell/toggle без `aria-expanded`/`aria-pressed`.

**UX5. PWA-кэш**
- Ручная синхронизация трёх версий (`CACHE_NAME='gengine-v17'`, `?v=20260805`, precache-список). Один пропуск = сломанный offline.
- `manifest.json:5` — `start_url: "/dashboard?source=pwa"` под авторизацией; `/offline` роут есть (`router.go:185`).
- **Фикс:** версия из build-time константы; `start_url:"/"` + `scope`.

**UX6. Мелочи**
- `min-w-[180px]` инпуты чата/геймплея давят на ≤360px (`team-chat.html:13`, `chat-page.html:21`, `gameplay-show.html:32`).
- Дублирующий `<meta name="csrf-token">` в `games-photos.html:5`.
- `games-photos.html:28,55` generic `alt="Photo"`.
- Двойной `fetch`-враппер в layout (`:305-326` + `:515-525`) — хрупкая цепочка.
- Табы не гасятся на `document.hidden` (`layout.html:419`, `gameplay-show.html:192-196`).

### Тесты (покрытие)

**T1. 🔴 Критические пути БЕЗ тестов** — подтверждено
- Refresh-ротация/reuse/family (`RefreshAccessToken`) — нет тестов (в `user/service_test.go` — 0 упоминаний `RefreshAccessToken|family|reuse`).
- Lockout-логика (5 попыток → lock, generic-ответ, сброс при успехе).
- `TournamentService.UpdateScoresForGame` (математика мест, `tournament_scored`, повторный вызов) — тест `TestTournamentService_Leaderboard` вставляет `TournamentResult` напрямую, метод не вызывает.
- `CheckTimeouts`/`CheckAutoStartGames` — 0 упоминаний в тестах.
- WebAuthn — 0 тестов.

**T2. 🟡 Средние**
- Кэш листинга (`svc_listing_test.go` использует `NoopCache`), theme-middleware cache, tie-break голосования, Move на границах, Duplicate сдвиг позиций.
- 4 скипнутых теста 2FA/OAuth в `user/auth_handler_test.go`, `service_test.go`.
- Бизнес-логика тестируется только через реальный PG (`-short` всё скипает) — mock-слой есть, но почти не используется; нет mock-based юнит-тестов для auth/tournament/play.

---

## 4. ⚡ Варианты оптимизации (по приоритету)

### Быстрые и безопасные (дни)
1. **H1** — фикс маппинга `attemptRecord.LevelProgressID` (включит мёртвую детекцию) + порог.
2. **S1** — зарегистрировать `GlobalRateLimit` в `setupEngine` (1 строка).
3. **B1** — `onCommit` в `checkTimeoutsImpl` вызывать после `db.Transaction` (паттерн уже есть в svc_play).
4. **P1** — не грузить `Level.Questions.Answers` повторно в `SubmitCodeWithTx`.
5. **S3 (частично)** — не вызывать `CalculateResults` в `SubmitCode`, если уровень последний (колбэк уже делает).
6. **B6** — копия `*Game` на cache hit.
7. **P4** — cache-заголовки: immutable только для `?v=`.
8. **UX2/UX3** — заменить `alert()` на toasts, мигрировать raw-WS на reconnecting-клиент, удалить ws-client.js.
9. **UX1** — удалить/подключить мёртвые JS-модули.
10. **C4** — `sync.Once` в `Cache.Close`.

### Средние
11. **S4/S5** — атомарный claim refresh-токена + строгая fingerprint-проверка.
12. **S2** — роль из БД в middleware (TTL-кэш).
13. **B2/B3** — сериализация `CalculateResults`, идемпотентность `UpdateRatingsForGame`.
14. **S3 (полностью)** — debounced per-game snapshot worker + закэшированные байты снапшота.
15. **P2/P3** — singleflight `GetByID`, инвалидация `games:list:*`.
16. **B8** — unique-индекс сессий голосования + перечитывание в транзакции.
17. **UX4** — a11y-проход (aria-live чатов, dialog-роль, button для lightbox).
18. **P6/P7** — индекс `(user_id, created_at)`; кэш unread_count.
19. **C3/C5/C6** — Valkey-строки, миграция 000028, очередь email.

### Крупные архитектурные
20. **T1** — mock-based юнит-тесты для auth (refresh/lockout), tournament, timeouts; реальные PG-интеграционные через `//go:build integration`, а не implicit `-short`.
21. **C2** — почистить мёртвые интерфейсы/поля сервисов.
22. **Error-code-контракт API** — машинно-читаемые коды вместо русского текста.
23. **Единый realtime-клиент** — `createReconnectingWebSocket` для всех чатов/монитора/уведомлений; убрать три стека.

---

## 5. 🚀 Предложения по улучшению

### Кодовая база
- **Адvisory-блокировки** в `CheckTimeouts`/`CalculateResults` (паттерн уже есть в level).
- **Генератор версии статики**: единая build-time константа для SW/CACHE_NAME/`?v=`.
- **Index coverage**: `games.name` pg_trgm (сейчас `ILIKE '%..%'` без индекса, `migrations/000023` покрывает только users), `notifications(user_id, created_at DESC)`.
- **Слой кэша не для `[]LeaderboardEntry`** в Valkey — перейти на `GetBytesWithCtx`/хранение JSON-байт.
- **`go generate` + wire-проверка** в CI: падать, если DI-граф рассинхронизирован.
- **Логирование**: request-id, тайминги SQL в hot paths (SubmitCode, листинг).
- **Prometheus**: метрики на SubmitCode, листинг, SSE/WS-подключения, cache hit-ratio.
- **Pre-commit guard**: блокировать стейджинг `.env` (сейчас только `.gitignore`).

### Пользовательский опыт
- **Офлайн-first PWA**: `start_url:"/"`, `scope`, precache главных страниц, content-hash в URL статики.
- **Единая система фидбека**: только тосты + модалка, `alert()` удалить.
- **Пустые состояния** вместо skeleton (админка уже).
- **Живой статус чата/уведомлений**: единый статус-компонент reconnect (сейчас бэлл умирает молча на flaky-сети).
- **Адаптивность**: `min-w-0` на чатах, `w-full sm:w-32` в дашборде.
- **A11y**: `aria-live` чаты, dialog-роль модалки, `aria-pressed` toggle'ы, реальный `<button>` для lightbox, уникальные alt.
- **Персонализация**: серверные предпочтения (вид списка) — расширить на язык уведомлений (сейчас C7 хранит только ru-строки в БД).

---

## 6. ✅ Что сделано хорошо (подтверждено ревью)

- **Транзакции**: `FOR UPDATE` на игровых путях, broadcast/колбэки после commit в `SubmitCode`/`AcceptBlackboxAnswer`, `SkipDefaultTransaction`, onCommit-дисциплина.
- **Безопасность**: параметризованные запросы (0 SQL-инъекций найдено), ORDER BY-whitelist, CSP nonce, exact-origin WS/SSE, JWT iss/aud/jti + HMAC-method enforcement, SHA-256 хэши refresh-токенов, dummy-bcrypt cost 12, generic-ошибки логина, CSRF на HTML-формах, size-лимиты, whitelist загрузок + server-side sniffing, open-redirect guard.
- **Идемпотентность турнирных очков** ✅ — `ListFinishedPassings` фильтрует `tournament_scored = false` (repo:118-122), метка в той же транзакции.
- **Refresh-семьи**: reuse детектится для отозванных токенов (`RevokeAllByFamily`), fingerprint-binding в штатном случае.
- **Производительность**: bounded LRU 10k (P11 закрыт), singleflight в кэше и мониторе, batch SQL (`COUNT(*) OVER()`, CTE-снапшот, CASE-UPDATE, unnest), прекомпьютинг `rating_value`/`participant_count` (000027), неблокирующие WS-отправки (M4), partial-индексы на горячих фильтрах.
- **Фон**: все таски в `bgWg` + `goSafe` + graceful shutdown; монитор-poller без утечек.
- **UX**: единая модалка (focus-trap, restore, per-action label), reconnecting WS + polling fallback, wizard с progressive enhancement, `prefers-reduced-motion`, skip-link (один, рабочий), keyboard dropzones, локализация через `data-i18n` без видимого хардкода (0 mojibake после чистки).
- **Архитектура**: C1 закрыт (0 сырых `*gorm.DB` в хендлерах), sentinel-ошибки, idempotent-миграции, DI через wire, i18n ru/en синхронны.

---

## 7. Ложные срабатывания (проверено и исключено)

| № | Заявка агента | Вердикт |
|---|---|---|
| H2 | `UpdateScoresForGame` не идемпотентен | ❌ **Ложь** — `tournamentGameRepo.ListFinishedPassings` фильтрует `tournament_scored = false` (`repository.go:121`) |
| H4 | `FOR UPDATE` на UPDATE — синтаксическая ошибка PG | ❌ **Ложь** — в GORM v1.26 `updateClauses = ["UPDATE","SET","WHERE","RETURNING"]`, clause `LOCKING` не билдится для UPDATE — молчаливый no-op. Защиту даёт `RowsAffected`-проверка (`svc_progress.go:347`), которая корректна. Поправить вводящий в заблуждение комментарий |
| S-ChangePassword | Смена пароля не отзывает refresh-токены | ❌ **Ложь** — `profile_handler.go:388-394` вызывает `RevokeAllUserTokens` + `RevokeJWT` |
| M12 | `ON CONFLICT (game_id, team_id)` без unique-ограничения | ❌ **Ложь** — `000001_init.up.sql:168` создаёт `UNIQUE(game_id, team_id)`. Модель не декларирует его — drift модели, не баг |
| UX-шрифт | JetBrains Mono не загружается | ❌ **Уже исправлено** — system stacks в `app.css:31-34` |
| UX-конфликт | `w-full w-auto` | ❌ **Не найдено** — `w-auto` отсутствует в шаблонах |
| UX-дубли confirm | inline `confirm()` + модалка | ❌ **Уже исправлено** — 0 `confirm()` в шаблонах, всё через `data-confirm-form`/`data-confirm` |
| UX-чат | разный протокол (JSON vs raw) | ❌ **Уже исправлено** — оба шлют JSON, сервер имеет raw-fallback |

---

## 8. Приоритеты следующей волны

1. **H1** — включить мёртвую детекцию подозрительных команд (функциональность мониторинга).
2. **S1** — зарегистрировать глобальный rate limiter.
3. **B1** — порядок `onCommit` в `checkTimeoutsImpl` (целостность турнирных данных).
4. **S4/S5** — атомарная refresh-ротация + строгий fingerprint.
5. **S2** — роль из БД (пониженный админ теряет права немедленно).
6. **S3** — асинхронный debounced снапшот + убрать двойной `CalculateResults` (латентность геймплея).
7. **T1** — юнит-тесты на refresh/lockout/tournament/timeouts.
8. **UX1/UX2/UX3** — мёртвый JS, `alert()`, raw-WS.
9. **B2/B3/B8** — сериализация результатов, идемпотентность рейтингов, голосования.
10. **P4/UX5** — cache-заголовки и PWA-версионирование.
