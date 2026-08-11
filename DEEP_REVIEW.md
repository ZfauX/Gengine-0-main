# DEEP_REVIEW — Gengine-0

> Глубокое ревью проекта: ошибки, оптимизации, улучшения.
> Проведено: pprof-профилирование под нагрузкой + 3 параллельных аудита (@reviewer, @security, @perf).
> Критические подозрения проверены эмпирически (HTTP-запросами к работающему серверу).
> ✅ = исправлено после ревью (см. разделы CRITICAL и pprof).

---

## 🔬 Что было сделано

1. **pprof-профилирование** — сервер запущен с `PPROF_ENABLED=true`, под нагрузкой (30 регистраций + 80 просмотров страниц) сняты профили:
   - `goroutine` — 25 горутин в покое
   - `heap` — 7.8 MB
   - `cpu` (5 сек) — 60ms сэмплов
2. **3 параллельных аудита**: ревью кода (34 файла), аудит безопасности, ревью производительности (все репозитории + горячие пути).
3. **Эмпирическая проверка критических подозрений** — реальные HTTP-запросы к работающему серверу на `:8081`.

---

## 🐛 Найденные ошибки

### 🔴 CRITICAL

#### 1. Платёжный вебхук заблокирован CSRF → платежи ЮKassa никогда не подтверждаются ✅ ИСПРАВЛЕНО

- **Файл**: `internal/app/app.go:93-107` (skip-list CSRF) + `internal/domain/payment/routes.go:22`
- **Проблема**: `POST /payments/webhook` зарегистрирован на `htmlGroup`, который обёрнут CSRF-middleware. Skip-list (`/api`, `/static`, `/uploads`, `/ws`, webauthn) **не включает** `/payments/webhook`. ЮKassa шлёт server-to-server POST **без** `_csrf`-токена → `gorilla/csrf` возвращает 403 раньше, чем сработает `PaymentHandler.Webhook`.
- **Эмпирически подтверждено**: `POST /payments/webhook` → **403 "CSRF token mismatch"**.
- **Последствие**: статус `succeeded` никогда не записывается в БД — пользователь платит, но система не начисляет ничего.
- **Статус**: ✅ **Исправлено** — `/payments/webhook` добавлен в skip-list CSRF (вебхук уже защищён IP-allowlist + подтверждением статуса через API). Регрессионный тест `TestRouter_PaymentWebhook_NoCSRFToken` + эмпирическая проверка: теперь 500 «internal» (доходит до handler), а не 403.

#### 2. IDOR: список комнат любой игры без проверки прав ✅ ИСПРАВЛЕНО

- **Файл**: `internal/domain/monitor/routes.go:115` + `internal/domain/monitor/handler.go:978-992`
- **Проблема**: `GET /games/:id/chat/rooms` (`GameRooms`) не покрыт `gameManager` middleware и не проверяет права в handler (в отличие от `CreateRoom`, который проверяет `IsUserManager`).
- **Эмпирически подтверждено**: аутентифицированный пользователь получает **200** на `/games/1/chat/rooms` для чужой игры.
- **Последствие**: любой пользователь может перечислить комнаты любой игры (утечка структуры игры, имён комнат).
- **Статус**: ✅ **Исправлено** — `gameManager` добавлен к `GameRooms` и `CreateRoom`. Эмпирическая проверка: теперь **403** для не-менеджера.

#### 3. Потеря очков турнира для игры в 2+ турнирах

- **Файл**: `internal/domain/tournament/service.go:386-485`
- **Проблема**: `UpdateScoresForGame` читает прохождения с `tournament_scored=false` и после начисления очков одному турниру помечает их `tournament_scored=true`. Если игра зарегистрирована в турнирах A и B — второй турнир никогда не получит очки за эту игру.
- **Фикс**: хранить флаг начисления в разрезе `(game_id, tournament_id)`.

#### 4. Инвариант «игрок в одной команде» не атомарен

- **Файл**: `internal/domain/team/service.go:406-434`
- **Проблема**: `AcceptInvitation` проверяет `GetTeamsByUserID` **вне транзакции**, а вставка `team_members` защищена только `ON CONFLICT DO NOTHING` на `(team_id, user_id)` — не на «один пользователь в одной команде». Два конкурентных accept (или accept + `AddMember`) могут поставить игрока в две команды, нарушив A-5.
- **Фикс**: уникальный partial-индекс `team_members(user_id)` или `SELECT ... FOR UPDATE` на строку пользователя внутри транзакции.

---

### 🟠 HIGH

