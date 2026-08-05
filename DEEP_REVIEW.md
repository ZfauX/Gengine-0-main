# Deep Review Gengine-0 — 5 августа 2026 (pass 22)

Повторный глубокий аудит после закрытия всех пунктов pass 21 (4 волны исправлений: безопасность, корректность, производительность, UX, тесты; коммиты d4798d5…27396ef).

**Методология:** 5 параллельных ревью-агентов (безопасность, производительность, корректность/архитектура, UX/фронтенд, тесты) + **ручная верификация** каждой критической/высокой находки по коду. Ложные срабатывания исключены (перечислены отдельно).

**Легенда:** 🔴 критично · 🟠 высоко · 🟡 средне · 🟢 хорошо · ❌ ложное срабатывание (проверено)

---

## 1. 🔴 Критические проблемы

### 1.1 Фронтенд

**A-1. `initPushSubscription()` вызывается, но функция не определена — ReferenceError на каждой странице** ✅ подтверждено
- `static/js/app.js:330` — `document.addEventListener('DOMContentLoaded', ... initPushSubscription())`, но определения `function initPushSubscription` в кодовой базе **нет** (удалена в волне 3 вместе с мёртвыми модулями, вызов остался).
- Каждая страница кидает uncaught ReferenceError; push-кнопки `#enable-push`/`#disable-push` (`profile-show.html`, `notification-settings.html`) мёртвые; `data-i18n-push-*` в layout не используются.
- **Бэкенд push готов** (`/api/push/subscribe|unsubscribe|vapid-public-key`, `user/routes.go:180-186`) — фичу можно доделать.
- **Фикс:** либо реализовать `initPushSubscription` (subscribe через `pushManager` + POST `/api/push/subscribe`), либо удалить вызов, кнопки и атрибуты.

**U-1. Пагинация логов сломана** ✅ подтверждено
- `monitor/templates/logs-list.html:29,46` — `document.getElementById('current-page')`, но элемента `id="current-page"` в разметке **нет**. На строке 46 — `TypeError` (нет `?.`), `currentPage = NaN` → `?page=NaN`.
- **Фикс:** инициализировать `currentPage` из `{{.Page}}` и добавить `id="current-page"` в разметку (или убрать обращения).

### 1.2 Производительность

**P-C1. `StaticCacheMiddleware` не зарегистрирован — статика без cache-заголовков** ✅ подтверждено
- `internal/pkg/middleware/static_cache.go` определён и покрыт тестами, но `r.Use(middleware.StaticCacheMiddleware())` в `internal/app/router.go` **отсутствует**. `r.Static("/static")` и `/uploads` зарегистрированы без middleware.
- Иммутабельный `?v=`-кэш (P4 из pass 21) фактически не работает: браузер пере-запрашивает CSS/JS на каждой странице; `/uploads` (включая ответы игроков) кэшируются эвристически вместо `no-cache`.
- **Фикс:** одна строка `r.Use(middleware.StaticCacheMiddleware())` перед `r.Static(...)` в `setupEngine`.

**P-H1. Valkey-кэш листинга/лидерборда/отзывов никогда не хитнит (type-assertion miss)** ✅ подтверждено
- `svc_listing.go:64-67` (`cached.(listingCacheEntry)`), `svc_rating.go:182-187` (`cached.([]LeaderboardEntry)`), `svc_review.go:81-85` (`cached.([]Review)`) используют `GetWithCtx` + типовая ассерция.
- На `ValkeyCache` `GetWithCtx` (`valkey.go:144-163`) JSON-десериализует в `map[string]any` → ассерция **всегда падает**. Только `cacheGetGame`/`cacheGetRating` имеют Valkey-aware raw-bytes пути.
- **Результат:** в проде (с Valkey) кэш 30s-листинга, 5min-лидерборда и 5min-отзывов молча отключён — каждый запрос пере-выполняет SQL на самых горячих анонимных эндпоинтах.
- **Фикс:** повторить паттерн `cacheGetGame` (store `[]byte` JSON, читать `vc.GetBytesWithCtx` + `json.Unmarshal`, type-switch fallback для in-memory).

### 1.3 Корректность

