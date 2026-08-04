# Deep Review Gengine-0 — 4 августа 2026

Повторный глубокий аудит проекта после волны исправлений pass 1–19 и стратегического рефакторинга (game-домен, WebSocket-клиент, серверная персонализация, очистка секретов из git-истории).

**Методология:** 4 ревью-агента параллельно (безопасность, производительность, код/корректность, фронтенд/UX) + выборочная верификация ключевых находок по коду.

**Легенда:** 🔴 критично · 🟠 высоко · 🟡 средне · 🟢 хорошо · ✅ уже исправлено в этой/прошлой сессии

---

## 1. 🔴 Критические проблемы

### 1.1 Корректность

**B1. Турнирные очки не начисляются при обычном завершении игры** ✅ не подтверждено исправлением
- `cmd/server/main.go:273-282,303` — колбэк `onGameFinished` (вызывает `Tournament.UpdateScoresForGame`) передаётся **только** в `game.CheckTimeouts` (таймаут-финиш).
- Обычный путь завершения: `SubmitCode` → `CompleteLevel(tx, progress, nil)` — колбэк **nil** (`svc_play.go:104,449`), `AcceptBlackboxAnswer` тоже `nil` (`svc_play.go:322`), `ForceFinishGame` (`svc_admin.go:46`) колбэк не вызывает вовсе.
- **Результат:** команды не получают очков турнира, если игра завершилась штатно или принудительно.
- **Дополнительно:** `UpdateScoresForGame` (`tournament/service.go:300`) **не идемпотентен** — при повторном вызове начисляет очки повторно по всем `finished`-прохождениям.

**B2. Учётная запись доступна для блокировки (lockout-DoS + oracle существования email)**
- `user/service.go:111-114` — при существующем и заблокированном аккаунте возвращается «аккаунт заблокирован до …», а для несуществующего email — общее сообщение. Различие раскрывает факт регистрации email.
- 5 неверных паролей блокируют аккаунт; сброс только после успешного входа → любой может держать известный аккаунт заблокированным.
- **Фикс:** единый generic-ответ; блокировка по email+IP; лимит попыток с одной IP без учёта существования пользователя.

### 1.2 Безопасность

**S1. JWT без проверки `iss`/`aud`**
- `user/service.go:233-243` — `ParseToken` проверяет только подпись (HS256) и expiry; `generateJWT` (`:342-362`) не задаёт issuer/audience.
- Любой токен, подписанный тем же секретом (другой сервис, dev-окружение, токен для другого audience), будет принят.
- **Фикс:** `WithIssuer`/`WithAudience` при выпуске + проверка в `ParseToken`.

**S2. Роль берётся из JWT-claims и не перепроверяется по БД**
- `user/service.go:297-300` доверяет `claims["role"]`; `middleware/auth.go:72-88` (AdminRequired) полагается на claim. Пониженный/заблокированный пользователь сохраняет admin-права до истечения токена (15 мин).
- `ChangePassword` **не отзывает** текущие JWT/refresh (только password-reset это делает).
- **Фикс:** перечитывать роль из БД (или короткий TTL + revoke при change-password).

**S3. Anti-enumeration dummy-bcrypt с неверным cost**
- `user/service.go:105` — dummy-хэш `$2a$10$...` (cost 10), а реальные пароли — cost 12 (`pkg/crypto/constants.go:7`). Тайминг-атака различает «пользователь не найден» и «пароль неверный».
- **Фикс:** генерировать dummy-хэш с тем же `BcryptCost` (12).

**S4. Refresh-token reuse не детектится; fingerprint-binding обходится**
- `user/service.go:192-231` — ротация есть, но повторный use старого токена даёт generic-ошибку без отзыва семьи/устройства.
- `service.go:205` — при `clientFingerprint == ""` проверка привязки пропускается (`stored.ClientFingerprint != "" && clientFingerprint != "" && …`).
- **Фикс:** вести семьи токенов (token family), отзывать всю семью при reuse.