| # | Файл | Проблема | Фикс |
|---|------|----------|------|
| 5 | `internal/domain/game/svc_passing.go:100-119` | `Apply` проверяет `Count >= MaxTeamNumber` и вставляет без сериализации → конкурентные заявки могут превысить лимит команд | `SELECT ... FOR UPDATE` строки игры в транзакции |
| 6 | `internal/domain/payment/service.go:196-242` | Вебхук не проверяет сумму/валюту из API против локальной записи | Сравнить `remote.amount.value`/`currency` перед `StatusSucceeded` |
| 7 | `internal/domain/payment/service.go:162-191` | Платёж создан в ЮKassa, но не сохранён в БД при сбое → повторная попытка создаёт второй платёж | Сохранять локальную запись `pending` до вызова API, ключевать идемпотентность |
| 8 | `internal/db/migrate.go:65-119` | При запуске из другой CWD (systemd/Docker/cron) миграции **молча пропускаются** (`return nil`) — сервер стартует на немгрированной схеме | Резолвить пути относительно `os.Executable()`, возвращать ошибку |
| 9 | `internal/domain/game/hnd_gameplay.go:276-279` | Подсказка попадает в URL (`?hint=...`) → история браузера, Referer, логи доступа | Только flash-сообщение; подписанный одноразовый токен для JS-ветки |
| 10 | `internal/domain/game/hnd_geolocation.go:56-66` | `UpdateLocation` не проверяет статус прохождения (finished/disqualified могут слать GPS); `Accuracy` не валидируется | Проверять статус + диапазон accuracy |
| 11 | `internal/pkg/middleware/auth.go:178-181` | `OptionalAuth` сохраняет роль из JWT, если роль в БД пустая — пониженный админ остаётся админом на optional-auth роутах | Всегда перезаписывать роль |
| 12 | `internal/domain/tournament/service.go:498-511` | Кэш лидерборда никогда не хитится с Valkey (тип `[]TournamentResult` не переживает JSON round-trip) | Явная маршализация или кэш только для in-memory |
| 13 | `internal/pkg/websocket/room_hub.go:66-104,131-145` + `hnd_sse.go:332-348` | TOCTOU в лимитах соединений: `CanAccept` и `incConnection` — не под одним lock | Проверка+инкремент в `RegisterClient` под одним мьютексом |
| 14 | `internal/domain/game/svc_monitor.go:87-130` | Кэшированный `[]TeamProgress` возвращается по ссылке — мутация портит кэш | Возвращать копию |
| 15 | `internal/domain/game/svc_monitor.go:231-234` | Отрицательная общая длительность при рассинхроне таймстампов | Кламп к 0 |

---

### 🟡 MEDIUM

| # | Файл | Проблема |
|---|------|----------|
| 16 | `internal/pkg/cache/cache.go:166-173` | `Set(key, ttl=0)` после `ttl>0` оставляет ключ в `ttlKeys` навсегда |
| 17 | `internal/domain/game/svc_passing.go:77-122` | Нет проверки `visibility`/публикации; pending-заявки не считаются в лимит команд |
| 18 | `internal/pkg/websocket/room_hub.go:199-203` | Drop-on-full для медленных клиентов молча теряет сообщения без отключения |
| 19 | `internal/domain/user/service.go:186-208` | Backoff использует устаревший `LockCount` при параллельных неудачных попытках |
| 20 | `internal/db/migrate.go:84-96` | При ошибке `getCurrentVersion` возвращает 0 → выбирается squashed-набор для существующей БД |
| 21 | `internal/config/config.go:349-372` | `YKASSA_WEBHOOK_KEY` загружен, но не используется (нет проверки подписи) |
| 22 | `internal/config/config.go:599-616` | `getEnvAsInt`/`getEnvAsDuration` молча фолбэчат при ошибке парсинга даже в STRICT_CONFIG |
| 23 | `internal/domain/game/hnd_sse.go:151-174` | `RegisterSession` не проверяет `m.stopped` — конкурентный вызов после `Stop()` нарушает контракт WaitGroup |
| 24 | `internal/domain/monitor/handler.go` | ChatWS hot-path: до 7 последовательных DB-запросов на сообщение |
| 25 | `internal/domain/user/profile_handler.go:491-501` | `UpdateNotifyGameDays` без валидации диапазона |

---

### ⚪ LOW / NIT

| # | Файл | Проблема |
|---|------|----------|
| 26 | `internal/domain/user/service.go:163-218` | Для 2FA-пользователя `Login` минтит JWT, который выбрасывается (лишняя работа) |
| 27 | `internal/domain/user/password_reset_service.go:63` | `TokenHash` генерируется и хранится, но не используется |
| 28 | `internal/domain/game/svc_play.go:159-162` | Повторное чтение passing (уже залоченного) для team_id |
| 29 | `internal/domain/level/service.go:142` | `RequiresConfirmation` сбрасывается в false при частичном Update |
| 30 | `internal/domain/payment/handler.go:63` | Ошибка `ParseFloat` игнорируется |
| 31 | `internal/pkg/middleware/auth.go:60-89` | Роль-кэш локальный на процесс — до 5 сек устаревшая роль |
| 32 | `internal/config/config.go:638-657` | VAPID-ключи регенерируются при каждом рестарте, если не заданы |