**C-C1. Tournament `UpdateScoresForGame` — гонка двойного начисления** ✅ подтверждено
- `tournament/service.go:302-392` + `repository.go:118-123`: фильтр `tournament_scored = false` только в **read**-запросе `ListFinishedPassings` (вне транзакции); внутри транзакции прохождения **не пере-проверяются атомарно**; пометка `tournament_scored=true` — простой `UPDATE ... WHERE id IN ?` **без `RowsAffected`-guard**; **нет `pg_advisory_xact_lock`** (в отличие от `svc_rating.go:34` и `svc_monitor.go:276`).
- **Trigger:** две команды финишируют в одном окне → два параллельных `UpdateScoresForGame` → оба читают те же `tournament_scored=false` прохождения → двойные очки.
- **Фикс:** скопировать паттерн `svc_rating.go:45-51` — атомарный claim `UPDATE game_passings SET tournament_scored=true WHERE id=? AND tournament_scored=false` с `RowsAffected` + advisory lock на gameID.

**C-C2. Nil-pointer dereference при ошибке сохранения настроек** ✅ подтверждено
- `game/hnd_settings.go:189-198`: при ошибке `SaveSettings` возвращает `nil, err`, а хендлер делает `"Settings": *settings` (строка 194) → panic (маскируется gin recovery, но 500-страница не рендерится).
- **Фикс:** guard `if settings == nil { settings = &GameSetting{} }`.

---

## 2. 🟠 Высокие проблемы

### Безопасность

**S-H1. Passkey (WebAuthn) — потенциальный backdoor: регистрация не требует 2FA, `FinishLogin` не проверяет 2FA** ✅ подтверждено
- `user/routes.go:90-91` — `/auth/webauthn/register/begin|finish` только за `AuthRequired`, **не** за `TwoFactorRequired`.
- `webauthn_handler.go` `FinishLogin` выпускает JWT **без проверки** `user.TwoFactorEnabled`.
- При смене пароля WebAuthn-ключи **не удаляются** (только при admin-delete).
- **Риск:** при украденной сессии атакующий регистрирует свой authenticator и логинится без TOTP; backdoor переживает смену пароля.
- **Фикс:** при `TwoFactorEnabled` требовать 2FA-флаг/код перед регистрацией passkey; при смене пароля удалять WebAuthn-ключи или требовать re-auth; явно определить политику «passkey = второй фактор?» и применять в `FinishLogin`.

**S-M5. `LogoutAll` не отзывает текущий JWT** ✅ подтверждено
- `user/auth_handler.go:340-353`: `Logout` вызывает `RevokeJWT` (строка 317), `LogoutAll` — только `RevokeAllUserTokens` (refresh) + очистку куки. Украденный JWT валиден до expiry.
- **Фикс:** `RevokeJWT` в `LogoutAll` тоже.

**S-M4. Session-cookie `Secure` игнорирует reverse-proxy** ✅ подтверждено
- `app/router.go:55-60`: `Secure: app.Config.TLS.CertFile != ""`, тогда как JWT-куки используют `isHTTPS()` (X-Forwarded-Proto/ForceSecureCookie). За TLS-терминирующим прокси session-cookie (pending_user_id, oauth_state, 2fa_verified_*, WebAuthn challenge) уходит по HTTP.
- **Фикс:** тот же `isHTTPS` для session store.

**S-M1. 2FA backup-code верификация без rate limit / lockout** ✅ подтверждено
- `user/routes.go:83`, `two_factor_handler.go:153-207`: `/auth/2fa/backup` только за `AuthRequired`, без per-user лимита; `/auth/2fa/login` — только 5/min per-IP. Backup-коды 6-значные → brute-force через много IP.
- **Фикс:** rate limiter keyed by userID + счётчик неудач с блокировкой.

**S-M2. `/api/*` state-changing с cookie-auth без CSRF-токена/Origin-check** ✅ подтверждено
- `app/app.go:93-101` исключает `/api` из CSRF; `csrf_json.go` — мёртвый код. Смягчение: `SameSite=Strict`. Defense-in-depth: Origin/Referer check на не-GET `/api/*`.

**S-M3. `/api/users/search` — публичный enumeration без спец-лимита** ✅ подтверждено
- `user/handler.go:79-104`, `routes.go:142`: без auth, маскированный email (первый символ + домен), имена + ID. Применить `OptionalAuth` + IP-лимит.

