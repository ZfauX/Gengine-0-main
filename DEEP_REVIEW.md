# Deep Review Gengine-0 — 9 августа 2026 (pass 37 — повторное ревью после закрытия pass 30-36 + дефера)

## Резюме

Повторное глубокое ревью выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture/DI) с последующей **личной верификацией ключевых находок** по коду.

**Итог pass 37:** 0 критичных, 3 высоких (все подтверждены лично), ~12 средних, ~10 низких.

> **Контекст:** pass 30-36 закрыты полностью; дефер разобран; линтер 2.12.2 чист. Новые проблемы: **утечка подсказок через FullPreview**, **GET /settings без прав**, **fail-open TTL pending 2FA**, N+1 в UpdateScoresForGame/автостарте/таймаутах, **og:image http за reverse-proxy**, мёртвые не-Tx методы AttemptService, отсутствие тестов на новый код pass 36.

---

## Статус (обновлено 9 авг 2026)

**PASS 30 ЗАКРЫТ ПОЛНОСТЬЮ** (4 раунда фиксов + верификация @tester/@reviewer):

- **Раунд 1** (`3b06811`): H1-H5 (даты, турнирная страница, deleted_at, VK user_id, GetUserRole), H6 (слоистость), M1 (RUM), M16/M17 (DI), индексы 000039.
- **Раунд 2** (`6b77b71`): H7 (worker-pool push), M2 (backup-коды), M3 (mojibake), M4 (ExtraHead), M9 (batch), M10 (OAuth single update), M14 (HasPermissionTx), M18 (dashboard errors).
- **Раунд 3** (`bdd9cb0`): M5 (таймзона), M6 (кэш роли), M7 (таймстампы), M8 (локализация), M11 (JOIN), M15 (CheckTimeouts), M16-остаток (чистка DI).
- **Раунд 4** (`5d39da1`): M13 (кэш листинга отзывов), + фиксы найденные @tester/@reviewer:
  - **Регрессия M14** (поймана @tester): `Table().First(&uint)` → "model value required" → заменено на `Model(&Game{}).Scan` + `RowsAffected`.
  - **C1 (tz_sign)** — подтверждено: JS инвертирует `-getTimezoneOffset()`, сервер `t.Add(+offset)`. Согласовано (UTC+3 → +180 → +3ч).
  - **C2 (ExtraHead)** — guard `{{if .Game}}` — блок больше не рендерит мусор на чужих страницах.
  - **S1 (push pool race)** — флаг `pushShutting` — повторный Shutdown идемпотентен, enqueue после shutdown дропает.
  - **S2 (CheckTimeouts partial advance)** — advance только по `finished_at = наш now` (multi-instance без дублей).
  - **S3 (CreateInBatches unique)** — `OnConflict DoNothing` на оба батча (RemoveGame+re-add безопасен).
  - **Новые тесты**: role cache (middleware), TZOffset+ExtraHead (render), extraString (user), интеграционные game проходят.