---

## 📊 Оптимизации (по итогам pprof + perf-аудита)

### Найденное pprof-профилированием

- **goroutine-профиль**: при недоступном Valkey остаются **2 висящие `ConnPool.tryDial` + 2 `CircuitBreaker.cleanupLoop`** goroutine — go-redis бесконечно ретраит подключение к мёртвому Redis.
- **CPU-профиль (5 сек)**: **33% времени — `tryDial`** (попытки переподключения к недоступному Valkey) + 67% `runtime.cgocall` (Windows-платформа).
- **Вывод**: `NewValkeyClient` возвращает nil после неудачного Ping, но внутренние goroutine go-redis уже запущены и живут вечно. Нужно закрывать клиент (`client.Close()`) при недоступности или не создавать его до первого успешного Ping.
- **Статус**: ✅ **Исправлено** — `NewValkeyClient` теперь вызывает `client.Close()` при неудачном Ping. Повторный pprof: **0 висящих redis-goroutine**, CPU-профиль в покое — **0 сэмплов**. Тест `TestNewValkeyClient_UnavailableClosesClient`.

### Высокий эффект (уже в коде — для будущей работы)

| # | Путь | Проблема | Эффект |
|---|------|----------|--------|
| P1 | `svc_listing.go:75-87` | Аутентифицированный список игр **не кэшируется** — каждый запрос бьёт в PostgreSQL (только анонимный page=1 кэшируется) | Высокий (главная нагрузка) |
| P2 | `monitor/handler.go:738-751` | Отправка чат-сообщения = **~7 последовательных DB-запросов** (room + team member + can_write + INSERT + GET by id + preload) | Высокий (горячий real-time путь) |
| P3 | `monitor/repository.go:225-239` | `Preload("User")` в чате тянет **полные строки users** (включая password_hash/email) | Средне-высокий (payload + утечка) |
| P4 | `svc_coauthor.go:92-97` | `IsUserManager` = 2 запроса (доп. `GetUserRole`) на **каждое** WS/SSE-подключение | Средне-высокий |
| P5 | `svc_play.go:753` | `game_settings` перечитывается на каждую страницу gameplay и каждую подсказку | Средне-высокий |
| P6 | `gzip.go:67` | gzip пропускает `/static/` — статика отдаётся **без сжатия** | Высокий (bandwidth/first-load) |

### Средний эффект

| # | Путь | Проблема |
|---|------|----------|
| P7 | `monitor/handler.go:877-916` | `ChatRoomIDs` = до 5 последовательных запросов |
| P8 | `user/profile_repository.go:23-43` | Публичный профиль = ~7 некэшированных запросов |
| P9 | `svc_listing.go:242-257` | Autocomplete бьёт в БД на каждое нажатие клавиши |
| P10 | `game/repository.go:186-201` | Календарь (`ListByDateRange`) не кэшируется |
| P11 | `monitor/handler.go:469-481` | MonitorWS заново маршалит snapshot, хотя есть `GetOrFetchSnapshotJSON` |
| P12 | `hnd_sse.go:255-288` | `Broadcast`/`UnregisterSession` под глобальным Lock с O(n) splice |
| P13 | `room_hub.go:242-293` | `dispatchToRoom` аллоцирует свежий slice и берёт Lock до 3 раз на broadcast |
| P14 | `game/repository.go:115-132` | `GetByIDPreloaded` тащит `description` + `search_vector` на каждую страницу игры |

### Быстрые победы (топ-5)

1. **Кэшировать аутентифицированный список игр** (30с TTL, инвалидация через `games:list:version`) — убирает главный некэшированный запрос.
2. **Схлопнуть отправку чата с ~7 до 1-2 запросов** — один SQL с EXISTS/INSERT RETURNING.
3. **Ограничить `Preload("User")` в чате** до `id, name, avatar_path` — меньше payload, нет хэшей паролей на проводе.
4. **Кэшировать роль пользователя + проверку менеджера** (15-60с TTL) — убирает 2 запроса на каждое WS/SSE-подключение.
5. **Сжимать статику** (прекомпрессия .gz + `Cache-Control: immutable` для `?v=`) — самый дешёвый выигрыш по bandwidth.

---

## 🚀 Улучшение проекта

### Кодовая база