**S-L1.** 2FA disable только по паролю (`two_factor_handler.go:435-492`) — стандарт требует TOTP/backup-код.
**S-L2.** VK OAuth email не верифицирован, аккаунты линкуются по email (`service.go:683-712`) — контроль неверного email мог бы захватить чужой аккаунт.
**S-L3.** Смена email не сбрасывает `email_verified` (`service.go:479-484`) — флаг становится устаревшим.

### Корректность

**C-H1. `UpdateRatingsForGame`: log-and-continue портит флаг идемпотентности** ✅ подтверждено
- `svc_rating.go:45-152`: guard `rating_scored=true` ставится первым, затем 5 точек глотают ошибки (`return nil` — коммит транзакции): ошибка после установки флага → `rating_scored=true` без начисленных очков, повтор не исправит.
- **Фикс:** возвращать ошибку из транзакции во всех 5 точках (флаг откатится).

**C-H2. `AcceptInvitation` — гонка дублирования `team_members`** ✅ подтверждено
- `team/service.go:238-272`: два параллельных accept одного Pending-приглашения → оба читают Pending вне tx, оба `UPDATE` без фильтра `status='pending'` и без RowsAffected, оба INSERT в team_members.
- **Фикс:** `UPDATE ... WHERE id=? AND status='pending'` + abort при RowsAffected=0; `INSERT ... ON CONFLICT DO NOTHING`.

**C-H3. `TournamentService.Apply` — check-then-insert на tournament_teams** ✅ подтверждено
- `tournament/service.go:194-273`: `GetByTournamentAndTeam` → `AddTeamTx Create` без `ON CONFLICT`.
- **Фикс:** `ON CONFLICT (tournament_id, team_id) DO NOTHING` + RowsAffected.

**C-H5. `DeleteLevelFromActiveGame` может оставить команду без активного прогресса** ✅ подтверждено
- `svc_admin.go:204-220`: цикл `continue` при любой ошибке advance и всё равно удаляет уровень; команда на удаляемом уровне остаётся без прогресса.
- **Фикс:** abort транзакции при ошибке advance или детерминированно завершать такие passings.

**C-H4 (пограничный). `Clauses(Locking{...})` на UPDATE в `checkTimeoutsImpl`** ✅ проверено
- `svc_progress.go:342-347`: GORM v1.26 не билдит clause `LOCKING` для UPDATE (`updateClauses = ["UPDATE","SET","WHERE","RETURNING"]`) — это **no-op, не синтаксическая ошибка**. Но комментарий «FOR UPDATE сериализует» вводит в заблуждение; защита — только `RowsAffected`. Рекомендуется убрать clause defensive и поправить комментарий.

**C-M1. `SaveSettings` (notification) — update-then-insert race** ✅ подтверждено
- `notification/service.go:150-171`: `GetByUserID` → `Create`. Применить `clause.OnConflict{Columns: user_id}` как в `game/service.go:539`.

**C-M2. `RemoveGame` списывает очки по текущему месту, а не по начисленным** ✅ подтверждено
- `tournament/service.go:142-176`: после пересчёта мест/изменения PointsFor списание не совпадает с начисленным; Score может уйти в минус (кламп в 0).
- **Фикс:** хранить начисленные очки на прохождении (`tournament_points`) и списывать точное значение.

**C-M3. `CloseVoting` tie-break недетерминирован** ✅ подтверждено
- `monitor/service.go:289-295`: итерация map → при равенстве голосов случайный победитель.
- **Фикс:** детерминированный тай-брейк (ранний голос / лексикографический option).

**C-M4. `LevelService.Move` маскирует ошибки БД как «некуда двигать»** ✅ подтверждено
- `level/service.go:234-247`: `First(&sibling)` — и `ErrRecordNotFound`, и реальные ошибки → одно сообщение. Различать через `errors.Is`.

**C-M5. `GetOrCreateTeamChatRoom` — check-then-insert + хардкод «Команда: »** ✅ подтверждено
- `team/repository.go:218-232`, `service.go:41`: без `ON CONFLICT`; русская строка в сервисе.
- **Фикс:** upsert + `i18n.TF`.

