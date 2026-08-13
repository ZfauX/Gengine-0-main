# DEEP_REVIEW — Gengine-0 (PASS 13)

> Глубокое ревью после закрытия PASS-12 (multi-instance).
> Метод: pprof-профилирование на реальном сервере (PPROF_ENABLED=true, :6060 loopback, Valkey через podman на :6380) + 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая проверка каждого HIGH/MEDIUM finding по коду.
> Архив предыдущего: `DEEP_REVIEW_2026-08-13_pass12.md`.

---

## 🔬 pprof-результаты (PASS 13)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 22 в покое → 21 после нагрузки (E2E 14/14 + 6 воркеров × 20 запросов) | ✅ Без утечек. Фоновые: CheckAutoStartGames, CheckTimeouts, roomWorker, realtimebus.run, themeCacheCleanup. |
| **heap inuse** | 18.5 MB | ⚠️ 51% (9.5 MB) — `golang.org/x/net/webdav.(*memFile).Write` = **swagger-файлы** (не код). Остальное — одноразовые 512KB-буферы (bufio, pgx, gorm schema, template parse). |
| **heap alloc** | инициализационные | ⚠️ SetupRouter/setupEngine — 32% аллокаций при старте (парсинг 60+ шаблонов, regexp, gorm-схемы). После старта — стабильно. |
| **cpu (лёгкая нагрузка)** | 0.27% (80ms samples) | ✅ Простаивает. |
| **cpu (6 воркеров × 20 req)** | 2.29s samples (7.6%) | 🔴 **55% cgocall (сеть)** — round-trips (rate-limit в Valkey на каждый запрос + gzip), **37% text/template** (walk 37%, evalCall 13.5% — вызовы `T()`/`TF()` через reflect). |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback, отдельный сервер, admin+2FA для /swagger. |

**Вывод**: утечек нет, предыдущие оптимизации работают (рендер пулом, дедуп `T()`, батч-запросы). Остаточные статьи — **swagger-память (51% heap)** и **template-рендер + сетевые round-trips (CPU)**.

---

## 🔴 HIGH (reviewer)

### H1. `onGameFinished` — неотслеживаемая goroutine с `WithoutCancel` работает после закрытия БД (потеря данных при shutdown) 🔍✅ (подтверждено)
- **Файл**: `cmd/server/main.go:339-358`.
- **Проблема**: колбэк завершения игры запускает `go func()` (строка 342), которая **НЕ добавлена в `bgWg`** (создаётся на строке 382). При этом `bgCtx = context.WithTimeout(context.WithoutCancel(reqCtx), 30s)` — отмена shutdown-контекста **не прерывает** эту работу. Порядок shutdown: `cancel()` (662) → `bgWg.Wait()` (665) → `sqlDB.Close()` (679). Игра, завершившаяся за ~30с до остановки, может выполнять `CalculateResults`/`UpdateScoresForGame`/`UpdateRatingsForGame` **после закрытия пула** → «sql: database is closed» → игроки НЕ получают турнирные очки/рейтинг.
- **Фикс (предложение)**: добавить горутину в `bgWg` (`bgWg.Add(1)` перед `go` + `defer bgWg.Done()`); либо сделать `WithoutCancel` только для HTTP-запроса, но слушать shutdown через отдельный канал; либо подождать эти горутины до `sqlDB.Close()`.

---

## 🟠 MEDIUM (reviewer + security)

### M1. `SSEManager.Save`/sessionstore: `MaxAge < 0` удаляет cookie, но backend-запись живёт 24ч 🔍✅
- **Файл**: `internal/pkg/sessionstore/sessionstore.go:331-357`.
- **Проблема**: TTL backend вычисляется только при `MaxAge > 0`; при `MaxAge < 0` (семантика gorilla «удалить куку») cookie получает `MaxAge=-1`, но данные сессии **сохраняются в backend на полный `sessionTTL` (24ч)**. Конфиденциальность + накопление мусорных записей (в т.ч. в Valkey).
- **Фикс**: при `MaxAge < 0` — `backend.Delete(sess.ID)` вместо `Set` (или TTL=1с).

### M2. `RenewToken` удаляет старую сессию ДО записи новой — потеря данных при сбое backend 🔍✅
- **Файл**: `internal/pkg/sessionstore/sessionstore.go:370-388`.
- **Проблема**: `backend.Delete(oldID)` выполняется до того, как `RenewGinSession` вызовет `Save` с новым ID. Если Save упадёт (backend недоступен) — данные сессии потеряны, откат невозможен (старый ID удалён).
- **Фикс**: писать новую сессию до удаления старой, либо delete только после успешного Save (возвращать ошибку из RenewGinSession до вызова Save).

### M3. `Logout` не удаляет server-side сессию (`DeleteGinSession` реализован, но не вызывается) 🔍✅
- **Файл**: `internal/domain/user/auth_handler.go:381-396`; `internal/pkg/sessionstore/sessionstore.go:447`.
- **Проблема**: logout отзывает JWT (blacklist) и refresh-токен, но server-side сессия (pending_*/oauth_state/2fa_verified) остаётся в backend до TTL. `sessionstore.DeleteGinSession(c)` написан и не используется.
- **Фикс**: вызвать `DeleteGinSession(c)` в `Logout` (и при смене пароля/отключении 2FA).