#### Безопасность (приоритет 1)
1. **Починить вебхук** (CRITICAL #1) — добавить `/payments/webhook` в skip-list CSRF + тест.
2. **Починить GameRooms IDOR** (CRITICAL #2) — добавить `gameManager`.
3. Добавить проверку суммы/валюты в вебхук (#6).
4. Использовать `YKASSA_WEBHOOK_KEY` для проверки подписи (#21) — подготовка к signed-уведомлениям.
5. Закрывать redis-клиент при недоступном Valkey (pprof-находка) — убрать висящие goroutine.

#### Надёжность (приоритет 2)
6. Атомарный инвариант «одна команда на игрока» (CRITICAL #4) — unique partial index.
7. Мультитурнирный скоринг (CRITICAL #3).
8. `Apply` с row-lock (HIGH #5).
9. Миграции: резолв CWD от бинарника + ошибка вместо `return nil` (HIGH #8).
10. `RegisterSession` с проверкой `stopped` (MEDIUM #23).

#### Производительность (приоритет 3)
11. Кэш аутентифицированного списка игр (P1).
12. Оптимизация чат-пути (P2 + P3).
13. Кэш роли/менеджера (P4), game_settings (P5).
14. Сжатие статики (P6).

#### Технический долг
15. Убрать неиспользуемый `TokenHash` (#27), использовать `GetOrFetchSnapshotJSON` (P11).
16. Отдельный `WithCtx`-тест для Valkey-кэша лидерборда (#12).
17. Единый middleware-помощник для WS-хендлеров (повторяющийся boilerplate MonitorWS/LogsWS).

### Пользовательский опыт (UX)

| Идея | Эффект |
|------|--------|
| **Онлайн-индикатор в чате** — кто из участников комнаты сейчас в сети (по WS-подключениям) | Живость, доверие к real-time |
| **Уведомление о подтверждении платежа** — после исправления вебхука: push/SSE «платёж прошёл» | Закрывает петлю ожидания после оплаты |
| **Прогресс-бар прохождения** в мониторинге — визуализация уровня/времени команды | Лучше читается snapshot |
| **Тёмная карта в мониторинге** (Leaflet dark tiles) — согласовано с тёмной темой приложения | Эстетика |
| **Мобильная PWA-установка** — кнопка «Установить приложение» (beforeinstallprompt) | Удержание, оффлайн-подход |
| **Фильтры в списке игр** — по дате/статусу/моим играм (данные уже есть в запросе) | Быстрее навигация |
| **Пагинация или виртуализация чата** — при >500 сообщений | Плавность на длинных играх |
| **Онбординг-подсказки** для новых авторов (первая игра → как добавить уровень) | Снижает порог входа |
| **A11y**: контраст, focus-стили, aria-метки в мониторинге/карте | Доступность |
| **Reconnect-статус в заголовке** — «потеряно соединение, переподключаюсь…» на всех WS-страницах | Прозрачность |

---

## ✅ Что в проекте хорошо (по итогам аудитов)

- **Refresh-токены** — SHA-256 в БД, семейства с отзывом, fingerprint-привязка, атомарный `ClaimAndCreate` — образцово.
- **Транзакционная дисциплина** — `onCommitFn` откладывает сайд-эффекты (broadcast, SSE) после коммита.
- **Postgres-сериализация** — advisory locks для `CalculateResults`, `Move`/`Duplicate` уровней, турнирного скоринга.
- **Безопасность в глубину** — dummy bcrypt против enumeration, constant-time OAuth state, IP-allowlist + API-подтверждение вебхука, origin-check на SSE/WS, `ExistsByPosition` вместо N+1.
- **Кэш** — композитный интерфейс, singleflight, `ttlKeys` sweep, документированный тип-Valkey caveat.
- **Производительность уже есть**: window-function пагинация, денормализация `logs.game_id`, version-key инвалидация, snapshot JSON-кэш, 10+ миграций с индексами.
- **Права доступа**: геолокация и Phase-3 маршруты под `GameManager`, чат через `canJoinRoom` (проверено юнит-тестами).

---

## 🎯 Итоговые рекомендации (что чинить первым)

1. ✅ **`/payments/webhook` → в skip-list CSRF** — ИСПРАВЛЕНО (было: платежи не подтверждались).
2. ✅ **`GameRooms` → под `gameManager`** — ИСПРАВЛЕНО (было: IDOR на комнаты любой игры).
3. ✅ **Закрывать redis-клиент при недоступном Valkey** — ИСПРАВЛЕНО (было: вечные goroutine tryDial, 33% CPU).
4. **Атомарность команды/турнира/лимита команд** — 3 data-integrity race'а (CRITICAL #3, #4, HIGH #5).
5. **Кэширование горячих путей** (список игр, чат, роль, статика) — 5-10× снижение DB-запросов.

---

*Дата: 2026-08-11. Метод: pprof (goroutine/heap/cpu) + @reviewer + @security + @perf + эмпирические проверки.*