**C-M6. `MarkAsRead` не ставит `ReadAt`** ✅ подтверждено
- `notification/service.go:347-360`: обновляет `read=true`, поле `ReadAt *time.Time` в модели мёртвое.

**C-M7. LIKE wildcard injection в поиске пользователей** ✅ подтверждено
- `team/repository.go:198-215`: `%`/`_` в query не экранируются → `%` = все пользователи. Экранировать `%`, `_`, `\`.

**C-M8. `UpdateScoresForGame` возвращает void и молча глотает ошибки** ✅ подтверждено
- `tournament/service.go:302-392`: почти все `return` без логов — ops не видят сбои начисления.
- **Фикс:** логировать каждую точку выхода (или вернуть error, логировать в main).

**C-M9. `GetGameplayData`: ошибка settings оставляет zero-value** ✅ подтверждено
- `svc_play.go:654-666`: при не-NotFound ошибке `settings` = zero (hints off), не `defaultGameSetting()`.

**C-M10. `SnapshotDispatcher`: лишний flush после debounce-reset** ✅ подтверждено (незначительно)
- `svc_snapshot.go:37-65`: stale-таймер может удалить из map новый. Безвредно (идемпотентно), но добавить generation/ID.

### Производительность

**P-H4. `GetGameplayData` — 5-6 последовательных запросов на загрузку страницы** ✅ подтверждено
- `svc_play.go:640-725`: passing+Team+Game.GameSetting (3), fallback settings (1), progress+Level (1), attempts LIMIT 50 (1), voting session (1) — всё последовательно.
- **Фикс:** `errgroup` на независимые запросы (attempts/voting/settings), settings из `game:%d` кэша, `LEVEL`-кэш для GameSetting.

**P-H5. `checkTimeoutsImpl` — `LIMIT 50` без `ORDER BY` + N+1 advance** ✅ подтверждено
- `svc_progress.go:268-385`: без ORDER BY по partial-индексу `(game_passing_id, finished_at)` одни и те же прохождения могут сканироваться каждые 30с; per-row: `tx.First(passing)` + `AdvanceToNextLevel` (passing + все уровни + create) — до 150 запросов за цикл.
- **Фикс:** `ORDER BY started_at ASC`, prefetch уровней по игре, cap per-run.

**P-H3. `SubmitCode` транзакция — ~10-11 round-trips** ✅ подтверждено
- `helpers.go:16-40` `CheckTeamMembership` (локирует passing + запросы) + `svc_play.go:112-157` повторно `tx.First(&passing)`. `GetCurrentProgressForUpdate` уже лок.
- **Фикс:** пробросить teamID/gameID из locked passing, убрать второй `First`, объединить membership+captain в один LEFT JOIN.

**P-M1. Двойной `CalculateResults` на финише** ✅ подтверждено
- `onGameFinished` (`main.go:267-283`) → `ProcessSnapshot` (`svc_play.go`) снова после debounce. Идемпотентно, но лишний advisory-lock + full query.
- **Фикс:** флаг «финиш уже обработан» или skip в `ProcessSnapshot`.

**P-M2. `SSEManager.Broadcast` синхронный — один медленный клиент блокирует хендлер до 10s** ✅ подтверждено
- `hnd_sse.go:203-244`: `session.write()` с 10s deadline в цикле подписчиков на вызывающей стороне (пост-commit хендлер или snapshot worker).
- **Фикс:** per-session buffered channel + writer goroutine (как WS write pump), либо short-lived goroutine с семафором.

**P-M3. Monitor SSE poller — marshal каждую секунду даже без изменений** ✅ подтверждено
- `monitor/handler.go:107-156`: всегда `GetOrFetchSnapshot` + `json.Marshal` + broadcast. Пропускать при неизменном `timestamp`.

**P-M6. `notification.getUnreadCount` — COUNT на каждую отправку** ✅ подтверждено
- `notification/service.go:300,313-319`: кэшировать или отдавать в одном запросе с insert.

**P-M7.** gzip применяется ко всем ответам включая уже сжатые JSON API — пропускать некомпрессируемые типы.
**P-M8.** `ForceFinishGame`/`DisqualifyTeam`/`DeleteLevelFromActiveGame` — O(teams × levels); батчить.
**P-M9.** `checkAutoStartGamesImpl` — лишний `Preload("GameSetting")` (JOIN уже грузит) + per-passing init.

### UX / Фронтенд

**U-2. Удаление ответа уровня без подтверждения** ✅ подтверждено
- `level/templates/answers-index.html:17-20`: голый POST-delete; везде в проекте используется `data-confirm`/`data-confirm-form`.

**U-3. FOUC + race на переключателе вида списка игр** ✅ подтверждено
- `games-list.html:229-236`: таблица серверная, карточки подменяются после `GET /api/users/preferences/games-view` → flash; клик до ответа перезаписывается.

**U-4. На мобильном дублируются таблица И карточки прохождений** ✅ подтверждено
- `game_passings-list.html:6` (таблица без `hidden sm:block`) + `:64` (`sm:hidden` карточки) → на телефонах видно и то и то.

**U-5. Полный re-render мониторинга при каждом WS-снапшоте** — `teamsContainer.innerHTML = ''` + rebuild → фокус/скролл прыгают, анимация повторяется.
**U-6. Чаты принудительно скроллят вниз, даже если пользователь прочитал выше** — auto-scroll только при близости к низу.
**U-7. Кнопка подписки без in-flight guard** (`profile-public.html:49-51`) — double-click = race.
**U-8. Добавление заметки без `.catch` и блокировки кнопки** (`notes-manage.html:55-73`).
**U-9. Debug-логи в проде** — `console.log/warn` с API-данными (`profile-public.html:110-149`, `monitor-page.html:183,236`, `gameplay-show.html:478`).
**U-10. aria-label кнопки закрытия тоста = «Отмена»** (`app.js:72`) — нужен `data-i18n-close`.
**U-11. Поиск соавторов не с клавиатуры** (`co_authors-manage.html:79-93`) — combobox/listbox.
**U-12. Wizard без aria-current на шагах и без фокуса при смене шага.**
**U-13. Пустые `<p class="field-error">` рендерятся всегда** (`game_passings-apply.html:22`, `games-new.html:34-64`) — guard `{{if .Errors.X}}`.

### A11y
**A-2.** `aria-live` на таймере геймплея озвучивает каждую секунду (`gameplay-show.html:25,193-196`) — объявлять на границах минут / `role="timer"`.
**A-3.** Preview/delete/photo модалки без focus-trap (восстанавливают фокус, но Tab уходит на фон).
**A-4.** `alt="Photo"`/`alt="Preview"` — неинформативные.
**A-5.** Непрочитанное уведомление — только цвет (`notifications-list.html:13`); добавить sr-only.
**A-6.** Календарные ячейки/фото-миниатюры не с клавиатуры (`calendar-page.html:122-130`, `games-photos.html:149-161`).
**A-7.** Touch-цели < 44px: гамбургер (24px + `focus:outline-none` убирает фокус), языковые кнопки, колокольчик, пагинация, карандаши, крестик тоста.
**A-8.** Поля без имени: поиск (`home.html:9`), textarea заметки (`notes-manage.html:9`) — добавить sr-only label.
**A-9.** Нет `aria-expanded`/`aria-pressed`/`aria-current` на: колокольчике, переключателе вида, активной комнате чата, активном фильтре админки.
**A-10.** `role="img" aria-hidden="true"` на эмодзи-лого (`layout.html:168`) — противоречиво.
**A-12.** Пустые `<script nonce>` блоки в 4 шаблонах — мёртвая разметка.
**A-13.** Логи не в `role="log"` (в отличие от чата) — SR не объявляют новые строки.

### Тесты
**T-H1. Tournament `UpdateScoresForGame`/`RemoveGame` — БЕЗ тестов** (3 теста в пакете, метод не вызывается).
**T-H2. `CheckTimeouts`/`CheckAutoStartGames` — без тестов** (0 упоминаний).
**T-H3. `SnapshotDispatcher` — без тестов** (debounce, Close).
**T-H4. `CalculateResults` — без тестов тай-брейков/штрафов** (только базовый 2-командный).
**T-H5. SubmitCode last-level finish path — не покрыт** (все тесты проходят только уровень 1 из 2; callback → CalculateResults + tournament + rating не проверен).
**T-M1.** Админ `DownloadBackup` — нет теста; `DeleteUser` не отзывает refresh-токены (наблюдение в коде) — тест не проверяет.
**T-M2.** Нет чистых юнит-тестов без БД для доменной логики — всё DB-backed, `-short` в CI ничего не проверяет в domain/*.
**T-M3.** `t.Parallel()` нигде не используется — DB-набор серийный.
**T-M4.** Пароли-«hashed» в фикстурах tournament/monitor — foot-gun.

---

## 3. ⚡ Варианты оптимизации (по приоритету)

### Быстрые и безопасные (дни)
1. **A-1** — убрать ReferenceError `initPushSubscription` (реализовать или удалить + кнопки).
2. **P-C1** — зарегистрировать `StaticCacheMiddleware` (1 строка).
3. **U-1** — починить пагинацию логов.
4. **C-C2** — guard nil в `hnd_settings.go`.
5. **C-H1** — атомарный claim `rating_scored` в 5 точках ошибок (вернуть err вместо log+continue).
6. **C-C1** — advisory lock + атомарный claim `tournament_scored` (скопировать паттерн rating).
7. **P-H1** — Valkey-aware чтение для listing/leaderboard/reviews.
8. **U-2/U-4/U-9** — confirm на удаление ответа, мобильный дубль, убрать console.log.
9. **C-M3/C-M6/C-M7** — детерминированный tie-break, ReadAt, LIKE escape.
10. **S-M5** — RevokeJWT в LogoutAll.

### Средние
11. **S-H1** — WebAuthn/2FA политика (регистрация под 2FA, удаление ключей при смене пароля).
12. **P-H3/P-H4** — оптимизация SubmitCode tx (~10 round-trips) и GetGameplayData (errgroup).
13. **P-M1/P-M2/P-M3** — двойной CalculateResults, SSE fan-out, poller по изменению.
14. **C-H2/C-H3** — гонки AcceptInvitation / Tournament.Apply (ON CONFLICT).
15. **S-M1** — rate limit + lockout для 2FA backup-code.
16. **S-M4** — Secure session cookie через isHTTPS.
17. **P-H5** — ORDER BY в CheckTimeouts + prefetch.
18. **A-7/A-2/A-3/A-9** — touch-цели, aria-live таймера, focus-trap модалок, aria-state toggle.

### Крупные архитектурные
19. **T-H1…T-H5** — юнит-тесты tournament/CheckTimeouts/SnapshotDispatcher/CalculateResults (mock-based, работают без БД).
20. **C-M2** — `tournament_points` на прохождении для точного списания.
21. **C-M8** — возврат ошибки из `UpdateScoresForGame` + логирование.
22. **S-M2/S-M3** — CSRF/Origin для `/api/*` + лимит публичного поиска.
23. **Единый подход к тестам**: mock-based слой для доменной логики, чтобы CI (`-short`) реально проверял бизнес-правила.

---

## 4. 🚀 Предложения по улучшению

### Кодовая база
- **Атомарные guards единообразно**: паттерн `UPDATE ... WHERE flag=false` + RowsAffected для всех «начислить один раз» (rating ✅, tournament ❌).
- **Valkey-стратегия**: единый helper `cacheGetTyped(ctx, key, &target)` для всех typed-кэшей (вместо 3 дублей).
- **Адvisory-локи**: `pg_advisory_xact_lock(gameID)` для всех операций «финиш/начисление/пересчёт» (monitor ✅, rating ✅, tournament ❌, level.Move ✅, level.Duplicate ❌).
- **CI-гейт**: `make test-integration` в CI с Postgres; `-short` — mock-based доменные тесты.
- **Логирование ошибок начисления**: `UpdateScoresForGame` должна возвращать ошибку и логироваться в main.
- **Убрать `Clauses(Locking{...})` с UPDATE** (no-op в GORM, вводит в заблуждение).
- **Проверка маршрутов**: тест «каждый роут = существующий хендлер».
- **Pre-commit guard** на стейджинг `.env`/`backups/`.

### Пользовательский опыт
- **Push-уведомления**: либо доделать (бэкенд готов), либо удалить мёртвые кнопки/атрибуты.
- **Адаптивность**: скрыть таблицу прохождений на мобильном (U-4).
- **A11y-проход**: touch-цели 44px, aria-state на всех toggle, focus-trap в модалках, sr-only для цвета.
- **Чат**: авто-скролл только у низа; мониторинг — обновлять карточки инкрементально (diff).
- **Формы**: guard от двойного submit (follow, notes), `.catch` для сети.
- **Отладка**: убрать `console.log` из прода.
- **Поиск соавторов**: полноценный combobox.

---

## 5. ✅ Что сделано хорошо (подтверждено)

- **Асинхронный snapshot-воркер (S3)** — debounce 500ms, корректный Close, nil-safe Schedule, 10s timeout в ProcessSnapshot.
- **Атомарный guard рейтинга** (`svc_rating.go:45-51`) — образцовый паттерн.
- **`pg_advisory_xact_lock`** в CalculateResults, UpdateRatingsForGame, LevelService.Move.
- **onCommit после commit** во всех игровых путях (SubmitCode/AcceptBlackboxAnswer/checkTimeoutsImpl/ForceFinishGame).
- **Refresh-ротация** атомарная + семья + строгий fingerprint; **роль из БД** в AuthRequired; **lockout** с generic-ответом и dummy-bcrypt.
- **JWT**: HMAC/iss/aud/nbf/iat/jti + blacklist; **CSRF** с узким skip-list; **WS/SSE origin**; **upload** санитайз + sniffing.
- **Обработка ошибок**: `errors.Is(ErrRecordNotFound)` в AcceptPendingAttempt, DisqualifyTeam, StartVoting (дубликат-ключ).
- **PWA**: единый источник версии (config.StaticAssetsVersion ⇄ ASSET_VERSION ⇄ ?v=), precache совпадает с URL, /offline публичен, uploads уникальные имена.
- **Модалка подтверждения**: focus-trap, Escape, focus-restore, per-action labels; **чаты**: role=log + дубли-suppression; **wizard**: progressive enhancement.
- **Миграции 000025-000032** идемпотентны, 000028 — pg_constraint-поиск.
- **Индексы** на горячих фильтрах (attempts, logs, partial finished, game_passings, notifications(user_id, created_at), pg_trgm).
- **Background-задачи**: bgWg/goSafe + корректный порядок shutdown (rate limiters → email → SSE → HTTP → hub → snapshot dispatcher → ctx → cache).

---

## 6. Ложные срабатывания (проверено и исключено)

| № | Заявка агента | Вердикт |
|---|---|---|
| C-H6 | Reset/change password не отзывают сессии | ❌ **Ложь** — `profile_handler.go:388-394` вызывает `RevokeAllUserTokens` + `RevokeJWT`; `auth_handler.go:600-606` (ResetPassword) тоже |
| S-H2 | SQL injection в svc_listing/svc_monitor/svc_rating | ❌ **Не подтверждено** — все Raw/Exec с позиционными `?` и args; ORDER BY whitelist; ILIKE через EscapeLike/placeholders |
| C-H4 | `FOR UPDATE` на UPDATE = синтаксическая ошибка PG | ❌ **Не ошибка** — GORM v1.26 не билдит Locking для UPDATE (no-op). Защита RowsAffected корректна; комментарий вводит в заблуждение |
| A-1-часть | `window.fetch` двойной wrap ломается | ❌ **Работает** — progress-враппер перехватывает уже-CSRF-обёрнутый fetch, заголовки добавляются |

---

## 7. Приоритеты следующей волны

1. **A-1** (ReferenceError) и **U-1** (пагинация логов) — ломают UX прямо сейчас.
2. **P-C1** (StaticCacheMiddleware) — 1 строка, возвращает кэширование статики.
3. **C-C1** (tournament race) и **C-H1** (rating flag) — целостность начислений.
4. **P-H1** (Valkey no-op) — производительность горячих эндпоинтов в проде.
5. **C-C2** (nil deref), **S-M5** (LogoutAll JWT), **U-2/U-4/U-9** — быстрые фиксы.
6. **S-H1** (WebAuthn/2FA) — безопасность учетки.
7. **T-H1…T-H5** — тесты критичных путей.
8. **P-H3/P-H4** — оптимизация геймплея.