### M4. SSE: соединение «висит» без чистого закрытия при превышении лимита после отправки заголовков 🔍✅
- **Файл**: `internal/domain/game/hnd_sse.go:421-438`.
- **Проблема**: заголовки `text/event-stream` отправлены, `c.Abort()`, затем `RegisterSession` возвращает nil (лимит/stop между CanAccept и регистрацией) → просто `return`. net/http закроет соединение, но клиент не получит чистого завершения (недочитанный chunked-ответ).
- **Фикс**: в ветке `session == nil` — явно закрыть соединение (например, `http.NewResponseController(w).SetWriteDeadline` + `Flush` + завершение) или отправлять `retry:`-кадр.

### M5. Мусорная кука → новая сессия в backend на каждый запрос (накопление) 🔍✅
- **Файл**: `internal/pkg/sessionstore/sessionstore.go:198-218` (Get).
- **Проблема**: невалидная подпись cookie возвращает новую сессию (`IsNew=true`, новый ID), но плохая кука не очищается. Если gin-contrib Save вызывается на каждый запрос — каждая попытка с мусорным cookie создаёт запись с TTL 24ч (до ~10k в memory, мусорные ключи в Valkey).
- **Фикс**: при невалидной подписи в Get — не создавать запись (откладывать до реального Set) или помечать `IsNew` и очищать cookie (`MaxAge=-1`).

### M6. Per-user rate limit отсутствует на загрузку аватара/фото 🔍✅
- **Файлы**: `internal/domain/user/profile_handler.go:233` (UploadAvatar), `internal/domain/game/hnd_photo.go:131` (UploadPhoto).
- **Проблема**: загрузка аватара (2MB) и фото игры (10MB) не имеет rate limit (в отличие от SubmitFile, чата, платежей). Менеджер игры может заливать фото в цикле → заполнение диска (галерея растёт неограниченно).
- **Фикс**: использовать готовый `newSharedLimiter` (паттерн `rate_limiter.go:256-261`) для `/uploads`-постов.

### M7. Глобальный rate-limiter — round-trip в Valkey на каждый запрос 🔍✅
- **Файл**: `internal/pkg/middleware/rate_limiter.go:172-221`; подключение `router.go:108`.
- **Проблема**: при Valkey каждый HTTP-запрос выполняет Lua-скрипт INCR (сетевая RTT) до хендлера — один из главных вкладчиков в 55% cgocall. Плюс `setRateLimitHeaders` аллоцирует 3 строки на запрос.
- **Фикс**: глобальный лимит держать in-memory (single-instance) или кэшировать результат на короткое окно (50-100мс); критичные лимитеры (login/register/2FA) оставить в Valkey fail-closed.

---

## 🟡 LOW

1. **`valkeyClient` и `realtimebus` не закрываются при shutdown** (`main.go:245-275`, `310-323`) — пул Redis-соединений и pubsub-горутины живут до выхода процесса. Добавить `Close()`/`realtimeBus.Close()` перед закрытием кэша.
2. **pprof-сервер не в graceful shutdown** (`main.go:549-564`) — `pprofSrv` не вызывает `Shutdown`.
3. **`og:image` строит URL из `Host`-заголовка** (`hnd_game.go:232`) — Host-header injection в соцсетевых превью; валидировать против `cfg.Server.BaseURL`.
4. **Debug-лог раскрывает существование email в ForgotPassword** (`auth_handler.go:589`) — oracle в логах при `LOG_LEVEL=debug`.
5. **CORS: авто-подстановка `http://` для origin без протокола** (`router.go:124-127`) — вводит в заблуждение при конфигурации.
6. **Полный путь файла бэкапа в audit-логе** (`admin/handler.go:865`) — логировать ID, а не путь.
7. **`Register` не валидирует формат email** (`auth_handler.go:464-483`) — `binding:"email"` не применяется; возможна регистрация с мусорной строкой.
8. **`initial`/`truncate` в templatefuncs конвертируют `[]rune`** (`templatefuncs/funcs.go:140-158`) — аллокации на каждой ячейке таблиц.
9. **`SetRateLimitHeaders` — 3 аллокации на запрос** — устанавливать только при `!Allowed`.
10. **`NewResponseController(s.w)` на каждое SSE-событие** (`hnd_sse.go:42-54`) — кэшировать controller.

---

## ⚡ Оптимизации (perf, проверены по pprof)