**S5. Критические секреты в рабочем дереве / истории** ✅ частично исправлено
- `.env` с живыми `JWT_SECRET`, `SESSION_SECRET`, `VAPID_PRIVATE_KEY`, `ADMIN_PASSWORD` лежит в рабочем дереве (нужен для dev, в `.gitignore`).
- История git **очищена** от `.env` (git filter-repo в этой сессии), секреты **ротированы** пользователем. ✅
- **Осталось:** репозиторий публичный; PR-рефы `refs/pull/1-3/head` (закрытые PR #1–3) всё ещё ссылаются на старые коммиты с секретами — удалить PR через веб-интерфейс GitHub. Проверить, не использовались ли старые секреты в проде.

---

## 2. 🟠 Высокие проблемы

### Корректность

**B3. Незамеченные ошибки БД маскируются под пользовательские**
- `monitor/service.go:128-131` — `tx.Where(...).Find(&attempts)` без проверки `.Error`; при ошибке БД избиратель получает «недопустимый вариант ответа» вместо 500.
- `svc_attempt.go:111-121` — `AcceptPendingAttemptWithTx` возвращает «нет ожидающей попытки» и при реальной ошибке БД (не `errors.Is(gorm.ErrRecordNotFound)`).
- `svc_admin.go:120-122` — `DisqualifyTeam` заменяет любую ошибку на «команда не в игре…».
- `tournament/service.go:96-97` — ошибка `Count` при вычислении `OrderIndex` игнорируется → коллизии `OrderIndex: 0`.

**B4. `SaveSettings` (game) — гонка update-then-insert**
- `game/service.go:520-555` — сначала `Updates`, при `RowsAffected == 0` → `Create`. Два параллельных первых сохранения оба увидят 0 строк и оба `Create` → нарушение уникальности.
- **Фикс:** один upsert через `clause.OnConflict`.

**B5. `ResetPassword` не атомарен**
- `user/service.go:719-742` — пароль записывается, затем `MarkTokenUsed`, затем `DeleteToken`. Сбой между шагами оставляет валидный reset-токен после смены пароля.
- **Фикс:** обернуть в транзакцию.

**B6. `CheckTimeouts` не concurrency-safe между инстансами**
- `svc_progress.go:333-365` — batch `UPDATE ... WHERE finished_at IS NULL` не проверяет `RowsAffected`; второй инстанс выберет те же timed-out IDs и создаст дублирующие next-level прогрессы, возможно дважды завершит игру.
- **Фикс:** проверять `RowsAffected` после batch-update, пропускать уже обработанные.

**B7. `Move` уровней — гонка на temp-слот и чтение вне транзакции**
- `level/service.go:226-276` — `tempPos := maxPos + 1` внутри каждой транзакции; два параллельных Move могут выбрать один `tempPos` → нарушение уникальности. `FindPrevLevel`/`FindNextLevel` выполняются вне транзакции — позиции соседей могут измениться до взятия блокировок.

**B8. `Duplicate` уровней — транзиентный конфликт `position+1`**
- `level/service.go:148-152` — `Update("position", gorm.Expr("position + 1"))` при уникальном индексе `(game_id, position)` и смежных позициях даёт конфликт в момент одного UPDATE (PostgreSQL вычисляет по строкам).

**B9. `StartTesting` путает пер-юзер и пер-игру**
- `svc_play.go:379-390` — проверка существующего `StatusTesting` по `game_id` и шаблону `_test_%`, без `userID`. Второй модератор/автор не может запустить свой тест для той же игры.

**B10. `GetPassingByUser` — недетерминированный JOIN**
- `game/service.go:445-456` — `JOIN team_members` + `First` без ORDER BY: если passing матчит и `accepted`, и `started`, выбирается случайная строка.

### Безопасность/реалтайм

**S6. WebSocket/SSE в layout — без reconnecting-клиента, три независимых realtime-стека**
- `layout.html:597-618` — «сырой» non-reconnecting WebSocket к `/ws/notifications`; страницы используют `createReconnectingWebSocket`; gameplay — свой SSE. Разные механизмы reconnect/статусов.
- **Фикс:** единый клиент (см. оптимизации).

**S7. `Secure`-флаг cookie зависит от окружения**
- `user/handler.go:55-77` и `app/router.go:55` — Secure ставится по `TLS`, `X-Forwarded-Proto` или `FORCE_SECURE_COOKIE`. За TLS-терминирующим прокси без проброса заголовка cookie уйдут без Secure.
- **Фикс:** по умолчанию `FORCE_SECURE_COOKIE=true` или производный от `TrustedProxies`.

---

## 3. 🟡 Средние проблемы

### Производительность

**P1. SubmitCode: полный snapshot + broadcast на КАЖДУЮ попытку (даже неверную)**
- `svc_play.go:132-136,506-543` — `broadcastSnapshot` после каждого submit: инвалидация кэша мониторинга (30 с), пересчёт всего CTE `GameSnapshot` (`svc_monitor.go:179-216`), включая `analyzeTeamsBehavior` (3-way aggregate по всей игре), `json.Marshal` всего снапшота и broadcast всем WS-клиентам. Синхронно внутри HTTP-запроса.
- **Фикс:** broadcast только при изменении видимых полей (верный ответ), debounce ~500 мс, асинхронно.

**P2. `CalculateResults` после каждого верного уровня**
- `svc_play.go:137-143` — `monitorSvc.CalculateResults(ctx, gameID)` пересчитывает рейтинг и делает 2 больших `UPDATE ... CASE ... WHERE id IN` по всем finished-прохождениям на каждый завершённый уровень.

**P3. Листинг игр: агрегация по всем видимым играм + без кэша**
- `svc_listing.go:36-63,106-138` — подзапросы `ratings`/`participants` агрегируют по всем видимым играм, а не по странице; `COUNT(*) OVER()` сканирует всё; сортировка по `rating_value` — полная сортировка. Кэша нет.
- **Фикс:** прекомпьютить рейтинг/участников (колонки на `games`), агрегировать только по ключам страницы, кэшировать анонимный листинг (по ключу страницы, TTL ~30 с).

**P4. Устаревание кэша `game:%d` при `UpdateGameWithCover` и `SaveSettings`**
- `svc_cover.go:78-123` — `UpdateGameWithCover` не инвалидирует `game:%d` (хендлер `Update` вызывает напрямую, минуя `GameService.Update`).
- `service.go:520-556` — `SaveSettings` обновляет БД, но не удаляет `game:%d` (кэш из `GetByIDPreloaded` включает `GameSetting`) → устаревшие `allow_hints`, `max_hints` до TTL. ✅ cache-инвалидация добавлена для Delete/Update/AdminDelete, но для этих двух путей — нет.

**P5. Настройки темы — запрос к БД на каждый авторизованный HTML-запрос**
- `middleware/theme.go:29-46` + `app/router.go:59-65` — `GetUserThemeSettings` на каждый запрос, без кэша. ✅ сделано исключение для `/api`,`/ws`,`/static`,`/uploads`, но HTML-страницы всё ещё делают запрос.

**P6. `GetStats` обходит кэш рейтинга**
- `svc_crud.go:198-219` — `ShowGame → GetStats` вызывает `ratingService.GetAverageRating` напрямую, минуя кэширующий `GameService.GetAverageRating`. Отзывы с `Preload("User")` не кэшируются вовсе.

**P7. Мониторинг: повторный marshal каждый тик**
- `monitor/handler.go:107-156` — poller каждую секунду делает `GetOrFetchSnapshot` + `json.Marshal`, отдавая те же байты всем подписчикам.

**P8. `CheckTimeouts` — глобальный скан каждые 30 с**
- `svc_progress.go:268-369` — скан всех `finished_at IS NULL` level_progresses; лимит 50 ограничивает работу, но не скан.

**P9. Автокомплит без rate limit и кэша**
- `hnd_autocomplete.go:60-64`, `routes.go:163-164` — `/api/search/games` по каждому нажатию, без лимита и кэша (см. также S-секцию: публичный эндпоинт).

**P10. `GetLogsByGameID` без LIMIT**
- `service.go:459-467` — страница логов грузит все логи игры без пагинации (пагинированная версия есть, но не используется).

**P11. Потенциально неограниченный in-memory LRU**
- `cache.go:41-84` — `NewCache` при `maxSize=0` использует `math.MaxInt` → неограниченный кэш, риск роста памяти. Проверить `cmd/server/main.go` — задаётся ли `maxSize`.

**P12. Unbounded кэш монитора** ✅ исправлено
- LRU-эвикция в `svc_monitor.go` добавлена (удаление старого элемента из cacheList перед PushBack).

### Код/архитектура

**C1. Сервисы держат сырой `*gorm.DB` и обходят репозитории**
- `GameService` (service.go:447,461,472,501,524), `GamePassingService`, `LevelProgressService`, `AttemptService`, `TournamentService`, `UserDashboardService`; `MonitorService` использует `gameRepo.DB(ctx)`. Репозитории сами экспонируют `Model(ctx)`/`DB(ctx)` (`game/repository.go:21-25,98-100`).
- **Фикс:** постепенно закрыть доступ к `DB(ctx)` из сервисов, перевести на репозиторные методы.

**C2. Дублирование проверок прав и cleanup-логики**
- `GameService.Delete` (service.go:243-283) и `AdminDelete` (287-323) — почти идентичные блоки очистки файлов/кэша (surface для расхождений).
- Проверка владельца дублируется: `svc_crud.go:106` + `service.go:248`.

**C3. Строковые ошибки vs sentinel**
- Много `errors.New("русский текст")`, сравниваемых/локализуемых через `render.LocalizeError(c, err.Error())` (`hnd_gameplay.go:224,360`, `hnd_passing.go:233,259,289`) — хрупко. ✅ введены `ErrGameNotFound`, `ErrNoActiveLevel`, `ErrHintLimitReached` — но покритие неполное.

**C4. Бизнес-логика/транзакции в хендлерах и роутерах**
- `app/router.go:265-269` строит `NewGamePassingService(...)` при конфигурации роутов; `game/routes.go:56` строит `NewSimulateService` внутри `RegisterRoutes`.

**C5. Мёртвый код / рассинхронизированные интерфейсы**
- `interfaces.go:86` — `InitFirstLevelWithTx(ctx, tx LevelIniter, ...)` несовместим с реализацией `svc_progress.go:55` (принимает `*gorm.DB`) — интерфейс не удовлетворяется.
- `LevelIniter` (`interfaces.go:79-81`) не используется.
- `AchievementService.SeedAchievements` (`user/service.go:436-450`) без вызова.
- `GamePassingService.ListByGame` deprecated — проверить вызовы.
- Room-key формат расходится: `StartGame` использует `strconv.Itoa(int(gameID))`, монитор — `gameID` (иначе комнаты разойдутся).

**C6. Mojibake в комментариях и i18n**
- `game/routes.go:19-20` — битые комментарии.
- `en.go:949` — `"вЏ±пёЏ Remaining: %s"`, `en.go:1610-1612` — `"рџҐ‡ 1st"/"рџҐ€ 2nd"/"рџҐ‰ 3rd"`, `en.go:1617-1618` — `"вњ“ Saved"/"вњЋ Not saved"` — испорченная UTF-8 в EN-интерфейсе.

**C7. Мелочи**
- `GetAverageRating` (`service.go:396-398`) nil-чекает `s.reviewService`, а dereference'ит `s.ratingService` (латентный panic).
- `map[bool]string{true: "принят", false: "неверный"}` (`svc_play.go:122`) — излишняя хитрость.
- `monitor/service.go:238-243` — недетерминированный тай-брейк победителя голосования (порядок map).
- `user/auth_handler.go` (814 строк), `game/hnd_gameplay.go` (582 строки) — крупные файлы.

### UX / Фронтенд

**UX1. Двойное подтверждение: native `confirm()` + кастомная модалка** ✅ частично исправлено
- Шаблоны с `data-confirm-form` (модалка app.js) + inline `confirm()` одновременно: `games-show.html:89,106,416-421`, `games-list.html:252-263`, `admin-backups.html:26,67`, `questions-list.html:19,40`, `questions-show.html:23,43`, `levels-show.html:73,105`, `teams-members.html:22,51`, `games-photos.html:187-188`.
- В прошлой сессии убран конфликт для `initFormLoading` и `data-confirm-form`, но inline-обработчики confirm() в шаблонах остались.

**UX2. Кнопка «OK» в модалке всегда «Удалить»**
- `showModalConfirm` использует глобальный `data-i18n-confirm-ok` = «Удалить»/«Delete». Для publish/force-finish модалка показывает кнопку «Удалить». Нет per-action label.

**UX3. Зависающие submit-кнопки**
- `app.js:98-114` `initFormLoading` блокирует кнопку, но формы, перехватывающие submit через `preventDefault()` + fetch, кнопку не восстанавливают: `notification-settings.html:157-194`, `gameplay-show.html:352-396`.

**UX4. FOUC на `/games`**
- `games-list.html:53-62,111` — skeleton и таблица видимы по умолчанию до JS (карточки скрыты). До выполнения JS видно и skeleton, и таблицу.

**UX5. Flash-сообщение и тост дублируются**
- `gameplay-show.html:423-430` — `.flash-error`/`.flash-info` превращаются в тосты без удаления inline-flash.

**UX6. Wizard: Enter пропускает шаги**
- `games-new-wizard.html:172-181` — перехват только кликов по кнопке; Enter в поле name отправляет форму с 1-го шага.

**UX7. SSE game-уведомления мертвы**
- `app.js:1090-1098` ищет `[data-sse-game-id]`, которого нет ни в одном шаблоне → `initSSEGameNotifications` и `/game/sse/:game_id` не работают (после удаления `data-sse-game-id` с геймплея в этой сессии).

**UX8. Тосты под модалкой**
- `layout.html:285` — toast container `z-50`, модалка `z-[10000]` → тосты за модалкой.

**UX9. A11y: два skip-link; нет restore фокуса; нет aria-live на таймерах/чатах; drop-zone не с клавиатуры**
- `layout.html:136` и `:166` — два skip-link (первый с inline `left:-9999px`, ломающим `focus:not-sr-only`).
- `showModalConfirm` не возвращает фокус на триггер; модалки `games-show` без focus-trap.
- Таймер геймплея и чаты без `aria-live`; drop-zone обложки без `tabindex`/`role`.
- Стрелки пагинации без aria-label; `prefers-reduced-motion` не обрабатывается; theme-toggle без `aria-pressed`.

**UX10. PWA: immutable-кэш без content-hash → устаревший деплой**
- `static_cache.go:13` — `Cache-Control: immutable` для всех `/static/*`, но `app.js`/`output.css` отдаются по неизменяемым URL. Старый JS/CSS задерживается на год без бампа версии SW (`gengine-v16` в `sw.js:1`).

**UX11. Консистентность**
- Смешение кастомной модалки, native `confirm()` и `alert()` в одном классе операций.
- `ws-client.js` загружается на каждой странице (`layout.html:160`), но не используется (мёртвый код, ~2 КБ/страницу).
- Протокол отправки чата разный: `team-chat.html` шлёт JSON, `chat-page.html` — сырой текст.
- Character counter в wizard: «X символов» → «N/2000 characters» после первого ввода.

**UX12. Дизайн**
- `games-show.html:17` — конфликтующие классы `w-full w-auto min-w-full`.
- `app.css:34` — `--font-mono: 'JetBrains Mono'` не загружается.
- Dark mode частичный: несколько бейджей без `dark:` вариантов.
- Полу-звезда рейтинга — иконка 🌤️ (солнце за облаком).

---

## 4. ⚡ Варианты оптимизации (по приоритету)

### Быстрые и безопасные
1. **P4** — добавить инвалидацию `game:%d` в `UpdateGameWithCover` и `SaveSettings` (2 строки).
2. **B3** — проверять `.Error` у `Find`/`Count` в monitor/attempt/admin/tournament.
3. **C7** — исправить nil-чеки `GetAverageRating`, убрать mojibake в `en.go:949,1610-1618` и `game/routes.go`.
4. **UX2** — добавить per-action OK-label в `showModalConfirm` (параметр, дефолт «Удалить»).
5. **UX3** — восстанавливать submit-кнопки в AJAX-формах.
6. **UX7** — убрать мёртвый SSE-модуль или завести `data-sse-game-id` там, где нужен.
7. **UX4** — спрятать skeleton и таблицу по умолчанию в разметке.

### Средние по трудоёмкости
8. **S1/S2** — JWT iss/aud + перечитывание роли из БД.
9. **S3** — dummy-bcrypt cost 12.
10. **B1** — пробросить `onGameFinished` в `SubmitCode`/`AcceptBlackboxAnswer`/`ForceFinishGame`; сделать `UpdateScoresForGame` идемпотентным (ключ «турнир+игра+команда уже начислено»).
11. **P1/P2** — broadcast/CalculateResults только при верном ответе, асинхронно, с debounce.
12. **P3** — прекомпьютинг рейтинга/участников + кэш листинга.
13. **UX1** — удалить inline `confirm()`-обработчики из 8 шаблонов (модалка app.js уже обрабатывает `data-confirm-form`).
14. **P5** — кэшировать настройки темы (короткий TTL) в middleware.

### Крупные архитектурные
15. **C1** — закрыть `DB(ctx)` в сервисах, перевести на репозитории.
16. **C2** — единый `GameDeleter` (общий cleanup файлов+кэша для Delete/AdminDelete).
17. **C3** — sentinel-ошибки для бизнес-правил + машинно-читаемые коды ошибок в API.
18. **S4** — token families для refresh-токенов.
19. **P3 (продолжение)** — `rating` и `participants` как колонки на `games` с триггерной пересчётом.
20. **P11** — ограничить in-memory LRU (`maxSize`) и добавить jitter/stale-while-revalidate к TTL.

---

## 5. 🚀 Предложения по улучшению

### Кодовая база
- **Единый механизм прав** (`CanManageGame`): один интерфейс вместо 4+ дублированных проверок владелец/соавтор/капитан.
- **Error-code-контракт API**: серверные ошибки как `{error, code}` (hint_limit, position_taken, …), клиент не парсит локализованный текст.
- **Автоматический CI-гейт**: `make i18n-check` (✅ добавлен), `golangci-lint`, `go test -short`, проверка миграций на idempotent-UP/DOWN.
- **Проверка маршрутов**: тест, что каждый роут в `routes.go` соответствует существующему хендлеру (ловит dead/перекошенные роуты вроде voting-400).
- **Логирование**: JSON-логгер в проде, request-id, тайминги SQL в hot paths.
- **Мониторинг**: Prometheus-метрики на SubmitCode, листинг, SSE/WS-подключения.
- **Dependency-скан**: `govulncheck`/`dependabot` в CI.

### Пользовательский опыт
- **Единый компонент подтверждения** с per-action текстами кнопок и restore фокуса.
- **Единый realtime-клиент**: один `createReconnectingWebSocket` для чатов, монитора, уведомлений; `ws-client.js` удалить.
- **Пустые состояния** вместо skeleton во всех списках (✅ админка — уже).
- **Офлайн-first**: precache `/dashboard`, `start_url`; content-hash для статики или `stale-while-revalidate`.
- **Адаптивность**: бейджи с `dark:` вариантами, `prefers-reduced-motion`.
- **Персонализация** (✅ начато): серверные предпочтения (вид списка) — расширить на другие настройки.
- **A11y-проход**: skip-link единственный, focus-trap+restore, aria-live для таймеров/чатов, keyboard-доступ к drop-zone, aria-labels пагинации.

---

## 6. ✅ Что сделано хорошо (подтверждено)

- **Безопасность:** параметризованные запросы; CSP с per-request nonce; exact-match origin-проверки WS; crypto/rand для токенов/стейтов; refresh-token rotation + SHA-256 хранение; JTI blacklist; generic-ошибки логина/восстановления; `subtle.ConstantTimeCompare` для OAuth state; body-size limits; whitelist загрузок; rate limiting (in-memory sharded + Valkey Lua).
- **Транзакции:** `FOR UPDATE`-блокировки в игровых путях, broadcast после commit, `SkipDefaultTransaction`.
- **Производительность:** `COUNT(*) OVER()` там, где нужно; singleflight в кэше и мониторе; shared per-game кэш; batch SQL (unnest, CASE-UPDATEs); общий poller монитора; неблокирующий `BroadcastToRoom` с drop-on-full.
- **UX:** кастомная модалка с focus-trap; reconnecting WebSocket с backoff и polling fallback; guard от дублей сообщений; progressive-enhancement wizard (✅ в этой сессии); серверная персонализация (✅).
- **Архитектура:** чёткое разделение model/service/handler; DI через wire; sentinel-ошибки начаты; идемпотентные миграции; i18n ru/en синхронны (тест).

---

## 7. Приоритеты следующей волны

1. **B1** — турнирные очки при обычном финише + идемпотентность (данные/деньги турниров).
2. **S1/S2/S3/S4** — JWT iss/aud, роль из БД, dummy-bcrypt, token families.
3. **B2** — lockout-политика (email+IP, generic-ответы).
4. **B3–B10** — обработка ошибок БД, атомарность reset-password, гонки CheckTimeouts/Move/Duplicate, StartTesting.
5. **P1/P2/P4** — async/debounce broadcast, инвалидация `game:%d`.
6. **UX1/UX2/UX3/UX4/UX10** — двойные confirm, label кнопок, зависающие кнопки, FOUC, immutable-кэш.