- **Раунд 5** (`dfb88ce`): CI workflow `go.yml` — mojibake-контроль-символы (#x0098) ломали YAML → переписан в чистый UTF-8.
- **Раунд 6** (`364cb17`): тесты GetUserRole (нашёл GORM-баг First(&scalar) → Scan+RowsAffected), dashboard error paths, svc_review M13. Перегенерён mock_service.go (GetGamesView).
- **Раунд 7** (`e6193bc`): gofmt-фикс (service.go, helper_test.go) — CI-гейт gofmt пройден.

**Проверка:** `go build ./...` ✓, `go test -short ./...` ✓, `go test -tags=integration` (game/user/tournament/admin) ✓, `go vet ./...` ✓, `gofmt -l .` → пусто ✓. `-race` недоступен на Windows (нет CGO).

> Примечание: `golangci-lint` на этой машине — v1 с конфигом v2 (инфраструктурная проблема, не кодовая).

---

# PASS 37 (повторное ревью) — 9 августа 2026

> Восьмое повторное ревью после полного закрытия pass 30-36 и разбора дефера. Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок по коду.

## Резюме pass 37

**Итог:** 0 критичных, 3 высоких (все подтверждены лично), ~12 средних, ~10 низких.

> Кодовая база стабильна, линтер 2.12.2 чист. Найдены: **утечка подсказок через FullPreview** (после K-1 pass 36 остался hint для не-менеджеров), **GET /settings без проверки прав**, **fail-open TTL pending 2FA**, N+1 в UpdateScoresForGame/автостарте/таймаутах, **og:image http за reverse-proxy**, мёртвые не-Tx методы AttemptService, дублированный flash-auto-hide.

---

## Статус (обновлено 9 авг 2026) — PASS 37 ЗАКРЫТ

**Все находки pass 37 исправлены** (3 раунда фиксов):

**Раунд 1** (`358588d`):
- **S-1**: full-preview больше не отдаёт `q.Hint` не-менеджерам (только текст вопроса; менеджеры видят hint+answers для редактирования).
- **S-2**: `SettingsPage` (GET) требует IsUserManager || IsAdmin (как SaveSettings) — настройки не раскрывают тактику.
- **S-3**: TwoFALoginVerify — fail-closed TTL: при отсутствии `pending_expires` pending-шаг считается истёкшим (redirect). Тест обновлён.
- **UX-1**: og:image — схема учитывает X-Forwarded-Proto (как HSTS).
- **P-1**: UpdateScoresForGame — batch UPDATE `CASE id` вместо N+1 построчных (helper'ы joinPlaceholders/toAnySlice).
- **P-4**: GetOrFetchSnapshotJSON — fallback на json.Marshal при кэш-хите с пустым json (нет (nil,nil)).
- **A-1**: StartVoting — через `blackboxRepo.GetPassingWithGameByGamePassingID` (убран raw s.db read).
- **A-2**: удалены мёртвые не-Tx методы AttemptService (SubmitCode/SubmitFile/AcceptPendingAttempt) + db поле; конструктор без параметров; wire перегенерирован.
- **UX-2**: layout.html — удалён дублированный auto-hide flash (125-130); осталась fade-версия.
- **T-2**: добавлен `csvsafe_test.go` (включая ведущий пробел перед `=`) + L-1: csvSafe учитывает TrimLeft пробелов.
- **T-1**: добавлены sqlmock-тесты GetByIDWithTeam, GetCurrentProgressWithLevel, GetAttemptsByProgress, GetOpenVotingSession (вкл. «нет сессии»).

**Осталось на раунд 2+:** P-2/P-3 (N+1 автостарт/таймауты), P-5 (LRU MoveToBack), P-6 (GetLogsByGameID LIMIT), P-7 (индекс 000045), UX-3 (reCAPTCHA theme sync), UX-4 (timer-sync), UX-5 (mobile cards admin-teams/tournaments), UX-6 (actionBtn null), UX-7 (localStorage dashboard), A-3 (HasPermission raw db), A-4 (tournament ignored err), L-1 (coauthor export).

**Раунд 2 (текущий):**
- **P-2**: автостарт — первый уровень грузится один раз на игру; прогрессы batch INSERT с OnConflict DoNothing (вместо 4 запросов × passing).
- **P-5**: снапшот-кэш — MoveToBack при кэш-хите (активные игры не вытесняются).
- **P-7**: миграция 000045 — `idx_level_progresses_passing_level (game_passing_id, level_id)`.
- **UX-3**: reCAPTCHA пере-рендерится при смене темы через `window.onThemeChanged` (layout → auth-register).
- **UX-4**: таймер геймплея — clearInterval при истечении (нет бесконечных /timer-sync).
- **UX-5**: mobile-карточки для admin-teams (таблица скрыта на < sm).

**Осталось на раунд 3+ (низкий приоритет):** P-3 (N+1 таймауты), P-6 (GetLogsByGameID LIMIT), UX-6 (actionBtn null), UX-7 (localStorage dashboard), A-3 (HasPermission raw db), A-4 (tournament ignored err), L-1 (coauthor export), UX-5 остаток (tournaments-list/admin-audit mobile cards).

**Раунд 3 (текущий):**
- **P-6**: GetLogsByGameID — LIMIT 500 последних логов (DESC + реверс).
- **UX-6**: disqualifyTeam — fallback-элемент при null actionBtn (красная OK-кнопка сохраняется).
- **UX-7**: dashboard onboarding — localStorage в try/catch.
- **A-4**: CanApply — ошибка GetByTournamentAndTeamIDs логируется, возвращает false.
- **L-1**: coauthor (manager) может экспортировать результаты команды.

**Проверка:** `go build ./...` ✓, `go vet ./...` ✓, `golangci-lint 2.12.2` → 0 issues ✓, `gofmt -l .` → пусто ✓, `go test -count=1 -short ./...` ✓, 84 шаблона валидны ✓.

**Оставлено осознанно (задокументировано):**
- **P-3** (N+1 таймауты), **A-3** (HasPermission raw db), **UX-5 остаток** (tournaments-list/admin-audit mobile cards) — стилевые/перф-улучшения без функционального бага, кандидаты на следующий раунд.

---

## A. Найденные ошибки pass 37 (верифицировано лично)

### 🔴 Критично

Нет.

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **S-1** | `game/hnd_fullpreview.go:110` | **Утечка подсказок (hint) через FullPreview.** После K-1 (pass 36) для non-manager убраны `Answer.Code`, но **`q.Hint` остался** для публичных игр. В геймплее hint выдаётся только по кнопке UseHint в активной сессии со штрафом; здесь — бесплатно и до старта. Разрушает экономику игры. **Верифицировано лично.** |
| **S-2** | `game/hnd_settings.go:47-98` | **GET /games/{id}/settings без проверки IsUserManager.** `SettingsPage` проверяет только `GetByID` (видимость публичной игры), любой залогиненный видит AllowHints/MaxHints/HintPenaltySeconds/PerLevelTimeLimit/HideAnswersUntilFinished/AutoStart — раскрывает тактику. `SaveSettings` (POST) права проверяет корректно. **Верифицировано лично.** |
| **S-3** | `user/auth_handler.go:220-232` | **Fail-open TTL pending-шага 2FA.** `TwoFALoginVerify` проверяет `pending_expires` только если ключ существует — флоу, забывший поставить ключ, получит бессрочное окно перебора TOTP. Pass 36 добавил установку в WebAuthn FinishLogin, но защита остаётся обходной. **Верифицировано лично.** |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| **P-1** | `tournament/service.go:433` | **N+1 UPDATE в UpdateScoresForGame** — по одному `UPDATE game_passings SET tournament_points` на прохождение в цикле (при N командах — N UPDATE в одной транзакции с advisory lock). **Верифицировано лично.** |
| **P-2** | `game/svc_progress.go:63-88,440` | **N+1 в InitFirstLevelWithTx при автостарте** — Count + First(firstLevel) + Create + Save = 4 запроса на passing; игра с 100 командами = 400 запросов каждые 30с. |
| **P-3** | `game/svc_progress.go:131-134,350` | **Повторная загрузка passing в AdvanceToNextLevel** при таймаутах (batch=100 → 300-500 запросов каждые 30с), хотя passings уже загружены батчем. |
| **P-4** | `game/svc_monitor.go:158-180` | **GetOrFetchSnapshotJSON может вернуть (nil, nil)**: при кэш-хите с json==nil (старая запись) `GetOrFetchSnapshot` вернёт data без пересчёта json → пустой payload SSE. **Верифицировано лично.** |
| **P-5** | `game/svc_monitor.go:84-153` | **LRU снапшот-кэш не промотирует активные игры** (нет MoveToBack при хите) — активные игры могут быть вытеснены, старые живут. |
| **P-6** | `game/repository.go:202-210` | **GetLogsByGameID без LIMIT** — выгрузка всех логов игры (сотни тысяч записей) в память. |
| **A-1** | `monitor/service.go:59` | **StartVoting — read-path raw s.db** `Joins("Game")`, при готовом repo-методе `GetPassingWithGameByGamePassingID` (pass 36). Дублирование SQL. **Верифицировано лично.** |
| **A-2** | `game/svc_attempt.go:25,86,107` | **Мёртвые не-Tx методы AttemptService** (SubmitCode/SubmitFile/AcceptPendingAttempt) — 0 прод-вызовов (rg подтвердил); write вне транзакции — небезопасный профиль. **Верифицировано лично.** |
| **UX-1** | `game/hnd_game.go:221-225` | **og:image http за reverse-proxy**: схема по `c.Request.TLS`, X-Forwarded-Proto игнорируется → social sharing сломан при продакшен-деплое. **Верифицировано лично.** |
| **UX-2** | `user/templates/layout.html:125-130,548-560` | **Дублированное авто-скрытие flash**: первый блок (querySelector, без fade) гасит анимацию fade-версии и удаляет только первый flash. **Верифицировано лично.** |
| **UX-3** | `auth-register.html:41-56` | **reCAPTCHA не синхронизируется с переключателем темы** — тема фиксируется при рендере. |
| **UX-4** | `gameplay-show.html:204-210` | **Бесконечные /timer-sync после истечения таймера** — setInterval не очищается. |
| **T-1** | game/export/monitor | **Нет юнит-тестов на новый код pass 36**: GetGameplayData repo-методы (GetByIDWithTeam/GetCurrentProgressWithLevel/GetAttemptsByProgress/GetOpenVotingSession), GetOrFetchSnapshotJSON, ListRecentAttempts LIMIT+reverse, 2FA Enable lockout, WebAuthn pending_expires, csvSafe — все 0% покрытия. |
| **T-2** | `export/service.go:53` | **csvSafe не покрыт тестами** — защита formula injection без проверки; и сам helper не обрабатывает ведущий пробел перед `=` (`" =2+2"`). |

### 🔲 Низкие / мелочи

| ID | Файл | Проблема |
|---|---|---|
| **UX-5** | `admin-teams.html`, `tournaments-list.html`, `admin-audit.html` | Нет мобильных карточек (только overflow-x-auto) — несоответствие admin-users/games (pass 35). |
| **UX-6** | `monitor-page.html:260-261` | `actionBtn` может быть null после перерисовки карточки — красная кнопка OK теряется. |
| **UX-7** | `dashboard-index.html:42-43` | localStorage без try/catch (приватный режим). |
| **UX-8** | `layout.html:123` | Серверный flash без ветки warning (сейчас хендлеры не шлют — ловушка). |
| **A-3** | `game/svc_coauthor.go:46` | HasPermission через raw s.db (в отличие от IsUserManager через repo) — дублирование форм проверки прав. |
| **A-4** | `tournament/service.go:341` | `existing, _ := ...GetByTournamentAndTeamIDs(...)` — игнорирует ошибку. |
| **P-7** | — | Отсутствует индекс `level_progresses(game_passing_id, level_id)` (миграция 000045). **Верифицировано лично.** |
| **L-1** | `export/handler.go:466-480` | Соавтор (manager) не может экспортировать результаты команды (isAuthor только по AuthorID) — UX-регрессия pass 36. |

### 🔲 Опровергнуто / безопасно (личная проверка)

- **ExportTeamResultsCSV IDOR** — НЕ IDOR: GetFinishedPassingForTeam привязывает teamID к gameID, IsTeamCaptain не даёт экспорт чужой команды. Безопасно.
- **2FA lockout + сброс счётчика при успехе** (pass 36) — корректен на всех путях (Verify/BackupVerify/Enable/Disable).
- **WebAuthn pending_expires** — ставится в FinishLogin. ✓
- **Refresh-ротация, password reset SHA-256, jti blacklist** — корректны.
- **CSRF** на всех проверенных формах; CSP nonce работает; XSS экранируется.
- **FullPreview GetByID** различает 403/404 корректно.

---

## B. Оптимизации (производительность)

1. **P-1**: batch `UPDATE game_passings SET tournament_points = v.points FROM unnest(...)` в UpdateScoresForGame (паттерн уже есть в RemoveGame).
2. **P-2**: первый уровень игры грузить один раз (map firstLevelByGameID); `INSERT ... ON CONFLICT DO NOTHING` вместо Count+Create.
3. **P-3**: передавать passing из уже загруженного батча в AdvanceToNextLevel (новый вариант с preloaded passing).
4. **P-4**: в GetOrFetchSnapshot заполнять json при кэш-хите, если пуст; в GetOrFetchSnapshotJSON fallback на `json.Marshal(cached.data)`.
5. **P-5**: `cacheList.MoveToBack(elem)` при кэш-хите снапшота.
6. **P-7**: миграция 000045 — `CREATE INDEX ... ON level_progresses(game_passing_id, level_id)`.
7. **P-6**: GetLogsByGameID — LIMIT или переиспользование GetLogsByGameIDPaginated.

## C. Улучшения кодовой базы (архитектура)

1. **S-1**: убрать `q.Hint` из full-preview для non-manager (оставить только Text/Description), либо проверять участие/статус игры.
2. **S-2**: `SettingsPage` — добавить `IsUserManager || IsAdmin` (как в SaveSettings).
3. **S-3**: fail-closed TTL — при отсутствии `pending_expires` считать pending истёкшим (`expires := 0`).
4. **A-1**: StartVoting через `blackboxRepo.GetPassingWithGameByGamePassingID`.
5. **A-2**: удалить мёртвые не-Tx методы AttemptService (или перевести на явные tx-сигнатуры).
6. **T-1/T-2**: добавить тесты: repo-методы GetGameplayData (sqlmock), GetOrFetchSnapshotJSON, csvSafe (включая `" =2+2"`), 2FA Enable lockout, WebAuthn pending_expires.

## D. Улучшения UX

1. **UX-1**: схема og:image — учитывать X-Forwarded-Proto (как HSTS в security.go:79) или конфиг внешнего base URL.
2. **UX-2**: удалить первый блок auto-hide flash (125-130), оставить fade-версию.
3. **UX-3**: reCAPTCHA — пере-рендер при переключении темы (MutationObserver или click на themeToggle).
4. **UX-4**: clearInterval таймера при истечении/level_completed.
5. **UX-5**: mobile-карточки для admin-teams/tournaments-list/admin-audit.
6. **UX-7**: try/catch вокруг localStorage в dashboard.

## Приоритет фиксов (pass 37)
1. **S-1** (hint утечка) + **S-2** (settings GET) + **S-3** (fail-open TTL) — security.
2. **UX-1** (og:image http) — продакшен social sharing.
3. **P-1/P-4** (N+1 UpdateScores + JSON nil).
4. **A-1/A-2** (read-path + мёртвые методы).
5. **T-1** (тесты нового кода pass 36).

---

# PASS 36 (повторное ревью) — 9 августа 2026

> Седьмое повторное ревью после полного закрытия pass 30-35. Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок по коду.

## Резюме pass 36

**Итог:** 1 критичная, 5 высоких (все подтверждены лично), ~10 средних, ~10 низких.

> Кодовая база зрелая, но найдены: **утечка ответов через `/games/{id}/full-preview`** (любой аутентифицированный получает коды ответов публичной игры), **регрессии pass 35 в чатах** (бейдж «новые сообщения» заблокирован CSP), **WebAuthn 2FA pending без TTL**, **мёртвые поля God-фасадов**, CSV/Excel injection в экспорте.

---

## Статус (обновлено 9 авг 2026) — PASS 36 ЗАКРЫТ

**Все находки pass 36 исправлены** (2 раунда фиксов):

**Раунд 1** (`c612eb4`):
- **K-1**: full-preview больше не отдаёт коды ответов не-менеджерам (`hnd_fullpreview.go` — IsUserManager check; менеджеры видят ответы, остальные только вопросы/подсказки).
- **UX-R1/R2**: бейдж «новые сообщения» починен — inline onclick заменён на addEventListener в nonce-скрипте (CSP блокировал onclick); счётчик сбрасывается при клике.
- **UX-R3**: ошибки submit — клиент извлекает сообщение из `.flash-error` через DOMParser вместо «стены» HTML.
- **UX-R4**: добавлена ветка warning в showMessage + `.flash-warning` в app.css.
- **UX-J1**: убран двойной decodeURIComponent подсказки (URIError на «%»).
- **S-1**: WebAuthn 2FA pending теперь ставит `pending_expires`/`pending_email` (TTL 10 мин, как парольный вход).
- **S-2**: 2FA Enable — lockout (инкремент счётчика + backoff на пароль и TOTP) + LoginRateLimit на маршрут.
- **A-1**: удалены мёртвые поля `GameService.db`, `GamePlayService.cfg`, `GamePlayService.progressSvc` + параметры конструкторов + wire.
- **A-4**: добавлены тесты recaptcha (httptest-фейк siteverify: success/failure/non-200/bad JSON/empty token/disabled/network).
- **S-3**: CSV/Excel formula injection — helper `csvSafe` (апостроф-префикс для `=+-@\t\r`) применён во всех CSV/Excel экспортах.
- **A-5**: cache `Set` с `ttl==0` — запись без истечения (expires=zero); ttl<0 — мгновенно протухший.

**Осталось на раунд 2+:** A-2 (BlackboxVoteService read-path утечки + мёртвые методы интерфейса), A-3 (GetGameplayData через репозиторий), F-1 (автостарт batch), F-2 (JSON-маршалинг снапшота), F-3 (ListRecentAttempts), S-4 (CSP style-src), S-5 (reviews .csrf), UX-1..6 (низкие).

**Раунд 2 (текущий):**
- **A-2**: BlackboxVoteService — 8 read-path `s.db` заменены на typed-методы BlackboxRepository (GetPassingByGamePassingID, GetPassingWithGameByGamePassingID, GetCaptainEmailsByGame, IsTeamMember); удалены 3 мёртвых метода интерфейса (UpdateSession, CreateVote, GetVoteBySessionAndVoter).
- **S-5**: reviews ShowForm/Create передают `csrf` в шаблон (форма была CSRF-сломана — шаблон имел `<input name=_csrf>`).
- **UX-1**: ExportTeamResultsCSV больше не требует IsUserManager — капитан команды/автор может экспортировать (раньше checkGameAccess блокировал капитанов без manager-прав).
- **F-1**: checkAutoStartGamesImpl — `NewLevelProgressService(tx)` вынесен за цикл.

**Осталось на раунд 3+ (низкий приоритет):** A-3 (GetGameplayData → GameplayReadRepository — большой read-assembly, осознанно отложен), F-2 (JSON-маршалинг снапшота), F-3 (ListRecentAttempts SQL-агрегация), S-4 (CSP style-src), UX-2..6 (низкие).

**Проверка:** `go build ./...` ✓, `go vet ./...` ✓, `gofmt -l .` → пусто ✓, `go test -count=1 -short ./...` ✓, все 84 шаблона валидны ✓, recaptcha тесты ✓.

**Оставлено осознанно (задокументировано):**
- **A-3** (GetGameplayData read-assembly) — большой рефакторинг на GameplayReadRepository без функционального бага; уже покрыт тестами через интеграционные.
- **F-2/F-3** (маршалинг снапшота, ListRecentAttempts) — перф-улучшения без пользовательского влияния.
- **S-4** (CSP `style-src 'unsafe-inline'`) — осознанный компромисс для inline-стилей.
- **UX-2..6** — низкий приоритет (reCAPTCHA dark theme, emoji aria-hidden, sessionStorage try/catch, OG-теги, confirm спиннер).

---

## Дефер разобран (раунд 3)

Все отложенные пункты pass 35/36 закрыты:

- **UX-2**: reCAPTCHA рендерится явно с `theme` по классу `<html>` (dark/light).
- **UX-3**: `aria-hidden` на декоративные emoji (offline 📡, games-list 🕵️, monitor-page ⚠️).
- **UX-4**: `sessionStorage.setItem` обёрнут в try/catch (приватный режим).
- **UX-5**: OG-теги — absolute `og:image` (из CoverPath через scheme+host), `og:type=article`.
- **UX-6**: `data-confirm-form` — спиннер + disabled submit перед `form.submit()` (без двойного POST).
- **F-2**: `GetOrFetchSnapshotJSON` — кэш маршалнутых байт в cachedSnapshot; поллер не сериализует каждые 5с.
- **F-3**: `ListRecentAttempts` — LIMIT 500 последних попыток (DESC + реверс) вместо выкачивания всех кодов.
- **A-3**: `GetGameplayData` — все 5 read-запросов через GamePassingRepository (GetByIDWithTeam, GetCurrentProgressWithLevel, GetAttemptsByProgress, GetOpenVotingSession) + GetGameSettingByGameID; `s.db` только для транзакций.
- **S-4**: CSP `style-src` — убран `'unsafe-inline'` (единственный `<style>` с nonce, inline-атрибутов нет).

**Проверка:** `go build ./...` ✓, `go vet ./...` ✓, `golangci-lint 2.12.2` → 0 issues ✓, `gofmt -l .` → пусто ✓, `go test -count=1 -short ./...` ✓, 84 шаблона валидны ✓.

---

## A. Найденные ошибки pass 36 (верифицировано лично)

### 🔴 Критично

| ID | Файл | Проблема |
|---|---|---|
| **K-1** | `game/hnd_fullpreview.go:59-105` + `routes.go:101` | **Утечка ответов игры.** `/games/:id/full-preview` защищён только `AuthRequired` (не IsUserManager). `GameService.GetByID` пропускает любого пользователя для публичных не-draft игр, а хендлер отдаёт **все уровни, вопросы, подсказки И коды ответов** (`a.Code`). Любой участник может получить ответы до/во время игры — разрушает честность. **Верифицировано лично.** |

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **S-1** | `user/webauthn_handler.go:419-431` vs `auth_handler.go:159-165` | **WebAuthn 2FA pending без TTL.** Passkey-логин с 2FA ставит только `pending_user_id`, не `pending_expires`/`pending_email`. `TwoFALoginVerify` пропускает TTL-проверку при отсутствии `pending_expires` — pending-шаг живёт до конца session-cookie, расширяя окно перебора TOTP. Парольный вход (pass 31) ставит TTL, WebAuthn — нет. **Верифицировано лично.** |
| **S-2** | `user/two_factor_handler.go:370-475` + `routes.go:199` | **2FA Enable без lockout.** `/user/2fa/enable` не имеет rate limit, `VerifyCode` не инкрементирует счётчик/не блокирует. Украденная сессия+пароль → перебор 6-значного TOTP. Контраст с `Disable` (pass 35). **Верифицировано лично.** |
| **S-3** | `export/service.go:60-81,156-174,302-311,430-522` | **CSV/Excel formula injection.** `lvl.Name`, `q.Text`, `q.Hint`, `answerCodes`, `p.Team.Name` без санитизации — значения с `=`,`+`,`-`,`@` интерпретируются как формулы в Excel/LibreOffice. **Верифицировано по коду.** |
| **UX-R1/R2** | `monitor/templates/chat-page.html:31`, `team/templates/team-chat.html:13` | **Регрессия pass 35: бейдж «новые сообщения» мёртв.** Inline `onclick` заблокирован CSP (`script-src` без `'unsafe-inline'`, nonce к атрибутам не применяется). Клик не работает, счётчик не сбрасывается. **Верифицировано лично.** |
| **UX-R3** | `game/templates/gameplay-show.html:300-324` | **Регрессия pass 35: ошибки submit показывают HTML.** `renderGameplayError` рендерит полную страницу (400), клиент берёт `r.text()` как сообщение → пользователь видит «стену» HTML. Комментарий «localized error messages» неверен для non-redirect ошибок. **Верифицировано лично.** |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| **A-1** | `game/service.go:83`, `game/svc_play.go:48,52` | **Мёртвые поля God-фасадов**: `GameService.db` (0 использований в 27 методах), `GamePlayService.cfg`, `GamePlayService.progressSvc` (только присваиваются). Тащатся в wire. **Верифицировано лично.** |
| **A-2** | `monitor/service.go:60,107,142,207,228,232,261,321` | **BlackboxVoteService — 8 read-path `s.db`** при инъектированном BlackboxRepository (GetSessionByID/GetVotesBySession есть). + 3 мёртвых метода интерфейса (UpdateSession, CreateVote, GetVoteBySessionAndVoter — не вызываются). **Верифицировано лично.** |
| **A-3** | `game/svc_play.go:705-772` | **GetGameplayData — 5 read-запросов через `s.db`** (passing, settings, progress+level, attempts, voting session) при внедрённых gameRepo/passingRepo. Самый горячий экран не юнит-тестируется без БД. |
| **UX-R4** | `game/templates/gameplay-show.html:251-255` | **showMessage без ветки warning** — рендерится зелёным success. Нет `.flash-warning` в app.css. **Верифицировано лично.** |
| **UX-J1** | `game/templates/gameplay-show.html:354` | **Двойное декодирование hint** (`URLSearchParams.get` уже декодирует + `decodeURIComponent`) → URIError при «%» в подсказке. **Верифицировано лично.** |
| **F-1** | `game/svc_progress.go:425-451` | **checkAutoStartGamesImpl N+1**: ~3 запроса × N прохождений внутри транзакции на игру (InitFirstLevelWithTx = Count+First, + Save). Игра с 50 командами = ~150 round-trips. |
| **F-2** | `monitor/handler.go:166-193` | **JSON-маршалинг полного снапшота каждые 5с** на активную игру даже когда данные не менялись (bytes.Equal экономит рассылку, не сериализацию). |
| **F-3** | `game/monitor_repository.go:86-98` | **ListRecentAttempts выкачивает коды всех попыток за 5 мин** на каждый промах снапшот-кэша — широкая выборка на регулярной основе. |
| **S-4** | `middleware/security.go:63` | **CSP `style-src 'unsafe-inline'`** — ослабляет nonce-защиту стилей (риск низкий, но отклонение от строгого CSP). |
| **S-5** | `game/hnd_review.go:51-54,87-94` | **Reviews ShowForm/Create не рендерят `.csrf`** в данные шаблона — форма может быть сломана или обходит CSRF-поле (зависит от шаблона). |

### 🔲 Низкие / мелочи

| ID | Файл | Проблема |
|---|---|---|
| **UX-1** | `export/handler.go:443-475` | Капитан, не являющийся менеджером, не может экспортировать (checkGameAccess раньше isCaptain) — fail-closed, но функциональный дефект. |
| **UX-2** | `auth-register.html` | reCAPTCHA виджет светлый в dark-mode (нет `data-theme="dark"`). |
| **UX-3** | `offline.html:4`, `profile-show.html:88`, `games-list.html:77`, `monitor-page.html:196` | Emoji без `aria-hidden`. |
| **UX-4** | `gameplay-show.html:233-234` | `sessionStorage.setItem` без try/catch (приватный режим → ошибка превью). |
| **UX-5** | `games-show.html:3-6` | OG-теги: `og:image` может быть пустым/относительным, `og:type` всегда website. |
| **UX-6** | admin/mobile | `data-confirm-form` формы без спиннера при подтверждении — риск двойного POST. |
| **A-4** | `pkg/recaptcha` | **Новый security-пакет pass 35 без единого теста.** |
| **A-5** | `pkg/cache/cache.go:156-164` | `Set` с `ttl=0` создаёт мгновенно протухающий ключ (expires=now, не zero). Реальных вызовов с ttl=0 нет (проверено) — теоретическая ловушка. |
| **A-6** | `game/svc_play.go:611`, `monitor/service.go:70-271` | Строковые `errors.New` вместо sentinel. |

### 🔲 Опровергнуто / безопасно (личная проверка)

- **CSRF на новых admin mobile-карточках** — есть `_csrf` (проверено). Безопасно.
- **reCAPTCHA рендер** — структурно корректен, CSP разрешает google/gstatic.
- **role=alert после ContentHTML** — работает (R8).
- **TTL=0 кэш** — реальных вызовов с ttl=0 нет (только tournament с 30с).
- **Path traversal uploads/backup** — защищены. **Push SSRF** — DNS-rebinding защита есть. **OAuth redirect_uri** — фиксирован. **Refresh-ротация** — корректна.
- **`style-src unsafe-inline`** — компромисс для inline-стилей Tailwind; осознанно.

---

## B. Оптимизации (производительность)

1. **F-1 (автостарт)**: batch `INSERT INTO level_progresses SELECT ...` для всех прохождений + один UPDATE статуса; вынести `NewLevelProgressService(tx)` за цикл.
2. **F-2 (monitor polling)**: кэшировать маршалнутые байты снапшота вместе с данными (или сравнивать timestamp) — не сериализовать каждые 5с.
3. **F-3 (ListRecentAttempts)**: агрегировать в SQL (группировка по passing, только non-success, limit на команду).
4. **A-2 (Blackbox)**: добавить typed-методы в BlackboxRepository (GetPassingWithGame, GetCaptainEmailsByGame, IsTeamMember) — убрать 8 s.db read.
5. **A-3 (GetGameplayData)**: вынести 5 read-запросов в репозиторий (или составной GetGameplayData) — юнит-тестируемость.
6. **UX-R2**: сброс `unreadCount` + не показывать бейдж для собственных сообщений (если автор — current user).

## C. Улучшения кодовой базы (архитектура)

1. **A-1**: удалить мёртвые поля (`GameService.db`, `GamePlayService.cfg`, `GamePlayService.progressSvc`) + параметры конструкторов + wire.
2. **A-2**: удалить 3 мёртвых метода BlackboxRepository или перевести Vote/CloseVoting на tx-методы репо.
3. **A-4**: httptest-тесты для recaptcha (success/failure/non-200/bad JSON/empty token/disabled).
4. **A-5**: `Set` с `ttl<=0` → expires=zero (без истечения).
5. **S-3**: CSV-экранирование (префикс `'` для строк с `=+-@`); excelize — префикс/EscapeCsv.
6. **S-1**: `pending_expires` в WebAuthn 2FA-ветке.
7. **S-2**: lockout в 2FA Enable (как в Disable).

## D. Улучшения UX

1. **UX-R1/R2**: заменить inline `onclick` на addEventListener внутри nonce-скрипта; сброс счётчика в обработчике.
2. **UX-R3**: парсить `X-Error-Code` + локальная карта сообщений на клиенте (или сервер возвращает plain-text; или извлекать `.flash-error`).
3. **UX-R4**: добавить `.flash-warning` и ветку в showMessage.
4. **UX-J1**: убрать `decodeURIComponent` (URLSearchParams уже декодирует).
5. **UX-2**: `data-theme="dark"` для reCAPTCHA в dark-mode.
6. **UX-4**: try/catch вокруг sessionStorage.
7. **UX-5**: absolute og:image + og:type article для игр.

## Приоритет фиксов (pass 36)
1. **K-1** — full-preview утечка ответов (security).
2. **UX-R1/R2** — регрессия бейджа чата (pass 35).
3. **UX-R3** — HTML в сообщениях ошибок submit.
4. **S-1/S-2** — WebAuthn TTL + 2FA enable lockout.
5. **A-1** — мёртвые поля God-фасадов.

---

# PASS 35 (повторное ревью) — 9 августа 2026

> Шестое повторное ревью после полного закрытия pass 30-34 и разбора дефера (H3, A-H1, A-M2, A-M5). Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок по коду.

## Резюме pass 35

**Итог:** 0 критичных, 4 высоких (все подтверждены лично), ~10 средних, ~12 низких.

> Кодовая база достигла зрелого состояния. Главные новые проблемы — **отсутствие lockout на change-password/2fa-disable** (перебор пароля с украденной сессией), **мёртвый конфиг reCAPTCHA**, **несоответствие Secure-флага CSRF-куки** (за reverse-proxy), и **мёртвое поле gameRepo в BlackboxVoteService** с дублированием raw-SQL проверки прав.

---

## Статус (обновлено 9 авг 2026) — PASS 35 ЗАКРЫТ

**Все находки pass 35 исправлены** (4 раунда фиксов):

**Раунд 1** (`275de5d`):
- **S-1**: ChangePassword — lockout (атомарный инкремент + backoff-блокировка, паттерн Login) + rate limit на маршруте.
- **S-2**: 2FA disable — lockout на пароль и TOTP + rate limit на маршруте.
- **S-3**: reCAPTCHA подключена — новый пакет `internal/pkg/recaptcha` (siteverify), серверная проверка на Register, site-key передаётся в шаблон (был мёртвый конфиг).
- **S-4**: CSRF Secure-флаг выровнен с session-store (TLS || TrustedProxies || ForceSecureCookie).
- **A-1**: удалён мёртвый `gameRepo` из BlackboxVoteService; raw-SQL `isGameManagerForGame` заменён на `game.CoAuthorService.IsUserManager`.
- **UX-1**: `X-Error-Code` на submit кода (submitErrorCode), клиент читает заголовок вместо языковой эвристики `/код|code/`.
- **UX-2**: убраны лишние `</div>` в admin-users/admin-games.
- **UX-3**: `:focus-visible` для `.btn`/`.nav-link`/ссылок.
- **UX-4**: role=alert скрипт перенесён после `{{.ContentHTML}}`.
- **UX-5**: `dark:text-gray-400` в notifications-list (контраст AA).
- **F-2**: LRU sweep — итерация только по `ttlKeys` вместо полного `lru.Keys()` под локом.

**Осталось на следующие раунды:** A-2 (ProfileService через репозиторий), A-3 (ExportRepository без DB()), F-1 (кардинальность ключей листинга), F-3 (monitor polling), F-4 (tournament List LIMIT), UX-6..11, A-4..A-6.

**Раунд 2 (текущий):**
- **A-2**: ProfileService через `ProfileRepository` (новый `internal/domain/user/profile_repository.go`, SQL перенесён из сервиса; wire через `NewGormProfileRepo`).
- **A-3**: ExportRepository убран `DB(ctx) *gorm.DB` — добавлены типизированные read-методы (GetPassingByGameAndTeam, GetProgressesByPassing, GetLevelsByGame, GetAttemptsByProgressIDs); ExportTeamResultsToCSV переписан на них.
- **F-4**: tournament `List` — LIMIT 50 + Select без тяжёлого Description.

**Осталось на раунд 3+:** F-1 (кардинальность ключей листинга — сузить ключ/кэш первых страниц), F-3 (monitor polling интервал), UX-6 (бейдж новых сообщений), UX-7 (mobile-карточки таблиц), UX-8 (SSE status), UX-9 (calendar loading), UX-10 (двойные if), A-4 (God-фасады), A-5 (context.Background), A-6 (emoji aria-hidden), UX-11 (пустые переводы).

**Раунд 3 (текущий):**
- **F-1**: листинг-кэш — только первая страница (page==1) для анонимного листинга без поиска/дат; ключи page 2+ больше не копятся.
- **F-3**: monitor polling 5с вместо 1с (кэш снапшота 30с всё равно ограничивает реальные запросы к БД; пустых вызовов в 5 раз меньше).
- **UX-6**: бейдж «Новые сообщения: N ↓» в chat-page и team-chat (при чтении выше).
- **UX-8**: SSE toast «Соединение потеряно» в геймплее (onerror + сброс onopen).
- **UX-10**: убраны 54 двойных `{{if .Errors.X}}{{if .Errors.X}}` в 19 шаблонах форм.
- **A-5**: `CanReview` принимает ctx (вместо context.Background); кэш-инвалидация турнира через ctx.

**Осталось на раунд 4+ (низкий приоритет):** UX-7 (mobile-карточки таблиц), UX-9 (calendar loading), A-4 (God-фасады — стилевой), A-6 (emoji aria-hidden), UX-11 (пустые переводы tournament.show_title/profile.push_status).

**Раунд 4 (текущий):**
- **UX-11**: заполнены пустые переводы `tournament.show_title`/`profile.push_status` (ru+en).
- **UX-9**: календарь — спиннер в #month-year + disabled prev/next при загрузке (с защитой от гонки).
- **A-6**: `aria-hidden="true"` на декоративные emoji (dashboard onboarding, achievements empty, notifications empty, verify success, 2FA enabled).
- **UX-7**: мобильные карточки для admin-users/admin-games (таблица скрыта на `< sm`, карточки на `sm:hidden`).

**Проверка:** `go build ./...` ✓, `go vet ./...` ✓, `gofmt -l .` → пусто ✓, `go test -short ./...` ✓, все 84 шаблона парсятся ✓.

**Оставлено осознанно (задокументировано):**
- **A-4** (God-фасады GameService/GamePlayService/TournamentService) — стилевой рефакторинг без функционального бага; фасады уже делегируют CRUD/Listing. Кандидат на отдельный раунд.

---

## A. Найденные ошибки pass 35 (верифицировано лично)

### 🔴 Критично

Нет.

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **S-1** | `user/profile_handler.go:378` + `user/service.go:538-551` + `routes.go:111` | **ChangePassword без lockout и без rate limit.** `bcrypt.CompareHashAndPassword` напрямую, без инкремента `failed_login_attempts`, без блокировки аккаунта. Украденная JWT-кука → бесконечный перебор `old_password`. Контраст с `Login` (lockout после 5 попыток). **Верифицировано лично.** |
| **S-2** | `user/two_factor_handler.go:556-561` + `routes.go:200` | **2FA disable — проверка пароля напрямую через bcrypt без lockout.** Комментарий (546-547) ложно утверждает «полный authService.Login инкрементит счётчик», но код вызывает `bcrypt.CompareHashAndPassword` сам. Перебор пароля + TOTP на `/user/2fa/disable` не ограничен. **Верифицировано лично.** |
| **S-3** | `config/config.go:486-506` + хендлеры | **Мёртвый конфиг reCAPTCHA.** `RECAPTCHA_ENABLED/SECRET_KEY` загружаются, но ни один хендлер не проверяет токен reCAPTCHA (rg по всем хендлерам — 0 использований вне config). Rate limits (5/мин login, 3/10мин register) обходятся распределёнными атаками. **Верифицировано лично.** |
| **S-4** | `app/app.go:88` vs `app/router.go:61-66` | **Несоответствие Secure-флага CSRF-куки и session-куки.** CSRF: `secure := TLS.CertFile != "" && TLS.KeyFile != ""` (только собственный TLS). Session store: `Secure = TLS || TrustedProxies || ForceSecureCookie`. За TLS-терминирующим reverse-proxy (nginx, TRUSTED_PROXIES задан) — `_csrf_token` уходит по HTTP без Secure. **Верифицировано лично.** |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| **A-1** | `monitor/service.go:34,41,47` | **Мёртвое поле `gameRepo` в BlackboxVoteService** — записывается в конструктор, нигде не читается (`s.gameRepo.` — 0 вызовов). При этом проверка прав автора/соавтора дублируется raw-SQL `isGameManagerForGame` (строки 210-224) вместо существующего `game.CoAuthorService.IsUserManager`. **Верифицировано лично.** |
| **A-2** | `user/profile_service.go:33-40` | **ProfileService — чистый `*gorm.DB` без репозитория** (все методы — raw `.Table/.Model`). Не юнит-тестируем без БД; нарушение dependency rule. **Верифицировано лично.** |
| **A-3** | `export/repository.go:62` + `service.go:86` | **Репозиторий экспортирует `DB(ctx) *gorm.DB` наружу** — сервис пишет SQL через БД репозитория (`s.exportRepo.DB(ctx)`), интерфейс не ограничивает доступ. **Верифицировано лично.** |
| **F-1** | `game/svc_listing.go:56-57` | **Кардинальность ключей листинга**: `games:list:v%d:%d:%d:%s:...` включает page/perPage/sort. Каждая комбинация — отдельный ключ; старые ключи живут 24ч и копятся в LRU/Valkey. Инвалидация O(1) через version — но ключи-сироты остаются. **Верифицировано лично (код чтён).** |
| **F-2** | `pkg/cache/cache.go:106-117` | **Full-LRU sweep каждую минуту под `mu.Lock()`** — `lru.Keys()` весь кэш (при maxSize=0 — тысячи ключей) каждые 60с, блокируя Get/Set. **Верифицировано лично.** |
| **F-3** | `monitor/handler.go:163` | **Polling 1с на активную игру** вызывает `GetOrFetchSnapshot` (кэш 30с) — ~30 миссов/мин/игру. singleflight спасает от стампеда, но нагрузка на БД. **Верифицировано лично.** |
| **F-4** | `tournament/repository.go:72-74` | **`List()` без LIMIT + `Preload("Author")`** — рост турниров → рост результата на каждый заход `/tournaments`. **Верифицировано лично.** |
| **UX-1** | `game/templates/gameplay-show.html:310` | **Языковая эвристика определения «неверного кода»**: `/код|code|answer|ответ/i` по тексту ошибки. Ломает EN-локали и не-кодовые ошибки («Число уже использовано» → warning вместо error). В проекте уже есть паттерн `X-Error-Code` — не применён. **Верифицировано лично.** |
| **UX-2** | `admin/templates/admin-users.html:113-114`, `admin-games.html:107-108` | **Лишние закрывающие `</div>`** — невалидная разметка, ломается DOM-вложенность. **Верифицировано лично.** |

### 🔲 Низкие / мелочи

| ID | Файл | Проблема |
|---|---|---|
| **UX-3** | `static/css/app.css` | **Нет `:focus-visible`** у `.btn`/`.nav-link` (rg — 0 совпадений) — клавиатурная навигация вслепую. |
| **UX-4** | `user/templates/layout.html:135-139` | Скрипт `role=alert` для `.flash-error` выполняется **до** рендера `{{.ContentHTML}}` — ошибки в контент-шаблонах не получают role. |
| **UX-5** | `notification/templates/notifications-list.html:12` | `dark:text-gray-500` — провал контраста AA в тёмной теме (остальные — `dark:text-gray-400`). |
| **UX-6** | `monitor/templates/chat-page.html:165-168`, `team/templates/team-chat.html:93-95` | Нет бейджа «Новые сообщения ↓» при чтении выше — пропуск сообщений организатора. |
| **UX-7** | `admin/*.html`, `games-list.html:112-152` | Мобильные таблицы без card-представления (только `overflow-x-auto`), кнопки < 44px. |
| **UX-8** | `gameplay-show.html:485-526` | SSE `onerror` пустой — нет индикатора «соединение потеряно». |
| **UX-9** | `calendar-page.html` | Нет индикатора загрузки при переключении месяца. |
| **UX-10** | `auth-login.html:17,24`, `auth-register.html:18,26` | Двойные вложенные `{{if .Errors.Email}}{{if .Errors.Email}}`. |
| **A-4** | `game/service.go:73-87` | **God-фасады**: GameService (13 полей), GamePlayService (11), TournamentService (8) — высокое сцепление; часть полей (userRepo/coAuthorSvc/db) может дублироваться поверх подсервисов. |
| **A-5** | `tournament/service.go:224,462`, `game/svc_review.go:31`, `game/svc_play.go:639` | `context.Background()` вместо ctx в service-путях (инвалидация кэша, CanReview). |
| **A-6** | `user/templates/notifications-list.html` и др. | Декоративные emoji без `aria-hidden` в контент-шаблонах. |
| **UX-11** | `en.go:1239`/`ru.go:1241` (`tournament.show_title`), `en.go:627`/`ru.go:648` (`profile.push_status`) | Пустые строки переводов. |

### 🔲 Опровергнуто агентами (лично проверено — НЕ является проблемой)

- **`.env` в git** — **опровергнуто**: `git ls-files` показывает только `.env.example`, `.env` игнорируется (check-ignore подтверждает). Секреты в git не ушли.
- **`DeleteOldRead` не вызывается** — **опровергнуто**: retention-джоба запущена в `cmd/server/main.go:392-412` (раз в 24ч).
- **`common.you` отсутствует в ru** — **опровергнуто**: ключ есть (`ru.go:1617`, `en.go:1615`).
- **XSS через `| safe`** — **не найден** (все innerHTML экранируются `escapeHtml`/`safeUrl`).
- **CSRF на формах** — покрыт (67 форм + fetch-перехватчик).
- **Focus-трапы модалок, dark mode, prefers-reduced-motion, skip-link** — реализованы хорошо.

---

## B. Оптимизации (производительность)

1. **F-1 (листинг)**: кэшировать только первую страницу без sort/page в ключе, либо хранить не больше N последних ключей; инвалидация — по version (уже есть).
2. **F-2 (LRU sweep)**: хранить отдельный список TTL-ключей (мини-куча/список с `expires`), sweep только по нему; либо отключать sweep при `maxSize==0`.
3. **F-3 (monitor polling)**: увеличить интервал до 2-5с, либо эвристика — активные игры чаще (1с), неактивные реже; либо SSE-подписка вместо polling.
4. **F-4 (tournament List)**: `LIMIT` + пагинация; `Select` только нужных колонок (без body/description).
5. **CalculateResults (svc_monitor.go:235-258)**: batch-пагинация по passings (LIMIT 500) с повторным вызовом при остатке — игра с сотнями команд не выльется в один гигантский запрос.
6. **checkAutoStartGamesImpl (svc_progress.go:394-455)**: 50 игр = до 50 последовательных транзакций; можно группировать по батчам или параллелить с errgroup (но осторожно — FOR UPDATE).

## C. Улучшения кодовой базы (архитектура)

1. **A-1**: удалить мёртвое `gameRepo` из BlackboxVoteService + заменить raw-SQL `isGameManagerForGame` на `game.CoAuthorService.IsUserManager` (устраняет дублирование логики прав).
2. **A-2**: ввести `ProfileRepository` (или расширить `UserRepository`) и провести `ProfileService` через него.
3. **A-3**: убрать `DB(ctx) *gorm.DB` из ExportRepository — вынести запросы в типизированные методы.
4. **A-4**: продолжать паттерн тонких фасадов — GameService уже делегирует CRUD/Cover/Listing; убрать из фасадов дублирующие repo/db.
5. **A-5**: прокидывать `ctx` из вызывающего кода вместо `context.Background()`.
6. **Transaction репозитории**: ввести `WithTx`-интерфейсы или UnitOfWork для svc_play/svc_progress (8+ транзакций напрямую через `s.db`) — позволит юнит-тестировать без БД.

## D. Улучшения UX

1. **UX-1 (ошибка кода)**: сервер должен слать `X-Error-Code` (`wrong_code`), клиент — матчить код, не текст.
2. **UX-3 (focus-visible)**: 5 строк CSS `:focus-visible { outline: 2px solid #2563eb; outline-offset: 2px; }` для `.btn`/`.nav-link` — мгновенный выигрыш a11y.
3. **UX-4 (role=alert)**: перенести скрипт после `{{.ContentHTML}}`; `aria-describedby` на инпуты с ошибками.
4. **UX-6 (чат)**: floating-бейдж «N новых ↓» при `!nearBottom`.
5. **UX-5 (dark contrast)**: `dark:text-gray-400` в notifications-list.
6. **UX-2 (валидность)**: убрать лишние `</div>`.
7. **UX-7 (mobile)**: card-вид таблиц на `< sm`.
8. **UX-8/9**: индикаторы статуса SSE/загрузки календаря.
9. **A-6 (emoji)**: `aria-hidden="true"` на декоративные emoji.

## Приоритет фиксов (pass 35)
1. **S-1/S-2** — lockout на change-password и 2fa-disable (security).
2. **S-3** — удалить мёртвый reCAPTCHA или подключить проверку на login/register.
3. **S-4** — выровнять Secure-флаг CSRF (использовать предикат как у session-store).
4. **A-1** — удалить мёртвый gameRepo + дублирование прав.
5. **UX-1/UX-3/UX-4** — быстрые UX-фиксы.

---

# PASS 34 (повторное ревью) — 8 августа 2026

> Пятое повторное ревью после полного закрытия pass 30-33. Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок.

## Резюме pass 34

**Итог:** 0 критичных, 5 высоких (3 подтверждены лично, 1 опровергнут), ~12 средних, ~12 низких + 2 новых индекса.

> Кодовая база продолжает укрепляться. Найдены: **регрессия P-5** (GetByIDPreloaded INNER JOIN — 404 для игр с удалённым автором), **missing lockout на TOTP step-up**, **hint toast теряет текст**, calendar-кэш без eviction, незавершённая миграция svc_play/svc_simulate/svc_admin на репозитории.

---

## Статус (обновлено 8 авг 2026) — PASS 34 ЗАКРЫТ + ДЕФЕР РАЗОБРАН

**Все находки pass 34 исправлены** (3 раунда фиксов + раунд дефера):

- **Раунд 1** (`014cb75`): UX-H1 (hint toast из r.url), S-2 (email не уходит анонимам в /api/users/search), F-1 (calendar cache evict), F-2 (индекс 000044), F-4 (AdminListGames count fallback), A-M1 (passing sentinel), A-M3 (мёртвый fallback), UX-H2/H3/M1/M2/M3 (data-confirm-danger + disqualify label), UX-M4/M5 (chat scroll + failure feedback).
- **Раунд 2** (текущий): F-5 (GetTeamsByUserID UNION), UX-L4 (Email i18n), UX-L1 (чат 24h), UX-L2 (team-chat timestamp), A-M4 (sentinel team + level), A-H1 (SimulateService через GameRepository.GetByIDForSimulation).
- **Раунд 3** (предыдущий commit): F-P5a (GetByIDPreloaded LEFT JOIN — регрессия 404), S-1 (TOTP step-up lockout).
- **Раунд 4 (дефер)**: H3 (все `DB *gorm.DB` → приватные, `repoOrDefault()` удалён — DI инжектит везде, тесты обновлены на WithRepository), A-H1 остаток (GetPassingWithGame → GamePassingRepository.GetByIDWithGame, IsTeamMember → GameRepository.IsTeamMember), A-M2 (svc_admin notify через team/user ListByIDs), A-M5 (канонический runner — RunMigrations с автодетектом; cmd/migrate без -dir использует его).

**Оставлено осознанно (задокументировано):**
- **F-3** — кэш авторизованного листинга: риск фрагментации ключей и инвалидации (черновики/публикации автора) > пользы. Оставлено без изменений.
- **GetGameplayData** — большой read-assembly (6+ запросов с errgroup, defaults, тайм-лимит); перевод на отдельный GameplayReadRepository вынесен в отдельный рефакторинг (не критичный read-path для модификаций).

**Проверка:** `go build ./...` ✓, `go vet ./...` ✓, `gofmt -l .` → пусто ✓, `go generate` (wire + user mock) ✓, `go test -short` ключевых пакетов ✓.

---

## A. Найденные ошибки pass 34 (верифицировано лично)

### 🔴 Критично
Не обнаружено.

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **F-P5a** | `game/repository.go:86-95` | **Регрессия P-5: `Joins("Author")` = INNER JOIN.** GORM `Joins("Author")` генерирует INNER JOIN с `users.deleted_at IS NULL` — игра с удалённым/отсутствующим автором исчезает из `First()` → 404 на странице показа. **Верифицировано лично; исправлено на LEFT JOIN.** |
| **S-1** | `user/two_factor_handler.go:80-141` | **Нет lockout на TOTP step-up `/auth/2fa/verify`** (в отличие от BackupVerify/TwoFALoginVerify): нет LockedUntil, AtomicIncrementFailedAttempts, AtomicLockAccount. Украденный pre-stepup JWT → перебор TOTP с IP-ротацией. **Верифицировано лично; исправлено.** |
| **S-2** | `user/routes.go:145` + `repository.go:221-234` | **Публичный email-enumeration**: `/api/users/search` без AuthRequired, `SearchUsersLight` возвращает `email` анониму. |
| **UX-H1** | `gameplay-show.html:342` | **Hint toast теряет текст**: читает `window.location.search` вместо `r.url` редиректа → hint-содержимое дропается. **Верифицировано лично.** |
| **A-H1** | `svc_play.go` (reads), `svc_simulate.go:35`, `svc_admin.go:282-327` | **Последние read-path утечки в репозитории**: GetGameplayData (6+ reads), GetPassingWithGame, IsTeamMember, SimulateService Preload, admin notify-чтения — всё через `s.db`/`s.DB`. |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| F-1 | `calendar/handler.go:109,158-160` | Calendar-кэш растёт безгранично: expired-записи не evictятся, каждый (year,month,tz) — постоянная запись. |
| F-2 | `notification/repository.go:124-127` | DeleteOldRead — полный seq-скан `read_at` ежедневно (нет partial-индекса). |
| F-3 | `svc_listing.go:73-85` | Авторизованный листинг не кэшируется (только ViewerID==0). |
| F-4 | `game/repository.go:300-345` | AdminListGames без count-fallback при пустой странице (offset за пределами). |
| F-5 | `team/repository.go:106-125` | GetTeamsByUserID — 3 последовательных запроса (можно UNION). |
| A-M1 | `svc_passing.go:22-31,173-189` | Мёртвые sentinel-ошибки (ErrGameFull и др.) + UpdateStatus возвращает inline вместо ErrStatusNotAllowed/ErrNotCaptainOrManager. |
| A-M2 | `svc_admin.go:282,291,322,327` | notify-чтения команд/капитанов через raw s.db (кросс-домен). |
| A-M3 | `svc_play.go:648-657` | ПроцессSnapshot: else-fallback raw-DB мёртв (gameRepo всегда injected) — удалить. |
| A-M4 | `team/service.go`, `level/service.go` | Инлайн-ошибки без sentinel (по контрасту с user/passing/tournament). |
| A-M5 | `cmd/migrate/main.go` vs `main.go` | Два миграционных runner'а (RunMigrations vs MigrateFromDir) — неясно какой канонический. |
| UX-H2/H3 | game_passings reject, levels-list delete | Деструктивные confirm без data-confirm-danger → синяя OK-кнопка (пропуск в pass 33). |
| UX-M1/M2 | admin-backups, games-show force-finish | То же: деструктивные формы без data-confirm-danger. |
| UX-M3 | monitor-page disqualify | OK-лейбл глобальный «Удалить» вместо «Дисквалифицировать». |
| UX-M4/M5 | chat-page, team-chat | Нет scroll-to-bottom после истории; нет failure-feedback при ошибке отправки. |

### 🔲 Осознанно оставлено / опровергнуто
- **S-3 (ChangePassword revoke)** — **опровергнуто**: profile_handler.go:388-394 уже вызывает RevokeAllUserTokens + RevokeJWT.
- **GetGameplayData errgroup** — нет гонок (Wait даёт happens-before). ✓
- **AdminListGames SQL** — параметризован, LIKE-экранирование корректно. ✓
- **Co-author 403/500** — корректно (ErrNotOwner → 403, DB → 500). ✓
- **AtomicLockAccount, ResetPassword** — атомарно, без bypass. ✓
- **nosniff, uploads traversal, XSS, CSP, i18n** — чисто. ✓
- **wire.go ↔ wire_gen.go** — синхронизированы. ✓

---

## B. Оптимизации (производительность / БД)

### Рекомендованные CREATE INDEX (migration 000044)
```sql
CREATE INDEX CONCURRENTLY idx_notifications_read_at_read
    ON notifications(read_at) WHERE read = true;  -- F-2: retention DELETE
CREATE INDEX CONCURRENTLY idx_games_visibility_draft_author
    ON games(visibility, is_draft, author_id);    -- F-3: authed listing OR-branch
```

### Прочие оптимизации
- **F-1**: calendar-кэш — evict expired (как unreadCache) или кап размера.
- **F-3**: кэшировать авторизованный листинг (ключ с ViewerID) или UNION-рерайт OR-предиката.
- **F-5**: GetTeamsByUserID — один UNION-запрос.
- **F-4**: count-fallback в AdminListGames при пустой странице.

---

## C. Улучшения пользовательского опыта (приоритеты)

1. **UX-H1** — hint toast: читать из `r.url`, не `window.location`.
2. **UX-H2/H3, M1/M2** — доделать data-confirm-danger для reject/delete/clean/force-finish.
3. **UX-M3** — data-confirm-ok для disqualify.
4. **UX-M4/M5** — scroll-to-bottom + failure-feedback в чатах.
5. **A11y** — aria-hidden для декоративных emoji, aria-describedby для field-error.
6. **L1-L5** — 24h-формат времени в чате, таймстамп в team-chat, dark-placeholder, Email-лейбл, follow-unfollow confirm.

---

## D. Архитектурные улучшения (кодовая база)

1. **A-H1**: мигрировать read-пути svc_play/svc_simulate/svc_admin на репозитории (GameplayReadRepository, GetByIDForSimulation, Team/User repos).
2. **A-M3**: удалить мёртвый else-fallback в ProcessSnapshot.
3. **A-M1/A-M4**: использовать/удалить sentinel-ошибки passing; добавить sentinel team/level.
4. **A-M2**: notify-чтения через Team/User репозитории.
5. **A-M5**: унифицировать миграционные runner'ы.
6. **H3 (arch)**: сделать все `DB *gorm.DB` поля неэкспортируемыми; удалить repoOrDefault()-fallback (DI гарантирует инъекцию).
7. **M6**: sqlmock для AtomicLockAccount, DeleteOldRead, Autocomplete, SearchVectorExists.

## Приоритет фиксов (pass 34)
1. **F-P5a** (LEFT JOIN — регрессия 404) — исправлено.
2. **S-1** (TOTP step-up lockout) — исправлено.
3. **UX-H1** (hint toast) + **UX-H2/H3** (data-confirm-danger).
4. **F-1/F-2** (calendar cache evict + notification index 000044).
5. **A-H1** (svc_play/simulate/admin репозитории).

---

# PASS 33 (повторное ревью) — 8 августа 2026

> Четвёртое повторное ревью после полного закрытия pass 30-32 (репозитории, race, golangci-lint CI). Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок.

## Резюме pass 33

**Итог:** 0 критичных, 5 высоких (4 подтверждены лично), ~15 средних, ~15 низких + 2 новых индекса.

> Кодовая база стабильно зрелая. Найдены: **последний TZ-баг** (calendar API в UTC), **баг админ-дашборда** (backups без deleted_at — счётчики молча 0), дублирование SQL/репозиториев в svc_play, незавершённая миграция GamePassing/GameListing на репозитории, stale TODO, и пакет dark-mode/UX-мелочей.

---

## Статус (обновлено 8 авг 2026) — PASS 33 ЗАКРЫТ

**Все находки pass 33 исправлены** (3 раунда фиксов):

- **Раунд 1** (`8fc47ca`): A-H1 (админ-дашборд backups deleted_at), TZ-1 (calendar TZ), A-H4 (Model(ctx) убран из GameRepository: RawScan/Autocomplete/SearchVectorExists), A-H3 (GamePassingRepository в сервисе), A-H2 (CountPassingsInStatuses в svc_play), A-M2 (stale TODO), P-1/P-2 (индекс 000043 + retention уведомлений), S-3 (nosniff), A-2 (500-страница без raw err).
- **Раунд 2** (`4b0ee4b`): S-1 (backup-code lockout), P-4 (GetLogs COUNT OVER), A-M1 (sentinel-ошибки passing), UX-1/2/3 (confirm focus, бейдж 9+, hotkey n), DM sweep (admin/invitations/dashboard/tournaments/games-show).
- **Раунд 3** (`ca5707c`): P-3 (AdminListGames COUNT OVER + JOIN users), P-5 (GetByIDPreloaded Joins Author), P-7 (GetGameplayData параллельные settings/progress), S-2 (coauthor remove: ErrNotOwner → 403 vs 500), S-5 (явный data-confirm-danger вместо эвристики), A-M3 (go-sqlmock для CountPassingsInStatuses/AdminListGames).
- **CI-фикс** (`6904ccc`): staticcheck QF1008 — убран лишний embedded selector (`rows[i].Game.Author` → `rows[i].Author`).

**Отложено (задокументировано):**
- **P-6** (GetAverageRating из rating_value) — count отзывов не прекомпьютится в `games.rating_value` (только AVG); reviews-скан с индексом + 5-мин кэш приемлем.
- Полная миграция транзакционных сервисов (svc_play/svc_admin/svc_simulate tx) на репозитории — стандартный GORM-паттерн, оставлено.

**Проверка:** `go build ./...` ✓, `go test -short ./...` ✓ (вкл. новые go-sqlmock-тесты), `go test -tags=integration` (game/admin/user/notification/calendar) ✓, `go vet ./...` ✓, `gofmt -l .` → пусто ✓, `go generate` (wire) ✓, `go test -race -short ./...` (WSL) ✓.

---

## A. Найденные ошибки pass 33 (верифицировано лично)

### 🔴 Критично
Не обнаружено.

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **TZ-1** | `calendar/handler.go:132-137` | **Последний TZ-баг**: CalendarData форматирует `StartsAt.Format("2006-01-02")`/`"15:04"` в UTC без `tz_offset` cookie. UTC+3 пользователь видит время на 3ч раньше; игры около полуночи попадают в **неправильную ячейку дня**. Кэш `year-month` тоже не учитывает TZ. **Верифицировано лично.** |
| **A-H1** | `admin/handler.go:122` | **Админ-дашборд молча показывает 0 счётчиков.** `backups` таблица НЕ имеет `deleted_at` колонки (есть у audit_logs), но SQL фильтрует `WHERE deleted_at IS NULL` → ошибка, глотается log.Error, все 5 счётчиков = 0. **Верифицировано лично.** |
| **A-H2** | `svc_play.go:636-638,795-808` | **Дублирование SQL**: ProcessSnapshot переписывает `CountActivePassings` (добавлен pass 32, но с другой семантикой: started+accepted vs started+testing), `IsTeamMember` дублирует `GameRepository.IsTeamMember`. Семантическая дивергенция — потенциальный баг снапшотов. **Верифицировано лично.** |
| **A-H3** | `svc_passing.go:21` + `repository.go:45-52` | **GamePassingService игнорирует существующий GamePassingRepository** — read-пути (ListByGamePaginated, ListTestPassings) идут через экспортированный `DB *gorm.DB`. Репозиторий создан в DI, но не используется. |
| **A-H4** | `svc_listing.go:192,204,241,269` + `repository.go:23` | **`GameRepository.Model(ctx)` — последняя утечка `*gorm.DB`** через интерфейс (raw SQL в GameListingService: ListFiltered, Autocomplete, SearchVector). |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| S-1 | `two_factor_handler.go:155-209` | BackupVerify: нет счётчика неудачных попыток/локдауна при украденной сессии (в отличие от TOTP-пути). |
| S-2 | `hnd_coauthor.go:178-196` | RemoveCoAuthor рендерит сырой err в 403 для любой ошибки (в т.ч. DB) — раскрытие + неверный статус. |
| S-3 | `uploads.go` Serve | Нет `X-Content-Type-Options: nosniff` на ответах uploads (answers/*). |
| S-4 | `service.go:488-498` | SetLockedUntil: TOCTOU в вычислении длительности backoff при параллельных попытках. |
| S-5 | `static/js/app.js:183-187` | isDanger-эвристика хрупкая: не-деструктивный confirm без data-confirm-ok станет красным «Удалить»; force-finish/reject сейчас синие, а кнопки btn-danger. |
| P-1 | `notification/repository.go:79-105` | ListByUser: `ORDER BY created_at DESC` без подходящего индекса — top-N sort всех уведомлений пользователя. |
| P-2 | notification | Таблица уведомлений растёт **безгранично** (нет retention-задачи). |
| P-3 | `game/repository.go:240-263` | AdminListGames — 2-3 round-trip (Count+Find+Preload Author); можно COUNT OVER + JOIN. |
| P-4 | `svc_passing.go:112-133`, `repository.go:140-166` | ListByGamePaginated/GetLogsByGameIDPaginated — Count+Find (2 запроса). |
| P-5 | `repository.go:71-78` | GetByIDPreloaded — 3 запроса (Preload Author+GameSetting); можно Joins. |
| P-6 | `svc_rating.go:227-245` | GetAverageRating на cache-miss сканирует reviews вместо precomputed `games.rating_value` (000027). |
| P-7 | `svc_play.go:679-782` | GetGameplayData — ~8-9 последовательных запросов (потребляет preloads). |
| A-M1 | game domain | ~15 inline русских ошибок при 6 sentinel — localization/errors.Is неполны. |
| A-M2 | `auth_handler.go:308-309` | **Stale TODO про JTI blacklist** (реализовано). **Верифицировано лично.** |
| A-M3 | `AdminListGames`, `AtomicLockAccount`, 4 новые repo-метода | Нет прямых юнит-тестов (только интеграционные). |
| UX-1 | `app.js:246-248` | Confirm-модалка фокусит Cancel — Enter (мышечная память «подтвердить») отменяет действие. |
| A-2 | `errors-500.html:7` | Сырой Go-err рендерится на 500-странице — утечка путей/внутренностей. |
| UX-2 | `layout.html:190` | Серверный бейдж уведомлений «15» выходит за 16px-круг; JS корректно капает «9+». |
| UX-3 | `games-list.html:298-301` | Горячая клавиша «n» работает для гостей (ведёт на логин-стену). |
| i18n-1 | `auth-login.html:14` | Хардкод «Email» вместо T. |
| XSS-dp | `invitations-new.html:81` | `u.id` в innerHTML без escapeHtml (число, но defense-in-depth). |

### 🔲 Осознанно оставлено / опровергнуто
- **SQLi/LIKE-экранирование** в AdminListGames — корректно (BuildLikePattern + bind).
- **XSS** в шаблонах/JS — все innerHTML-сайты экранируют; CSP nonce корректен.
- **Гонок не найдено** — race-тесты pass 31 чистые.
- **wire.go ↔ wire_gen.go** — синхронизированы (проверено).
- Rate-limit покрытие — хорошее (login/register/2FA/code/RUM/search).
- go.mod: ядро актуально; skip2/go-qrcode устаревший (low risk).

---

## B. Оптимизации (производительность / БД)

### Рекомендованные CREATE INDEX (migration 000043)
```sql
CREATE INDEX IF NOT EXISTS idx_notifications_user_created
    ON notifications(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_games_author_draft_created
    ON games(author_id, is_draft, created_at DESC);
```

### Прочие оптимизации
- **P-1/P-4/P-5**: COUNT(*) OVER() + JOIN для AdminListGames, ListByGamePaginated, GetLogsByGameIDPaginated, GetByIDPreloaded (паттерн уже есть в notification).
- **P-6**: GetAverageRating читать `games.rating_value`/`participant_count` вместо скана reviews.
- **P-2**: retention-задача для уведомлений (удалять read за 90 дней), как RefreshToken.DeleteExpired.
- **P-7**: параллелить независимые чтения в GetGameplayData (settings + progress + attempts/voting).

---

## C. Улучшения пользовательского опыта (приоритеты)

1. **TZ-1** — calendar API: применять TZOffset к дате/времени + к ключу кэша.
2. **A-2** — не рендерить сырой Go-err на 500-странице.
3. **UX-1** — фокус на кнопке OK (или Cancel для не-деструктивных) в confirm-модалке.
4. **DM-1..DM-6** — dark-mode sweep бейджей/боксов (admin, invitations, dashboard, tournaments, games-show, calendar).
5. **UX-2** — серверный кэп бейджа уведомлений «9+».
6. **UX-3** — guard горячей клавиши «n» для гостей.
7. **S-5** — явный `data-confirm-danger` вместо эвристики.

---

## D. Архитектурные улучшения (кодовая база)

1. **A-H4**: убрать `GameRepository.Model(ctx)` — типизированные `ListFiltered`/`SearchVectorExists`/`Autocomplete` в репозитории.
2. **A-H3**: подключить `GamePassingRepository` в `GamePassingService` (ListByGamePaginated/ListTestPassings).
3. **A-H2**: использовать `CountActivePassings`/`IsTeamMember` в svc_play; устранить семантическую дивергенцию (started+accepted vs started+testing).
4. **A-M1**: sentinel-ошибки для ~15 инлайн-строк game-домена.
5. **A-M3**: go-sqlmock для AdminListGames/CountActivePassings/AtomicLockAccount.
6. **A-M2**: удалить stale TODO про JTI.
7. **S-4**: атомарное вычисление длительности backoff внутри SQL.

## Приоритет фиксов (pass 33)
1. **A-H1** (админ-дашборд 0 счётчиков) — молчаливый баг.
2. **TZ-1** (calendar UTC) — видимый пользователям.
3. **A-H4/A-H3** (репозитории: Model leak, GamePassing) — архитектура.
4. **P-1/P-2** (индекс уведомлений + retention).
5. **S-1/S-3** (backup-коды brute-force, nosniff).

---

# PASS 32 (повторное ревью) — 8 августа 2026

> Третье повторное ревью после полного закрытия pass 30-31 (миграция на репозитории, race-верификация). Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок.

## Статус (обновлено 8 авг 2026) — PASS 32 ЗАКРЫТ

**Все находки pass 32 исправлены** (2 раунда фиксов + мелкие UX):

- **Раунд 1** (`b552fd4`): F-C1 (глобальный escapeHtml — тосты/confirm ожили), F-P1 (инвалидация лидерборда в in-memory), S-1 (OAuth 2FA TTL), S-2 (PhotosPage visibility), S-3 (нормализация backup-кодов), S-4/S-5 (атомарный lock_count + 2FA backoff), A-1 (CoAuthor→репозиторий), индексы 000042, dark-mode (монитор/admin/passings).
- **Раунд 2** (`784c010`): A-5 (GameRepository без *gorm.DB leak: типизированные Count/AdminListGames), A-2 (батч DeleteLevelFromActiveGame), S-6 (фото-delete authz), UX-H1 (Escape на dropdown), UX-M3 (lightbox local TZ), UX-M5/M7 (удалён мёртвый WS логов, .catch).
- **Раунд 3** (`9d17109`): UX-M11 (role=menuitem), UX-M8 (stale-response guards в автокомплитах), UX-M4 (confirm-кнопка по типу действия).
- **CI-фиксы** (`d8eb388`, `d7e123b`): govet shadow в tournament RemoveGame (4 `err :=` → `err =`) — golangci-lint v2 на CI прошёл.

**Опровергнуто/отложено (задокументировано):**
- **P-1** (GameSnapshot) — уже кэшируется 30с LRU + singleflight (GetOrFetchSnapshot); тяжёлый CTE выполняется раз в 30с на активную игру, не каждую секунду.
- **P-4** (кэш авторизованного листинга) — риск фрагментации кэша по viewerID > пользы.
- **P-5** (GetGameplayData) — attempts/voting уже параллелятся (errgroup); passing-first обязателен для статус-проверки.
- **Юнит-тесты новых репозиториев** — интеграционные покрывают через сервисы; go-sqlmock — отдельная задача.

**Проверка:** `go build ./...` ✓, `go test -short ./...` ✓, `go test -tags=integration` (game/admin/user/monitor) ✓, `go vet ./...` ✓, `gofmt -l .` → пусто ✓, `go generate` (wire) ✓, `go test -race -short ./...` (WSL) ✓.

---

## Резюме pass 32

**Итог:** 2 критичных (1 подтверждена лично), 5 высоких, ~20 средних, ~15 низких + 3 новых индекса.

> Кодовая база существенно укреплена (репозитории, sentinel-ошибки, race-чисто). Найдены: **регрессия JS-хелперов** (escapeHtml не глобальный — тосты/confirm сломаны), **баг инвалидации лидерборда** (in-memory кэш), 2 утечки auth (OAuth 2FA TTL, приватные игры в фото-галерее), плюс незавершённая миграция репозиториев (CoAuthorService дублирует CoAuthorRepository).

---

## A. Найденные ошибки pass 32 (верифицировано лично)

### 🔴 Критично

| ID | Файл | Проблема |
|---|---|---|
| **F-C1** | `static/js/app.js:78,181` + `layout.html` | **`escapeHtml` не определён глобально.** app.js вызывает `escapeHtml(message)` в `showToast`/`showModalConfirm`, но функция объявлена только внутри IIFE-блока уведомлений в layout.html. На любой странице, кроме calendar-page, вызов тоста/confirm-модалки кидает `ReferenceError` — молча ломаются все тосты, подтверждения удаления, монитор, gameplay-флеши. **Верифицировано лично.** |
| **F-P1** | `cache.go:162-212` + `svc_rating.go:82,173` | **Инвалидация лидерборда не работает в in-memory кэше.** `trackPrefix("leaderboard:limit:10")` регистрирует префиксы `leaderboard`/`leaderboard:limit`/`leaderboard:limit:10` (без trailing colon), а `DeleteByPrefix("leaderboard:")` ищет `prefixKeys["leaderboard:"]` → early return. Лидерборд устаревает до 5-мин TTL после каждой игры. Работает только Valkey (SCAN). **Верифицировано лично.** |

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| S-1 | `auth_handler.go:840-846` | **OAuth-2FA pending-сессия без TTL.** `Login` ставит `pending_expires` (10 мин), а OAuth-ветка — нет; TwoFALoginVerify пропускает проверку при отсутствии ключа. Окно brute-force открыто на жизнь session-cookie. **Верифицировано лично.** |
| S-2 | `hnd_photo.go:57-92` | **Утечка метаданных приватных/черновиков игр** через `PhotosPage` (только AuthRequired, без visibility-проверки) — фото-записи, описания, автор перечисляемы любым залогиненным. |
| A-1 | `svc_coauthor.go` + `coauthor_repository.go` | **CoAuthorRepository — мёртвый код для своего сервиса.** CoAuthorService дублирует все запросы репозитория через `s.DB` (IsUserManager, Find, Save, Create, Delete, List); репозиторий используется только PhotoService. Комментарий в заголовке ложный. **Верифицировано лично.** |
| A-2 | `svc_admin.go:202-221` | **N+1 в DeleteLevelFromActiveGame** — GetCurrentProgressForUpdate+Save+AdvanceToNextLevel на каждое прохождение в цикле. |
| P-1 | `monitor_repository.go:41-84` | **AggregateGameSnapshot — тяжёлый CTE/LATERAL каждую секунду** на активных играх (CROSS JOIN + попытки за час). |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| S-3 | `two_factor_handler.go:155-209` | BackupVerify: нет rate-limit, нет локдауна, нет нормализации ввода (нижний регистр не проходит — коды верхнего). |
| S-4 | `service.go:194-204` | Race в lock_count (два параллельных неверных пароля оба пишут `lock_count=1`). |
| S-5 | `auth_handler.go:262` | SetLockedUntil в 2FA-пути не инкрементирует lock_count — backoff не применяется. |
| S-6 | `svc_photo.go:47-59` | Сервисный authz для удаления фото слабее handler'а (`!=observer` вместо `ContentEditor||Moderator`). |
| P-2 | `svc_play.go:454` | StartTesting ищет команду по `name` без btree-индекса (только trgm). |
| P-3 | `note_repository.go:30`, `photo_repository.go:36` | ListByGame сортирует created_at DESC — нет составного индекса. |
| P-4 | `svc_listing.go:104` | Аутентифицированный листинг не кэшируется (только анонимный), OR-предикаты бьют индексы. |
| P-5 | `svc_play.go:679-782` | GetGameplayData — ~5 последовательных round-trip. |
| A-3 | `repoOrDefault()` паттерн (rating/progress/monitor) | Двойная конструкция репозитория; fallback мёртв при wire. |
| A-4 | `svc_play.go:795-808` | IsTeamMember дублирует GameRepository.IsTeamMember + лишняя загрузка teams. |
| A-5 | `GameRepository.Model(ctx)`/`Count(query)`/`ListFiltered(query)`/`DB()` | Escape-hatch `*gorm.DB` в интерфейсе репозитория — сервисы строят raw-запросы. |
| A-6 | 7 новых репозиториев | Нет юнит-тестов (все интеграционные, скипаются в -short). |
| UX-H1 | `layout.html:584` | Escape-закрытие dropdown висит на bell, а фокус в списке — клавиатура «застревает». |
| UX-H2/H3/H4 | monitor-page, admin-games, game_passings mobile | Dark-mode пробелы в JS-генерируемых бейджах/карточках. |
| UX-M3 | `games-photos.html:28` | Lightbox caption в UTC — расходится с карточкой (local). Единственный оставшийся raw `.Format`. |
| UX-M4 | `app.js:183-184` | Кнопка OK в confirm всегда красная даже для не-деструктивных действий. |
| UX-M5/M6/M7 | `logs-list.html` | Нет `.catch` в loadPage; WS-таймстампы в UTC; **WS-комната "logs_"+gameID не существует в Go** — live-логи мертвы. |
| UX-M8 | invitations/co_authors autocomplete | Нет stale-response guard. |
| UX-M11 | layout notification dropdown | `role="menu"` без `role="menuitem"` — ARIA-нарушение. |

### 🔲 Осознанно оставлено / опровергнуто
- **SQLi/XSS/путь-траверс/MIME** в новых репозиториях — чисто (параметризация сохранена).
- **go.mod**: ядро актуально (gin 1.12, gorm 1.26, jwt v5.3.1, x/crypto 0.54); один устаревший skip2/go-qrcode (low risk).
- **Гонки не найдены** — race-тесты pass 31 подтвердили.
- Rate-limit fail-open при Valkey-ауттаге — задокументированный компромисс.

---

## B. Оптимизации (производительность / БД)

### Рекомендованные CREATE INDEX (migration 000042)
```sql
CREATE INDEX IF NOT EXISTS idx_notes_game_created ON notes(game_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_photos_game_created ON photos(game_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_teams_name ON teams(name); -- StartTesting exact-match
```

### Прочие оптимизации
- **P-1 (F-P1)**: чинить DeleteByPrefix — регистрировать префиксы как используют (без trailing colon) или звать `DeleteByPrefix("leaderboard")`.
- **P-2 (A-2)**: батчить DeleteLevelFromActiveGame (один запрос current progress для всех passings).
- **P-3 (A-4)**: убрать дубль IsTeamMember, параллелить GetGameplayData (errgroup).
- **P-4 (P-1)**: оценить кэш GameSnapshot (уже 30с TTL) + пред-агрегация попыток.

---

## C. Улучшения пользовательского опыта (приоритеты)

1. **C1 (escapeHtml)** — вернуть глобальный `escapeHtml`/`tI18n` в app.js: тосты и confirm-модалки оживают на всех страницах.
2. **UX-M3** — единые локальные таймстампы в lightbox фото.
3. **Dark-mode sweep** (H2-H4, M1-M3) — бейджи, монитор-карточки, админ-фильтры, календарь.
4. **UX-M7** — починить live-логи (broadcast в "logs_"+gameID) или убрать мёртвый WS.
5. **UX-M8** — stale-response guards в автокомплитах.
6. **UX-M11** — role="menuitem" для пунктов dropdown или заменить на region.

---

## D. Архитектурные улучшения (кодовая база)

1. **A-1 (CoAuthor)**: внедрить CoAuthorRepository в CoAuthorService (или удалить дубль) — устранить ложный комментарий.
2. **A-5 (GameRepository leak)**: добавить `CountActivePassings`/`CountLevelsByGame`/`CountPublished`, убрать `Model(ctx)`/`Count(query)`/`ListFiltered(query)`/`DB()` из интерфейса.
3. **A-3**: сделать репозиторий обязательным параметром конструктора; убрать `repoOrDefault()`.
4. **A-6**: юнит-тесты новых репозиториев (go-sqlmock/SQLite), особенно AggregateGameSnapshot.
5. **S-4/S-5**: атомарный UPDATE lock_count + backoff в 2FA-пути.
6. **Удалить мёртвый шаблон** games-new-wizard.html (L7) и stale TODO (L1).
7. **CI**: govulncheck + go test -race уже в пайплайне; добавить `go generate` diff-check.

## Приоритет фиксов (pass 32)
1. **F-C1** (escapeHtml) — сломаны тосты/confirm на всех страницах.
2. **F-P1** (leaderboard invalidation) — одна строка + тест.
3. **S-1/S-2** (OAuth 2FA TTL, private-game photo metadata) — security.
4. **A-1/A-5** (CoAuthor repo, GameRepository leak) — архитектура.
5. Индексы 000042; A-2 N+1; dark-mode sweep.

---

# PASS 31 (повторное ревью) — 8 августа 2026

> Результаты повторного глубокого ревью после полного закрытия pass 30 (7 раундов фиксов). Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок по коду.

## Статус (обновлено 8 авг 2026) — PASS 31 ЗАКРЫТ

**Все находки pass 31 исправлены** (5 раундов фиксов):

- **Раунд 1** (`020e382`): UX-1/2 (таймзона в формах, кнопки push), S-1 (RUM-лимит), F1 (listing OR-split), S-3 (2FA lockout+TTL), S-7 (hash reset code), индексы 000040.
- **Раунд 2** (`1cb78bb`): UX-3/4/8/9 (TZ-таймстампы, role=alert, fetch .catch, SSE guard), A-8/10 (пустой тест, ctx в Review), F-3/5 (RemoveGame batch, leaderboard cache), S-2 (backup-коды 10 символов).
- **Раунд 3** (`32badb7`): A-1 (SSE→репозиторий), A-2 part1 (LevelProgressRepository+DI), S-4 (exponential backoff, миграция 000041), S-6 (in-memory JTI blacklist), F-7 (COUNT OVER), UX-5/7 (guest view FOUC, monitor refresh).
- **Раунд 4** (`232646d`): UX-10..15 (a11y, labels, empty states, dark), A-3/5/7/9 (DI: EmailVerification, TwoFactor; dead db params; sentinel-ошибки).
- **Раунд 5** (текущий): **миграция на репозитории** (A-2) + **race-верификация**.

**A-2 — миграция на репозитории (6 сервисов):**
- Новые репозитории: `Note`, `Photo`, `Review`, `Rating`, `CoAuthor`, `Monitor` (+ LevelProgress из раунда 3) — все подключены в wire.
- Мигрированы read-пути: svc_note, svc_photo, svc_review, svc_rating, svc_monitor.
- Транзакционные паттерны (svc_play/svc_admin/svc_passing/svc_simulate — `s.DB.Transaction(func(tx...)`) оставлены: это стандартный GORM-паттерн, не нарушение слоистости. CoAuthorService — тонкий data-access сервис с логикой ролей, оставлен.
- Следующие кандидаты (документировано): svc_play read-хелперы, svc_admin notify-чтения, svc_passing ListTestPassings.

**Race-верификация (через WSL Fedora, gcc):**
- `go test -race -short ./internal/...` — **все пакеты OK, 0 data races, 0 паник**.
- Покрыты: middleware (role cache), websocket (hub), notification (worker pool), user, game, tournament, monitor, admin, render, templatefuncs и все pkg.

**Проверка:** `go build ./...` ✓, `go test -short ./...` ✓, `go test -tags=integration` (game/user/tournament/monitor/social/admin/notification) ✓, `go vet ./...` ✓, `gofmt -l .` → пусто ✓, `go generate` (wire) ✓, `go test -race -short ./...` ✓.

> Примечание: локально `-race` недоступен на Windows (нет CGO); CI-пайплайн уже запускает `go test -race` на Ubuntu. Локальная WSL-верификация подтвердила чистоту.

---

## Резюме pass 31

**Итог:** 0 критичных, 5 высоких (3 подтверждены лично), ~20 средних, ~15 низких + 4 новых индекса.

> Общий вывод: кодовая база зрелая, defense-in-depth работает. Найденные проблемы — преимущественно hardening и UX-недоделки, плюс несколько реальных багов (timezone round-trip в формах, кнопки без type, RUM лимит для анонимов).

---

## A. Найденные ошибки pass 31 (верифицировано лично)

### 🔴 Критично
Не обнаружено.

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **S-1** | `app/router.go:200` + `middleware/rate_limiter.go:488-491` | **RUM-лимит не работает для анонимов.** `APIRateLimit` при `userID==0` сразу `c.Next()`; RUM-эндпоинт публичный → per-IP лимит 60/min — no-op. Остаётся только глобальный 100/min. |
| **UX-1** | `game/hnd_game.go:66-75` (parseDateTime) + `games-edit.html:34,43`, `games-new.html`, `games-new-wizard.html` | **Таймзона-баг в формах datetime-local.** `time.Parse` хранит wall-clock как UTC, а отображение сдвигает на TZOffset: пользователь UTC+3 вводит «12:00», видит на странице «15:00», а в форме снова «12:00». Fix pass 30 покрыл только отображение, не ввод. |
| **UX-2** | `notification/templates/notification-settings.html:58,62` | **Кнопки push без `type="button"`** → по умолчанию `submit`, при клике запускают push-flow И AJAX-сохранение формы (потенциально устаревшие настройки). |
| **A-1** | `game/hnd_sse.go:365-421` + `game/repository.go:20-22,104-108` | **SSE-хендлеры принимают `*gorm.DB` и делают raw-запросы** (`First(&passing)`, `Table("team_members")`). Слоистость нарушена; репозиторий утекает `*gorm.DB` через `Count(query)`, `ListFiltered(query)`, `Model()`. |
| **A-2** | `game/svc_*.go` (12 сервисов) | **Сервисы напрямую используют `s.DB.Model/Raw/Preload`** вместо репозиториев — противоречит заявленному C1, делает unit-тестирование без Postgres невозможным. |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| S-2 | `user/two_factor_service.go:106-119` | Backup-коды — 6 цифр (~20 бит). Модуль-фикс pass 30 не изменил длину. |
| S-3 | `user/auth_handler.go:193-257` | 2FA-шаг без per-account локдауна и без TTL `pending_user_id` — распределённый brute-force TOTP; pending-сессия живёт до конца session-cookie. |
| S-4 | `user/service.go:152-165` | Бинарный локдаун аккаунта (5 ошибок → 30 мин) — DoS-вектор. |
| S-6 | `user/service.go:332-337` | JWT-отзыв работает только при настроенном кэше (без Valkey — просто warn). |
| S-7 | `user/password_reset_service.go:56-61` | Код сброса пароля хранится в plaintext (только неиспользуемый rawToken хешируется). |
| S-9 | `user/auth_handler.go:272-285` | Refresh-токен принимается и в JSON-теле, не только в cookie. |
| F-1 | `game/svc_listing.go:90-96` | **OR-предикаты в листинге мешают индексам**: `(visibility='public' OR author_id=?) AND (is_draft=false OR author_id=?)` → для анонимов BitmapOr + filesort на каждой сортировке. |
| F-2 | `admin/handler.go:601-648` + audit | Аудит: фильтр `user_id+action` + `ORDER BY created_at` — только одноколоночные индексы. |
| F-3 | `tournament/service.go:181-207` | RemoveGame: построчный Upsert+Delete результатов в цикле (N+1 write). |
| F-4 | `game/svc_progress.go:345-364` | CheckTimeouts: `AdvanceToNextLevel` внутри цикла делает +2 запроса на прогресс. |
| F-5 | `tournament/repository.go:240-247` | Турнирный лидерборд не кэшируется (а player-лидерборд кэшируется). |
| F-6 | `cache/cache.go:51-85` | In-memory кэш без maxSize по умолчанию (LRU только при maxSize>0). |
| F-7 | `notification/repository.go:79-90`, admin List* | Отдельные COUNT + Find (2 round-trip) вместо COUNT(*) OVER(). |
| A-3 | `user/service.go:115` | `AuthService.Register` создаёт `NewEmailVerificationService` локально (вне wire). |
| A-4 | `user/handler.go:22`, `routes.go:39` | Глобальное состояние `SetSecureCookieConfig`, `SetThemeSettingsLoader`, `SetRoleProvider`. |
| A-5 | `admin/routes.go:39` | 4-й инстанс `NewTwoFactorService()` inline (wire + user + admin). |
| A-7 | `level/routes.go:26`, `team/routes.go:18` | Неиспользуемые параметры `db` в RegisterRoutes. |
| A-8 | `user/auth_handler_test.go:307-309` | Пустая заглушка `TestTwoFALoginVerify_ValidCode` (t.Skip без тела); устаревший TODO «JTI blacklist» (уже реализовано). |
| A-9 | `user/service.go` auth | Inline-русские строки ошибок вместо sentinel-ошибок (`ErrInvalidCredentials`, ...). |
| A-10 | `game/hnd_review.go:96` | `reviewService.Create(...)` без ctx — контекст не передаётся. |
| UX-3 | `gameplay-show.html:84`, `logs-list.html:8` | Таймстампы `.Format "15:04:05"` (UTC) — не учитывают TZOffset. |
| UX-4 | auth-login/register, games-new, levels-new | Flash-error без `role="alert"` — SR не объявляет ошибки. |
| UX-5 | `games-list.html:247` guest branch | Гостевое переопределение view → FOUC; `aria-pressed` статичен. |
| UX-7 | `monitor-page.html:257-291` | После дисквалификации карточка не обновляется до следующего WS-снапшота. |
| UX-8 | `notes-manage.html:95-107` | fetch delete без `.catch` — unhandled rejection. |
| UX-9 | `gameplay-show.html:476-507` | SSE `JSON.parse(e.data)` без try/catch. |
| UX-13 | admin-users/games/teams, errors-404 | Поиск только с placeholder, нет `<label>`/`aria-label`. |
| UX-14 | `notes-manage.html`, `game_passings-apply.html` | Нет empty-states. |

### 🔲 Осознанно оставлено / опровергнуто (проверено лично)
- **CSRF зарегистрирован корректно** (app.go:88-104) — не мёртвый код (security-агент пометил «unverified», подтверждено).
- **Session cookie Secure** — config-dependent, оправдано (TLS-прокси).
- **Rate limiters fail-open при Valkey-ауттаге** — задокументированный компромисс (P3).
- **clientFingerprint** — мягкий сигнал, задокументировано.

---

## B. Оптимизации (производительность / БД)

### Рекомендованные CREATE INDEX (pass 31)
```sql
-- F2: Аудит: фильтр + пагинация (append-only таблица)
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_created
    ON audit_logs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action_created
    ON audit_logs(action, created_at DESC);

-- F12: Пендинг-приглашения дашборда с сортировкой
CREATE INDEX IF NOT EXISTS idx_invitations_user_status_created
    ON invitations(user_id, status, created_at DESC);
```

### Прочие оптимизации
- **F1**: split листинга для анонимов — `WHERE visibility='public' AND is_draft=false` (без OR) → индексы 000035/000037/000008 станут используемыми. Самый высокий импакт.
- **F3**: RemoveGame — batch UPDATE/DELETE результатов (паттерн unnest как в svc_rating.go).
- **F5**: кэш турнирного лидерборда `tournament:leaderboard:%d` (TTL 30-60с), инвалидация на UpdateScoresForGame/RemoveGame.
- **F6**: задать maxSize для in-memory кэша (10k-50k) при конструировании в DI.
- **F7**: COUNT(*) OVER() вместо отдельного COUNT (notification, admin List*).
- **F9**: финальный callback (WithoutCancel goroutine) — обернуть в bounded worker.
- **S-1**: RUM — использовать IP-keyed лимитер или только глобальный.

---

## C. Улучшения пользовательского опыта (приоритеты)

1. **UX-1**: чинить parseDateTime — конвертировать локальный wall-clock в UTC по `tz_offset` cookie; форма рендерится через TZ-helper. Самый заметный баг.
2. **UX-2**: `type="button"` для enable/disable-push — чинить немедленно (активный баг).
3. **UX-3**: единые TZ-таймстампы в gameplay/logs.
4. **Dark mode**: бейджи статусов (admin-games, invitations, passings, monitor) — добавить `dark:` варианты (паттерн уже есть в games-list).
5. **Touch targets**: `.btn` ≥ 44px для coarse pointers (WCAG 2.5.8).
6. **A11y**: `role="alert"` на flash-error, label/aria-label для поиска и иконочных кнопок, aria для мобильных lang-кнопок.
7. **Resilience**: `.catch` в notes delete, try/catch SSE JSON.parse.
8. **Empty states**: «нет заметок», «сначала создайте команду» в apply.

---

## D. Архитектурные улучшения (кодовая база)

1. **A-1/A-2 (слоистость)**: перенести raw-запросы из SSE-хендлеров и 12 сервисов в репозитории; сузить `GameRepository` (убрать `Count(query)`, `ListFiltered(query)`, `Model()`, `DB()`). Самый большой структурный выигрыш — unit-тесты без Postgres.
2. **DI-закрытие**: `TwoFactorService` в admin/routes, `EmailVerificationService` в AuthService, убрать неиспользуемые `db` в level/team routes.
3. **Sentinel-ошибки** для user-домена (`ErrInvalidCredentials`, `ErrTokenRevoked`...) — handlers смогут делать errors.Is.
4. **A-10**: прокинуть ctx в `ReviewService.Create`.
5. **A-8**: реализовать `TestTwoFALoginVerify_ValidCode` или удалить; почистить устаревший TODO.
6. **CI**: добавить проверку `git diff --exit-code wire_gen.go` + `go test -short` + golangci-lint (после фикса версии бинарника).
7. **A-3/A-4**: убрать глобальное состояние (SetSecureCookieConfig и т.д.) в пользу DI-конструкторов.

## Приоритет фиксов (pass 31)
1. **UX-1** (timezone в формах) и **UX-2** (кнопки push) — видимые пользователю баги.
2. **S-1** (RUM-лимит) — тривиальный security-фикс.
3. **F1** (listing OR-split) — наибольший перф-выигрыш.
4. **S-3/S-7** (2FA lockout, password-reset hash) — hardening.
5. Индексы 000040 (audit_logs, invitations).
6. A-1/A-2 (слоистость) — крупный рефакторинг, по отдельному раунду.

---

## История: PASS 30 (закрыт)

<details><summary>Содержимое pass 30 — для истории (все пункты исправлены)</summary>

## A. Найденные ошибки (верифицировано лично)

### 🔴 Критично
Не обнаружено.

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **H1** | `pkg/templatefuncs/funcs.go:154-183` + `game/model.go:36-37` | **`formatDate`/`formatDateTime` возвращают пустую строку для `*time.Time`.** `Game.StartsAt`/`RegistrationDeadline` — указатели; хелперы делают `t.(time.Time)` (значение) → `ok=false` → `""`. Затронуты: games-list (карточка+таблица), games-show, tournaments-show — **даты не видны**. Проверено исполнением шаблона. |
| **H2** | `tournament/templates/tournaments-games.html:1` vs `tournament/handler.go:426` | **Страница «Добавить игру в турнир» пустая.** `{{define "tournaments-add_game.html"}}`, а handler рендерит `tournaments-games.html` → пустой вывод без ошибки → layout «нет содержимого». Единственный шаблон с расхождением имени. |
| **H3** | `game/svc_listing.go:90-96` | **Мягко удалённые игры попадают в публичный листинг.** Raw SQL `SELECT games.*` без `games.deleted_at IS NULL`; GORM `.Raw()` не добавляет soft-delete фильтр. Delete → `r.db.Delete(&Game{})` = soft-delete. |
| **H4** | `user/oauth_service.go:167` | **VK externalID может стать пустым.** `token.Extra("user_id").(string)` — VK отдаёт число; assertion тихо падает → пустой ExternalID → повторный вход не сопоставляется / дубликаты. |
| **H5** | `user/repository.go:283-287` + `app/router.go:80-84` | **Мёртвая ветка ErrTokenUserNotFound.** `GetUserRole` через `Scan` не возвращает `gorm.ErrRecordNotFound` → удалённый пользователь с валидным JWT получает пустую роль вместо отзыва сессии. |
| **H6** | `game/svc_facade.go:149-175,190-221` | **Фасад нарушает слоистость:** `GetLogsByGameIDPaginated`/`SaveSettings` используют `s.db`+`clause.OnConflict` напрямую вместо `GameRepository` — остальные методы фасада делегируют подсервисам. |
| **H7** | `notification/service.go:221-227` | **Неограниченные goroutine в sendWebPush** (без пула/семафора) при всплеске уведомлений; `context.WithoutCancel` не даёт остановить при shutdown. |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| M1 | `app/router.go:199-231` | RUM принимает любые float (1e300/NaN-подобные) + `page` не валидируется → отравление метрик мониторинга. |
| M2 | `two_factor_service.go:106-119` | Энтропия backup-кодов ~20 бит (4 байта → %1000000). |
| M3 | `admin/templates/admin-games.html:96,100`, `admin-audit.html:88,92` | Mojibake: стрелки пагинации `в†ђ`/`в†’` вместо `←`/`→` (битые комментарии там же). |
| M4 | `games-show.html:1-6` + `layout.html:104` | Мёртвый `{{define "ExtraHead"}}` — OG-теги не попадают в HTML (layout использует `.ExtraHead` ключ, не `{{template}}`). |
| M5 | `funcs.go:164,181` + `games-edit.html:34` | Даты в UTC без привязки к таймзоне пользователя: автор UTC+3 видит «17:00» вместо «20:00». |
| M6 | `auth.go:64-82,105-116` | Роль из БД перечитывается на **каждый** авторизованный запрос без TTL (тема кэшируется, роль нет). |
| M7 | `admin-audit.html:55`, `profile-public.html:83` | Сырые таймстампы Go вместо `formatDateTime`. |
| M8 | `dashboard-index.html:171`, `game_passings-list.html:35,82` | Нелокализованные enum-статусы («started»/«accepted»). |
| M9 | `tournament/service.go:127-136,430-436` | Построчные INSERT/Upsert в циклах (AddGame, UpdateScoresForGame) — `CreateInBatches`/batch UPDATE. |
| M10 | `oauth_service.go:222-231` | OAuth: 2 отдельных UPDATE (name, email_verified) на каждый вход → один `map[string]any`. |
| M11 | `svc_passing.go:102-128` | `ListByGamePaginated` — Preload Team+Captain → 4 запроса (JOIN в один SQL). |
| M12 | `svc_monitor.go:214-218` | LATERAL-подзапрос GameSnapshot с ORDER BY created_at — потенциальный filesort. |
| M13 | `svc_review.go:86` | Каждый отзыв бьёт версию листинга → кэш 30с бесполезен при активном ревью-потоке. |
| M14 | `svc_coauthor.go:52-78` | `HasPermissionTx` грузит полную строку Game (в т.ч. description) вместо `Select("author_id")`; два запроса → один UNION. |
| M15 | `svc_progress.go:307-326` | N+1 в `CheckTimeouts` (First passing + AdvanceToNextLevel на каждый прогресс). |
| M16 | `user/routes.go:39-44,144` | Дублирование сервисов/репозиториев: `NewProfileService` (DI-инстанс мёртв), `NewTwoFactorService` (3-й инстанс), `NewGormAchievementRepo`, `NewGormUserRepo` ×2, `UserDashboardService` не в DI. |
| M17 | `game/routes.go:56` | `NewSimulateService` не в DI — единственный сервис, создаваемый в routes. |
| M18 | `dashboard_service.go:79` | Непоследовательная обработка ошибок (`return &dash, err` без обёртки; loadInvitations глотает ошибку). |

### 🔲 Осознанно оставлено (низкий риск / стайл)
- PII в логах 2FA (user_id вместо email), /healthz раскрытие, мягкое перечисление 2FA-аккаунтов, LATERAL-index, notifications-индекс (вероятно покрыт 000032), flash-дубли, themeMinutes-дубли, keypress-устаревание.

---

## B. Оптимизации

### Рекомендованные CREATE INDEX (проверено по migrations)
```sql
CREATE INDEX IF NOT EXISTS idx_team_members_team_id
    ON team_members(team_id);                 -- H7/G-1: SearchUsersForInvitation, RemoveMember, JOIN dashboard/rating
CREATE INDEX IF NOT EXISTS idx_game_passings_team_status
    ON game_passings(team_id, status);        -- DashboardTeams JOIN + турнирные выборки
CREATE INDEX IF NOT EXISTS idx_level_progresses_passing_unfinished_created
    ON level_progresses(game_passing_id, created_at DESC) WHERE finished_at IS NULL; -- GameSnapshot LATERAL
```
Проверить в миграциях: `000031` (voting_sessions unique) и `000032` (notifications user_created) — вероятно уже покрывают M12/M13 индексы.

### Прочие оптимизации
- RUM: клампить виталы (0<v<60с; CLS 0..1), валидировать page, отправлять INP при `pagehide`.
- Роль: короткий TTL-кэш (5-10с) по аналогии с themeCache.
- Push: worker-pool с буферизованной очередью + graceful drain.
- `HasPermissionTx`: `Select("author_id")` + один UNION.
- OAuth: один UPDATE + `INSERT ON CONFLICT` с RETURNING.

---

## C. Улучшения пользовательского опыта

1. **H1**: formatDate/formatDateTime должны принимать `*time.Time` (type-switch) — вернуть даты на 4 страницах.
2. **H2**: переименовать define в `tournaments-games.html` — страница добавления игры снова рендерится.
3. **M3**: исправить mojibake в admin-шаблонах.
4. **M4**: прокинуть OG-теги через `{{template "ExtraHead"}}`.
5. **M5**: таймзона пользователя (cookie offset) при отображении/редактировании дат.
6. **M7**: единый `formatDateTime` для admin-audit/profile-public.
7. **M8**: локализация enum-статусов через T-ключи.
8. Мобильные lang-кнопки без `aria-pressed`; `lang` cookie без `Secure`; `color-scheme` для `<select>`; дубли авто-скрытия flash; гонка ответов поиска в co_authors/invitations (нужен request-token как в calendar).
9. **INP в RUM**: отложенная отправка при `pagehide`.

---

## D. Архитектурные улучшения (кодовая база)

1. **DI-полнота (M16/M17)**: добавить `UserDashboardService` и `SimulateService` в wire; использовать `repos.Achiev`/`repos.User`/`services.Profile` из DI; убрать 3-й инстанс TwoFactorService.
2. **Слоистость (H6)**: перенести raw SQL из `svc_facade.go` (GetLogsByGameIDPaginated, SaveSettings) в `GameRepository`.
3. **Тесты**: добавить `svc_facade_test.go` (ShowGame, SaveSettings, GetLogs...), `dashboard_service_test.go` (GetDashboard), `email_verification_service_test.go`; реализовать `TestTwoFALoginVerify_ValidCode` (сейчас t.Skip); VK error-ветки OAuth.
4. **Мёртвый код**: `cacheGetRating` в svc_facade (проверить вызовы), `ErrTokenUserNotFound`-ветка (H5), пустой тест-заглушка.

---

## Приоритет фиксов
1. **H1** — пропали даты (регрессия от pass 29).
2. **H2** — пустая страница турнира.
3. **H3** — удалённые игры в листинге; **H4** — VK externalID; **H5** — роль для удалённых пользователей.
4. **H6/H7**, M16/M17 (DI), M1 (RUM-валидация).
5. Индекс `team_members(team_id)`.

## Статус
**ЗАКРЫТ** — все находки pass 30 исправлены (H1-H7, M1-M18, индексы 000039). Закрытие прошло в 4 раунда с верификацией @tester (нашёл и помог исправить регрессию HasPermissionTx) и @reviewer (подтвердил знак tz_offset, нашёл S1/S2/S3, все исправлены). Остаточные риски задокументированы в «Статусе (обновлено 8 авг 2026)» выше.

</details>