| # | Оптимизация | Файл | Ожидаемый эффект |
|---|---|---|---|
| P-1 | **Вынести swagger за build-tag** (`//go:build swagger`) или лениво инициализировать: 9.5MB (51% heap) под webdav memFile | `router.go:38,48-49,186-187`, `main.go:41` | −51% heap inuse |
| P-2 | **Заменить остальные `T $.Lang` в layout на предвычисленные `$`-переменные** (после PASS-10 precompute покрыл только 11 из ~25 вызовов) | `layout.html` | −evalCall (13.5% CPU) |
| P-3 | **HTML-кэш анонимных публичных GET** (`/games`, `/`) на 30с (render в буфер → кэш по URL+lang) — убирает весь template-рендер для неавторизованных | `hnd_game.go:169` и др. | −37% template CPU для анонимов |
| P-4 | **Кэш `GetUserGamesView`** (60с TTL, инвалидация при сохранении настройки): отдельный SELECT на каждый авторизованный `/games` | `svc_facade.go:16-28`, `profile_repository.go:117` | −1 round-trip/запрос |
| P-5 | **Кэш `IsUserManager`** per `manager:{gameID}:{userID}` (60с); для `viewerID==0` сразу false | `svc_coauthor.go:128-133`, `hnd_game.go:207` | −1-2 round-trips/страница игры |
| P-6 | **Типизированный `cacheGetJSON`** (дженерик/type-switch вместо reflect) | `game/service.go:200-232` | −reflect на каждый cache-hit |
| P-7 | **`listingVersion` через `GetBytes`+`strconv.ParseInt`** вместо `json.Unmarshal` в `any` + `fmt.Sscanf` | `game/service.go:236-255`, `cache/valkey.go:149-168` | −аллокации на построение ключа листинга |
| P-8 | **`trackPrefix`: инкрементальная сборка префиксов** вместо Split/Join | `cache/cache.go:201-218` | −аллокации на каждую Set в кэше |
| P-9 | **`SetRateLimitHeaders`/`Initials`/`Truncate`** — strconv/utf8.DecodeRune вместо `[]rune`/fmt | `rate_limiter.go:283-294`, `templatefuncs/funcs.go:140-158` | −аллокации на таблицах |

---

## 💡 Улучшения UX

1. **Смена email**: пароль уже требуется (PASS-10 H3); добавить уведомление на СТАРЫЙ email «ваш email был изменён».
2. **2FA**: добавить «remember this device for 30 дней» (отдельный trusted-device cookie с подписью) — снижает трение повторного ввода TOTP на личных устройствах.
3. **Тестовый режим**: кнопка «Смотреть ответ» / «Пройти уровень за автора» в тестовом прохождении (сейчас уровень можно только «пропустить» — нет способа увидеть вопрос глазами игрока).
4. **Загрузка файлов**: прогресс-бар и лимиты подписаны на форме; предпросмотр фото до сохранения.
5. **Dashboard**: секция «Активные прохождения» — добавить прогресс (уровень X/Y) и «продолжить» одним кликом.
6. **i18n**: добавить переключатель языка в футере/шапке для гостевой страницы (сейчас только в профиле).
7. **Пустые состояния**: «У вас пока нет игр — создать первую» с иллюстрацией и CTA на пустых списках (dashboard, games, team).
8. **Email-уведомления**: уведомлять о получении инвайта в команду/игру (сейчас только в UI-ленте).

---

## 🛡️ Что проверено и НЕ подтвердилось (честность отчёта)

- **IDOR на `/testing/:passing_id`** (security #1): ShowTestGame/SubmitTestCode проверяют `IsUserManager` (hnd_gameplay.go:498, 546), StartTesting — `HasPermission` (svc_play.go:458), SkipLevelTest — `HasPermissionTx` (svc_play.go:638). **Не уязвимость.** Замечание: сервис `SubmitTestCode` сам не проверяет права (полагается на хендлер) — добавить defense-in-depth.
- **CSV-импорт без лимитов** (security #2): есть `maxImportRecords=5000`, `maxImportPosition=10000` (export/service.go:292-327). **Не подтвердилось.**
- **N+1** в game/repository, svc_listing, svc_admin — батч-запросы (COUNT(*) OVER(), unnest, Preload+Select). Не найдено в проверенных путях.
- **Гонки в `RoomHub.dispatchToRoom`** (после фикса PASS-12) — корректен (upgrade RLock→Lock с перепроверкой).
- **Паника send-on-closed** в очередях комнат/SSE/notification — закрытие через `sync.Once`, каналы не закрываются. Не найдено.
- **WaitGroup.Add vs Wait** в SSEManager/monitor — защищены lock-порядком.
- **Файловые хендлы в export** — файлы не открываются (работа с буферами).
- **Dashboard** — батчевые запросы (DashboardTeams — единый JOIN). Не N+1.

---

## 📋 Статус

- 3 аудита: ✅ проведены; HIGH/MEDIUM findings — ✅ эмпирически проверены по коду.
- **HIGH: 1** (H1 onGameFinished/grant shutdown data loss).
- **MEDIUM: 7** (M1-M7).
- **LOW: 10**.
- **Оптимизации: 9** (P-1..P-9), все обоснованы pprof.
- **UX: 8 предложений**.
- Проверки текущего кода: build ✅, golangci-lint ✅ (0 issues), test-short ✅, E2E 14/14 ✅ (с реальным Valkey).
- Рекомендуемый порядок фикса: H1 → M1-M3 (корректность сессий/логаута) → M4-M6 (SSE/rate-limit) → P-1..P-3 (память и рендер) → остальное.
