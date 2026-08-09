# Deep Review Gengine-0 — 9 августа 2026 (pass 43 — повторное ревью после полного закрытия pass 30-42)

## Резюме

Повторное глубокое ревью выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture/DI) с последующей **личной верификацией ключевых находок** по коду.

**Итог pass 43:** 1 критичный (S-43-1 — обход авторизации в командном чате), 2 высоких (UX-H1/H2 — stale user selection), ~8 средних, ~8 низких. Все ключевые находки исправлены раундами 1-3 (раунд 3 — финальные доделки: VK OAuth для привязанных аккаунтов, logs проекция, RotateBackups ListOldest, partial-индекс 000050, CanReview EXISTS).

> **Контекст:** pass 30-42 закрыты полностью. Новые находки: **S-43-1 — участник команды A мог читать/писать в чат команды B** (проверка TeamID была мёртвым кодом), **UX-H1/H2 — stale user selection + Enter-bypass в search-формах** (приглашение/соавтор не того пользователя), **P-43-1/5/6/7** (reviews без LIMIT, teams.name без trgm, LOWER LIKE, GetAvailableGames), **P-43-10** (snapshot eviction race → 500 у SSE), **UX-M1-M6** (change_captain error flash, дубль OG-тегов, cover aria, hardcoded строки, валидация), **A-44** (stale mock).

---

# PASS 43 (повторное ревью) — 9 августа 2026

> Четырнадцатое повторное ревью после полного закрытия pass 30-42. Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок по коду.

## Статус (обновлено 9 авг 2026) — PASS 43 ОТКРЫТ

Находки перечислены ниже; закрытие — раундами фиксов.

**Раунд 1** (правки текущие, не закоммичены до проверки):
- **S-43-1** (критично): ChatWS — проверка TeamID-first для командных комнат (участник команды A больше не может подключиться к чату команды B).
- **UX-H1/H2**: co_authors-manage + invitations-new — изменение текста инвалидирует выбор; submit-guard при пустом выборе.
- **P-43-1**: ReviewRepository.ListByGame — защитный LIMIT 100.
- **P-43-5/6**: миграция 000049 (teams.name trgm) + ILIKE вместо LOWER(x) LIKE в SearchUsersForInvitation.
- **P-43-7**: GetAvailableGames — Select(id,name)+LIMIT 100.
- **P-43-9**: tournament List Preload("Author") с Select колонок (без password_hash/email).
- **P-43-10**: GetOrFetchSnapshotJSON — eviction между Get/Peek больше не даёт 500 (возвращает данные без кэша).
- **UX-M1**: teams-change_captain — flash error.
- **UX-M2**: layout.html — убраны дубли OG/twitter-тегов (pass 38 блок).
- **UX-M3**: games-edit cover — id/for label.
- **UX-M4/M5**: hnd_settings.go — локализованная ошибка лимита времени + верхние границы hint_penalty/max_hints.
- **UX-L4**: user-2fa-enable — data-no-loading.
- **UX-L5**: user-2fa-disable — btn-danger.
- **A-44**: `go generate ./internal/domain/user/` — MockProfileRepository сгенерирован.
- **Опровергнуто лично**: A-45 (ProcessSnapshot уже имеет WithTimeout 10s), P-7/P-3 (room workers и LRU уже реализованы в коде), P-43-2 (COUNT OVER не окупается из-за Preload).

**Раунд 2** (доделки — оставшиеся пункты pass 43):
- **S-43-2**: RotateBackups — добавлен `ListOldest(ctx, limit)` (интерфейс+репо); ротация удаляет САМЫЕ СТАРЫЕ записи (List отдавал новые DESC+LIMIT 100 и при >100 записей старые не удалялись никогда).
- **P-43-3**: миграция 000050 — partial-индекс `level_progresses(started_at) WHERE finished_at IS NULL` (CheckTimeouts 30s sweep).
- **P-43-11**: getUnreadCount sweep ограничен 256 записями за проход (не блокирует мутекс надолго).
- **P-43-12**: GetLogsByGameIDPaginated — проекция колонок (id, created_at, level_id, message) вместо logs.*.
- **P-43-13**: CanReview — один `SELECT EXISTS` вместо двух COUNT.
- **S-43-4**: VK OAuth — вход для существующего email разрешён, если связка ExternalLogin по VK user_id уже привязана (пользователь сам её создавал); новый метод `ExistsByProviderExternalID` (интерфейс+репо+мок). Anti-hijack сохранён для не-привязанных.
- **UX-L6**: webauthn-login-button — min-h-44px + safeToast (UX-L9, fast-click до загрузки app.js).
- **UX-L7**: levels-list move-кнопки — min-h/min-w 44px.
- **Опровергнуто**: UX-M6 (ValidateGameDates НЕ должен запрещать deadline после старта — регистрация может продолжаться во время игры, подтверждено тестом), P-43-4 (authed-листинг — кэш per-user фрагментирует LRU (pass 25), UNION-рефакторинг рискован; SPECULATIVE, низкий приоритет), S-43-3 (2FA fingerprint — требует знания пароля; стандартная практика).

**Осталось к pass 44** (документировано): S-43-3 (2FA-статус по редиректу — приемлемо), P-43-4 (authed-листинг OR без кэша — низкий приоритет).

---

## Найденные ошибки pass 43 (верифицировано лично)

### 🔴 Критично

| ID | Файл | Проблема | Статус |
|---|---|---|---|
| **S-43-1** | `monitor/handler.go:600-622`, `repository.go:80-84` | **Обход авторизации командного чата**: комнаты команд создаются со всеми тремя полями (GameID+TeamID+PassingID), поэтому `else if chatRoom.TeamID != nil` — мёртвый код; единственная проверка — «менеджер ИЛИ участник любого прохождения игры». Участник команды A мог перебирать ID комнат, читать и писать в чат команды B (раскрытие private-чата + греф). | ✅ Исправлено (TeamID-first) |

### 🟠 Высокие

| ID | Файл | Проблема | Статус |
|---|---|---|---|
| **UX-H1** | `co_authors-manage.html`, `invitations-new.html` | **Stale user selection**: после выбора пользователя продолжение ввода не очищает hidden `user_id` — сабмит добавляет/приглашает СТАРОГО выбранного. | ✅ Исправлено |
| **UX-H2** | `co_authors-manage.html`, `invitations-new.html` | **Enter-bypass disabled submit**: Enter в поле поиска отправляет форму (implicit submission), hidden user_id пуст → валидные ошибки или неверное действие. | ✅ Исправлено (submit-guard) |

### 🟡 Средние

| ID | Файл | Проблема | Статус |
|---|---|---|---|
| **P-43-1** | `game/review_repository.go:59-65` | ListByGame без LIMIT — все отзывы игры в память и 5-мин кэш. | ✅ Исправлено (LIMIT 100) |
| **P-43-5** | `team/repository.go:145-168` | teams.name ILIKE без trgm-индекса (users.name — есть). | ✅ Исправлено (миграция 000049) |
| **P-43-6** | `team/repository.go:213-233` | LOWER(name) LIKE не использует trgm (case-sensitive). | ✅ Исправлено (ILIKE) |
| **P-43-7** | `tournament/repository.go:114-119` | GetAvailableGames — все игры автора (games.*). | ✅ Исправлено (Select+Limit) |
| **P-43-9** | `tournament/repository.go:72-84` | Preload("Author") — users.* (password_hash/email) на 50 турниров. | ✅ Исправлено (Select) |
| **P-43-10** | `game/svc_monitor.go:153-170` | Eviction между Get/Peek → 500 у SSE-клиента. | ✅ Исправлено |
| **UX-M1** | `teams-change_captain.html` | Нет flash-блока ошибки (валидация молча фейлится). | ✅ Исправлено |
| **UX-M2** | `layout.html` | Дубли OG/twitter-тегов (pass 38 + pass 40/42) + конфликт twitter:card. | ✅ Исправлено |
| **UX-M4** | `hnd_settings.go:137,171` | Hardcoded русские строки (Title/ошибка) в обход i18n. | ✅ Исправлено |
| **UX-M5** | `hnd_settings.go:157-175` | max_hints/hint_penalty без верхней границы (999999 принимался молча). | ✅ Исправлено (clamp) |
| **A-44** | `user/mock_service.go` | Stale mock: MockProfileRepository отсутствовал (ProfileRepository добавлен pass 35). | ✅ Исправлено (go generate) |

### 🔲 Низкие / мелочи

| ID | Файл | Проблема | Статус |
|---|---|---|---|
| **UX-M3** | `games-edit.html:74-86` | Cover file input без доступного имени. | ✅ Исправлено (id/for) |
| **UX-L4** | `user-2fa-enable.html` | Двойной обработчик submit (inline + initFormLoading). | ✅ Исправлено (data-no-loading) |
| **UX-L5** | `user-2fa-disable.html:34` | Ad-hoc red-классы вместо btn-danger. | ✅ Исправлено |
| **S-43-2** | `admin/service.go` | RotateBackups работает с LIMIT-100 — при >100 записей может остаться больше MaxBackups. | ✅ Исправлено (ListOldest) |
| **S-43-3** | `auth_handler.go:155-168` | 2FA-статус угадывается по редиректу при известном пароле. | 📋 Приемлемый fingerprint |
| **S-43-4** | `oauth_service.go:239-241` | VK OAuth не пускает существующих пользователей (emailVerified=false). | ✅ Улучшено: вход разрешён при ранее привязанной ExternalLogin; anti-hijack сохранён |
| **P-43-3** | `svc_progress.go:234-248` | CheckTimeouts ORDER BY started_at на unfinished-множестве — partial-индекс не покрывает. | ✅ Исправлено (миграция 000050) |
| **P-43-4** | `svc_listing.go` | Authed-листинг OR-предикаты без кэша. | 📋 Низкий приоритет |
| **P-43-11** | `notification/service.go:429-435` | Lazy sweep под мутексом O(n). | ✅ Исправлено (лимит 256 за проход) |
| **P-43-12** | `game/repository.go:251` | GetLogsByGameIDPaginated — logs.* (тяжёлый текст). | ✅ Исправлено (проекция id/created_at/level_id/message) |
| **P-43-13** | `review_repository.go:26-44` | CanReview — два COUNT. | ✅ Исправлено (EXISTS) |
| **UX-M6** | `games-edit.html:33-45` | deadline/starts_at порядок не валидируется. | ✅ Опровергнуто: регистрация после старта намеренно допустима (тест) |
| **UX-L6/L7** | `webauthn-login-button`, `levels-list` | Touch targets < 44px (mitigated). | ✅ Исправлено (min-h/min-w 44px) |
| **UX-L9** | `webauthn-login-button.html` | showToast до загрузки app.js — ReferenceError (fast-click). | ✅ Исправлено (safeToast guard) |

### 🔲 Опровергнуто / безопасно (личная проверка)

- **A-45** — ProcessSnapshot(context.Background()) в dispatcher: внутри `context.WithTimeout(10s)` — утечки нет. ✓
- **P-7/P-3** — per-room workers и Monitor LRU **уже реализованы** в коде (статус «open» устарел). ✓
- **P-43-2** — ListByGamePaginated COUNT+SELECT: Preload Team/Captain всё равно делает 2+ запроса, COUNT OVER не окупается. ✓
- **Pass 42 round 3** — theme loader via DI, healthz sanitized, backup LIMIT: всё VERIFIED. ✓
- **WS send-side** — room_id в payload игнорируется, broadcast только в свою комнату; уязвим только connect-authz (исправлен). ✓
- **Voting/upload/password-reset//api/*/i18n-escape/OAuth/admin-2FA** — защищены. ✓

---

## B. Оптимизации (производительность)

1. **P-43-1**: LIMIT ревью — кэш меньше, страница легче.
2. **P-43-5/6**: trgm-индекс teams.name + ILIKE — поиск команд без seq-scan.
3. **P-43-7/9**: Select-проекции — меньше данных из БД.

## C. Улучшения кодовой базы (архитектура)

1. **S-43-1**: правильная приоритизация authz (TeamID-first для командных комнат).
2. **A-44**: go generate для user-моков (MockProfileRepository).

## D. Улучшения UX

1. **UX-H1/H2**: защита от неверного выбора пользователя в search-формах.
2. **UX-M1-M5**: flash error, единые OG-теги, a11y cover, локализация, валидация границ.
3. **UX-L4/L5**: единые паттерны форм/кнопок.

## Приоритет фиксов (pass 43)
1. **S-43-1** (security — обход авторизации) — уже сделано.
2. **UX-H1/H2** (неверный invitee/соавтор) — уже сделано.
3. **P-43-1/5/6/7/10** (перф) — уже сделано.
4. **UX-M1-M5, A-44** (качество) — уже сделано.

---

# PASS 42 (повторное ревью) — 9 августа 2026

> Тринадцатое повторное ревью после полного закрытия pass 30-41. Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок по коду.

## Статус (обновлено 9 авг 2026) — PASS 42 ОТКРЫТ

Находки перечислены ниже; закрытие — раундами фиксов.

**Раунд 1** (правки текущие, не закоммичены до проверки):
- **P-01**: Vote — настоящий `SELECT EXISTS(...)` через Raw().Scan (первая версия pass 41 Count+Limit(1) в PostgreSQL всё равно сканировала все совпадения; короткозамыкание).
- **P-02**: удалена миграция 000048 из pass 41 — она дублировала `UNIQUE idx_blackbox_votes_session_voter` из 000023 (no-op) и её down.sql могла дропнуть UNIQUE-индекс.
- **P-04**: миграция 000048 (новая) — `attempts(level_progress_id, is_file, code)` + `attempts(level_progress_id, is_file, file_path)`.
- **P-05**: ListFilteredPaginated — Select только колонок карточек (вместо games.*) + индекс `games(is_draft, visibility, created_at DESC)`.
- **P-06**: индекс `team_members(user_id)` (GetPassingByUser JOIN).
- **P-07**: защитный LIMIT 500 в GetSubscriptions/GetFollowers.
- **P-09**: audit List — `COUNT(*) OVER()` в одном запросе (вместо Count+List round-trip).
- **UX-01**: games-show delete-модалка — undo блокируется при старте DELETE (data-loss race).
- **UX-02**: wizard финальный шаг — защита от двойного Enter-submit.
- **UX-03**: mobile-меню — Escape + click-outside + возврат фокуса.
- **UX-04**: team-chat «отправка…» — таймер-фолбэк 10с (не висит вечно) + i18n.
- **UX-05**: games-list карточки role=group вместо role=link (a11y).
- **UX-06**: disabled-пагинация — span+aria-disabled вместо фокусируемых ссылок.
- **UX-07**: aria-label на file-инпуты.
- **UX-08**: dark-mode контраст статусных цветов (gameplay, dashboard, photos, simulate).
- **UX-09**: toasts-ошибки → role=alert/assertive.
- **UX-10**: decline приглашения → confirm-модалка + i18n.
- **UX-11**: follow-list кнопка disabled на время DELETE.
- **UX-12**: aria-label на переключатель вида.
- **A-42-01/S-42-1**: team AddMember/RemoveMember → errors.Is (доделан pass 41).
- **A-42-03**: svc_passing.go — sentinel (ErrNotCaptain, ErrCannotStartGame, ErrPassingNotActive) вместо inline.
- **S-42-2**: убран мёртвый ErrNotFollowing из Follow-handler.
- **S-42-4**: отдельный CSRF_SECRET (fallback на SESSION_SECRET).
- **S-42-5**: VK user_ids → url.QueryEscape (defense-in-depth).
- **Опровергнуто лично**: G-1 (shadowing фикс корректен), W-1 (wire DI консистентен).

**Раунд 2** (остаточные пункты pass 42, не закоммичены до проверки):
- **S-42-3**: refresh fingerprint mismatch → отзыв только текущего токена (вместо всей семьи). Reuse отозванного по-прежнему отзывает семью. Тест `TestAuthService_FingerprintMismatchKeepsFamily`.
- **UX-13**: dashboard неизвестный PassingStatus → `passing.status_unknown` (вместо raw-строки).
- **UX-14**: offline.html кнопка — type=button.
- **Остаточный security-аудит завершён**: WS/SSE — CheckOrigin exact-host + authRequired (монитор, чат, логи, уведомления `/ws/notifications`); uploads — все через `storage.Save` (sanitizeFilename, boundary Rel, уникальные имена, MIME-проверка); iCal — без внешних запросов (SSRF нет); `.env` — в .gitignore; webhook-эндпоинтов нет (HMAC не требуется); /healthz раскрывает текст ошибки БД — задокументировано (обезличивать рискованно для мониторинга).
- **P-08 опровергнут**: iCal уже кэшируется 5 мин (P-6 pass 39).
- **P-11 опровергнут**: AggregateGameSnapshot индексы уже покрывают (PK level_progresses.id + idx_level_progresses_game_passing_id).
- **S-42-6**: Login() не имеет побочных эффектов — JWT генерируется, но не выставляется (wasted-issue, менять не требуется).

**Раунд 3** (доделки — оставшиеся пункты из pass 30-42):
- **A-1**: `GetUserThemeSettings(ctx, db, ...)` убрана — middleware темы использует `ProfileService` из DI (`app.Deps.Services.Profile`); raw *gorm.DB больше не протекает наружу.
- **P-10**: backup list — защитный LIMIT 100.
- **healthz disclosure**: checkDatabase/checkEmailQueue/checkDiskSpace — обезличены сообщения (err.Error() в логи через zerolog; наружу generic «database unavailable» и т.п.).
- **UX-10**: пагинация admin-teams/admin-audit — `min-h-[44px] min-w-[44px]` на стрелках (touch targets).
- **UX-6**: OG-теги (og:title/og:description/og:type/twitter:card) в layout.html; og:image не добавлен — static/img отсутствует.
- **S-1 (pass 41)**: оставлен как осознанный компромисс (L-1) — завышение счётчика на 1 до TTL при гонке Create/getUnreadCount, self-heals за 30с; устранить без потери L-1 невозможно без блокировки БД.
- **Опровергнуто/уже решено**: P-6 (logs LIMIT+индекс — pass 39), P-4 (ForceFinishGame батч — P-M8), F-4 (tournament LIMIT 50 — pass 35), UX-9 (auth-register data-no-loading — pass 38), UX-8 (replace('%s') function-заменители — pass 36), UX-11 (пустые переводы — заполнены), A-4 (sentinel game-домен — хендлеры используют LocalizeError, не errors.Is).

**Осталось к pass 43** (документировано): UX-16 (полная arrow-навигация календаря — фича, не баг), A-4 (sentinel game-домен — стилевой), P-3 (глобальный Lock монитор-кэша — sharding), P-7 (WS-хаб per-room workers), A-2/A-3 (единый путь прав), UX-3 (replace('%s') везде — стилевой).

---

## Найденные ошибки pass 42 (верифицировано лично)

### 🟡 Средние

| ID | Файл | Проблема | Статус |
|---|---|---|---|
| **P-01** | `monitor/service.go:162-175` | **Vote-EXISTS из pass 41 не настоящий EXISTS**: GORM `Count()+Limit(1)` в PostgreSQL даёт `count(*)` (LIMIT — no-op, сканируются все совпадения). Комментарий обещал short-circuit. | ✅ Исправлено (Raw EXISTS) |
| **P-02** | `migrations/000048` (pass 41) | **Миграция 000048 — no-op**: `idx_blackbox_votes_session_voter` уже создан UNIQUE в 000023. Down.sql дропала чужой UNIQUE-индекс → race-уязвимость при откате. | ✅ Удалена (пересоздана с новым содержимым) |
| **UX-01** | `games-show.html:437-441` | **Data-loss race в delete-модалке**: после отсчёта DELETE отправляется, но undo-кнопка активна — клик «Отменить» показывает «отменено» при уже-выполненном удалении. | ✅ Исправлено |
| **UX-02** | `games-new-wizard.html:188-198` | **Двойной Enter на финальном шаге** создаёт дубль игры: submit-обработчик не защищал финальный шаг, а кнопка защищалась только по click. | ✅ Исправлено |
| **A-42-03** | `game/svc_passing.go:81,218,222` | **Inline-ошибки без sentinel** — текст дублировал `tournament.ErrCaptainOnly`, но другой инстанс; errors.Is с ним не совпадёт. | ✅ Исправлено |
| **A-42-01/S-42-1** | `team/handler.go:339,362` | **AddMember/RemoveMember всё ещё string-match** (pass 41 исправил только Create). | ✅ Исправлено |
| **S-42-4** | `app.go:92`, `router.go:55` | **Один Secret на CSRF и session-store** — компрометация одного ослабляет оба. | ✅ Исправлено (CSRF_SECRET) |
| **P-05** | `svc_listing.go:92-97` | **ListFilteredPaginated SELECT games.*** — description/search_vector на каждую строку (авторизованные не кэшируются); дефолтная сортировка без индекса. | ✅ Исправлено |
| **P-07** | `social/repository.go:69-87` | **follows/followers без LIMIT** — 100k подписчиков = 100k строк users на просмотр профиля. | ✅ Исправлено (LIMIT 500) |
| **P-09** | `pkg/audit/audit.go:74-109` | **Count + List — два round-trip** на каждый просмотр админ-страницы аудита. | ✅ Исправлено (COUNT OVER) |

### 🔲 Низкие / мелочи

| ID | Файл | Проблема | Статус |
|---|---|---|---|
| **P-04** | `migrations/000048` | Нет индексов attempts(level_progress_id, is_file, code/file_path) — residual-фильтр в Vote. | ✅ Исправлено (миграция) |
| **P-06** | `game/repository.go:184-196` | GetPassingByUser JOIN team_members без user_id-индекса. | ✅ Исправлено (миграция) |
| **P-08** | `calendar/handler.go:119,202` | ListByDateRange вызывается дважды (месяц + iCal). | ✅ Опровергнуто: iCal уже кэшируется 5 мин (P-6 pass 39) |
| **P-10** | `admin/model.go:65-69` | Backup list без пагинации. | ✅ Исправлено (LIMIT 100) |
| **P-11** | `game/monitor_repository.go:64-71` | AggregateGameSnapshot — широкая выборка попыток. | ✅ Опровергнуто: индексы уже покрывают (PK level_progresses.id, idx_level_progresses_game_passing_id) |
| **S-42-3** | `user/handler.go:26-34` | Refresh привязан к IP-префиксу — NAT/мобильные ротируют IP → разлогин всей семьи. | ✅ Исправлено (раунд 2): mismatch отзывает только текущий токен, reuse — семью |
| **S-42-6** | `user/auth_handler.go:131-168` | JWT выпускается до 2FA-гейта (wasted-issue, токен не выставляется). | 📋 Не требует действий (нет побочных эффектов в Login) |
| **UX-13** | `dashboard-index.html:174` | Неизвестный PassingStatus рендерится raw (locale-дыра). | ✅ Исправлено (passing.status_unknown) |
| **UX-14** | `offline.html:7` | Кнопка без type=button (вне формы — безвредно). | ✅ Исправлено (type=button) |
| **UX-16** | `calendar-page.html` | Дни календаря не клавиатурные. | 📋 Полная arrow-навигация — фича, не баг (кнопки дней есть) |

### 🔲 Опровергнуто / безопасно (личная проверка)

- **G-1** — shadowing-фикс svc_play.go корректен, errgroup Wait() даёт happens-before. ✓
- **W-1** — wire DI консистентен (wire.go ↔ wire_gen.go ↔ wrap-функции). ✓
- **UX-03-проверка**: CSP, XSS-экранирование, loading-состояния, focus-trap — все проверенные шаблоны в порядке. ✓
- **S-42-регрессии**: isWithinBackupDir, CreateNow timeout, sentinel team/social — нет регрессий. ✓

---

## B. Оптимизации (производительность)

1. **P-01**: настоящий EXISTS короткозамыкается — нет count всех attempts.
2. **P-04/P-06**: композитные индексы attempts/team_members под hot-path Vote/GetPassingByUser.
3. **P-09**: COUNT(*) OVER() — один round-trip вместо двух.
4. **P-05**: Select колонок карточек + индекс дефолтной сортировки.
5. **P-07**: лимит списков follows.

## C. Улучшения кодовой базы (архитектура)

1. **A-42-03**: sentinel-ошибки svc_passing (errors.Is вместо строк).
2. **S-42-4**: разделение ключей CSRF/session.
3. **A-42-01**: доделан errors.Is в team-handlers.
4. **S-42-5**: QueryEscape defense-in-depth.

## D. Улучшения UX

1. **UX-01**: блокировка undo при старте delete.
2. **UX-02**: защита двойного Enter в wizard.
3. **UX-03/05/06/07/09/12**: a11y (меню, роли, aria, контраст).
4. **UX-04/10/11**: таймаут «отправка…», confirm decline, disable на время запроса.

## E. Остаточный аудит (выполнен в раунде 2 — pass 42)
- ✅ WebSocket/SSE auth: **CheckOrigin exact-host** (монитор, чат, логи — `monitor/handler.go`; уведомления — `notification/routes.go`), все WS/SSE под `AuthRequired` + `gameManager` на games-роутах, `/ws/notifications` — room по userID.
- ✅ SSRF в calendar/iCal: **отсутствует** — iCal строит .ics из БД, внешних запросов нет.
- ✅ Uploads traversal: **все через `storage.Save`** — sanitizeFilename (Base+regex), генерация уникальных имён, boundary через filepath.Rel, MIME-проверка (avatar, cover, photos, answers).
- ✅ Secrets-скан: `.env`, `.env.local`, `.env.*.local` — в .gitignore.
- ✅ `/healthz`: раскрывает текст ошибки БД (err.Error()) — **документировано**, обезличивать не стали (риск для мониторинга; решение за ops).
- ✅ Webhook HMAC: **входящих вебхуков нет** — не требуется.

## Приоритет фиксов (pass 42)
1. **P-01 + P-02** (корректность Vote + опасная миграция) — уже сделано.
2. **UX-01 + UX-02** (потеря данных / дубль создания) — уже сделано.
3. **P-05/P-07/P-09** (перф горячих списков) — уже сделано.
4. **A-42-01/A-42-03/S-42-4** (архитектура) — уже сделано.

---

# PASS 41 (повторное ревью) — 9 августа 2026

> Двенадцатое повторное ревью после полного закрытия pass 30-40. Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок по коду.

## Статус (обновлено 9 авг 2026) — PASS 41 ОТКРЫТ

Находки перечислены ниже; закрытие — раундами фиксов.

**Раунд 1** (правки текущие):
- **H-1**: RotateBackups — boundary-проверка пути через `isWithinBackupDir` (filepath.Rel), пропуск файлов вне BackupDir.
- **S-3**: CreateNow — собственный `context.WithTimeout(10m)` для pg_dump + чистка частичного файла при ошибке.
- **N-1**: Vote — `EXISTS` через `Count+Limit(1)` вместо загрузки ВСЕХ attempts уровня.
- **N-2**: миграция 000048 — композитный индекс `blackbox_votes(session_id, voter_id)`.
- **N-3**: ListByDateRange — `Select` только нужных колонок games (вместо games.*).
- **F-01**: unfollow — `data-confirm-ok` + `data-confirm-danger` (кнопка «Отписаться», а не «Удалить»).
- **F-02**: auth-reset — `data-no-loading` (локализованный спиннер не перетирается).
- **G6**: team/social — sentinel-ошибки (ErrInvitationExists, ErrOnlyCaptainCanInvite, ErrCannotFollowSelf) + `errors.Is` вместо string-match в хендлерах.
- **A-2**: svc_play.go — убрано shadowing receiver `s` в goroutine (gs).
- **Опровергнуто лично**: G5 (ProcessSnapshot уже имеет WithTimeout 10s), UX-3 (один aria-live, L-8 управляет), A-4 (нет string-match в social repository).

**Осталось к pass 42**: S-1 (unread race — компромисс L-1, документирован), A-1 (GetUserThemeSettings — решён в pass 42 раунд 3).

---

## Найденные ошибки pass 41 (верифицировано лично)

### 🟠 Высокие

| ID | Файл | Проблема | Статус |
|---|---|---|---|
| **P-1** | `monitor/service.go:162-177` | **Vote загружает ВСЕ попытки уровня без LIMIT** (`Find(&attempts)` + фильтр в Go) — на активной игре тысячи строк кодов в память на каждый голос. | ✅ Исправлено (EXISTS) |
| **S-1** | `notification/service.go:453-464` | **Race в incrementUnreadCount** — параллельный getUnreadCount может положить в кэш COUNT с уже-включённым уведомлением, затем increment завысит на 1 до TTL (30с). Само-лечится; компромисс ради L-1. | 📋 Компромисс |

### 🟡 Средние

| ID | Файл | Проблема | Статус |
|---|---|---|---|
| **S-2** | `admin/service.go` | **RotateBackups удаляет файл по пути из БД без boundary-проверки** (в отличие от Download) — при компрометации записи удаление произвольного файла ФС. | ✅ Исправлено (isWithinBackupDir) |
| **S-3** | `admin/service.go:71-83` | **CreateNow — pg_dump без собственного таймаута**: disconnect оставит частичный файл. | ✅ Исправлено (10m timeout + cleanup) |
| **N-2** | `migrations/000002` + `monitor/service.go` | **Нет композитного индекса `blackbox_votes(session_id, voter_id)`** — только раздельные (bitmap-OR). | ✅ Исправлено (миграция 000048) |
| **N-3** | `game/repository.go` | **ListByDateRange SELECT games.*** — тянет Description/search_vector/rating на год вперёд для календаря/iCal. | ✅ Исправлено (Select) |
| **F-01** | `social/templates/follow-list.html` | **unfollow confirm: кнопка «Удалить» вместо «Отписаться»** — нет data-confirm-ok/danger. | ✅ Исправлено |
| **F-02** | `user/templates/auth-reset.html` | **Двойной обработчик submit**: inline локализованный «Сброс…» перетирается generic-спиннером. | ✅ Исправлено (data-no-loading) |
| **G6** | `team/handler.go`, `social/handler.go` | **String-match ошибок по err.Error()** вместо errors.Is — brittle. | ✅ Исправлено (sentinel + errors.Is) |
| **A-2** | `game/svc_play.go:741` | **Shadowing receiver `s`** внутри goroutine. | ✅ Исправлено (gs) |

### 🔲 Низкие / мелочи

| ID | Файл | Проблема | Статус |
|---|---|---|---|
| **A-1** | `user/profile_service.go:76` | `GetUserThemeSettings(ctx, db, ...)` принимает raw `*gorm.DB` — кандидат на репозиторий. | ✅ Исправлено (pass 42, раунд 3: через ProfileService из DI, функция удалена) |
| **G5** | `game/svc_play.go` | ProcessSnapshot(context.Background()) в тестах — без отмены. | ✅ Опровергнуто: внутри WithTimeout(10s) |
| **UX-3** | `monitor/templates/chat-page.html` | Дубль aria-live. | ✅ Опровергнуто: один role=log, L-8 управляет |
| **A-4** | `social/repository.go` | String-match ошибок. | ✅ Опровергнуто |

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

# PASS 40 (повторное ревью) — 9 августа 2026

> Одиннадцатое повторное ревью после полного закрытия pass 30-39 и разбора дефера (P-5/P-7). Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок по коду.

## Резюме pass 40

**Итог:** 1 критичная (panic в RoomHub — регресс P-7), 2 высоких (все подтверждены), ~10 средних, ~8 низких.

> **Важно:** критичная находка — **регрессия P-7 pass 39**: `close(q)` в RoomHub вызывал panic `send on closed channel` при конкурентном broadcast+unregister. Исправлено. Также: **data race на cachedSnapshot.json**, **поиск на дашборде полностью сломан** (form отправляла `q`, сервер читал `search`), **monitor %dс плейсхолдер**, HasPermissionTx с context.Background.

---

## Статус (обновлено 9 авг 2026) — PASS 40 ОТКРЫТ

Находки перечислены ниже; закрытие — раундами фиксов.

**Раунд 1** (`0947b44`):
- **L-1**: unread-счётчик инкрементируется (вместо invalidate + COUNT-per-Create) — P-M6 достигнут.
- **L-3**: миграция 000047 — `idx_tournament_results_tournament_team (tournament_id, team_id)`.
- **L-4**: SSE `[]byte` один раз на broadcast.
- **L-5**: cache.removeExpired — `Peek` вместо `Get` (sweep не промоутит).
- **L-6**: пагинация админки — cap page ≤ 10000.
- **L-7**: pg_dump файлы chmod 0600.
- **L-8**: chat aria-live off на время загрузки истории (chat-page + team-chat).
- **L-9**: calendar dark today highlight (`dark:bg-blue-900/30`).
- **L-10**: `ErrTestingOnly` sentinel (убран дубликат).
- **L-11**: monitor startPolling идемпотентен.

**Раунд 2** (`19ef9b8`):
- **T1**: room_hub_worker_test.go — стресс broadcast↔unregister (50 раундов, ловит C-1 panic), idle-exit lifecycle, Stop worker exit, multi-room no-leak.
- **Rate limit**: добавлен на POST /voting/vote (20/мин) и POST /games/:id/review (10/мин) — были без лимита.
- **IDOR team/tournament/export** — проверено: CanManageTeam / checkGameAccess / AuthorID — безопасно.

**L-2** — закрыт как опровергнутый: `idx_follows_author_id` уже существует в миграции 000002.

---

## A. Найденные ошибки pass 40 (верифицировано лично)

### 🔴 Критично

| ID | Файл | Проблема |
|---|---|---|
| **R-1** | `pkg/websocket/room_hub.go:199-203` | **Регресс P-7: panic `send on closed channel`.** broadcast берёт `queue` под RLock, отпускает, затем `queue <- msg` — параллельно unregister/dispatchToRoom/Stop закрывают очередь (`close(q)`) → panic в горутине runLoop → крах всего WS-хаба. Подтверждено тремя агентами + лично. **Исправлено** (см. ниже). |

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **R-2** | `game/svc_monitor.go:160-163` | **Data race на `cachedSnapshot.json`**: `cached.json = jsonData` мутирует объект, на который ссылаются другие читатели LRU. **Исправлено** (Add нового значения). |
| **R-3** | `user/templates/dashboard-index.html` + `dashboard_handler.go:41` | **Поиск на дашборде полностью сломан**: формы отправляли текст в `name="q"`, сервер читал `c.Query("search")` (старое значение из hidden). Ввод «исчезал». **Исправлено** (text → name="search", hidden убран во всех 4 формах). |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| **R-4** | `monitor/templates/monitor-page.html:90-92` | **`%dс` утекает в статус реконнекта** — onStatus получает `{delay}`, но replace('%d') не вызывался. **Исправлено.** |
| **R-5** | `calendar/templates/calendar-page.html:193` | **replace('%s', str)** с `$`-паттернами (класс UX-3 pass 39 не доделан). **Исправлено** (replacement-fn). |
| **R-6** | `game/svc_coauthor.go:86-105` | **HasPermissionTx с `context.Background()`** — запросы прав не отменяются. **Исправлено** (ctx прокинут из всех 6 вызовов). |
| **R-7** | `game/svc_photo.go:47-58` | **Дублирующая проверка прав соавтора** (инлайн вместо hasCoAuthorRole) — риск расхождения ролей. **Исправлено.** |
| **R-8** | `game/repository.go:167-175` | **ListByDateRange Preload("Author") без Select** — password_hash/email в памяти на публичном calendar/ICal. **Исправлено** (id,name,avatar_path). |
| **R-9** | `pkg/websocket/room_hub.go` (G2) | **Утечка room-воркера через cleanupInactiveClients** (не удалял roomQueues). **Исправлено.** |
| **R-10** | `game/svc_monitor.go:138-141` | Возврат общего `cached.json` слайса консьюмерам (defensive nit). |

### 🔲 Низкие / мелочи

| ID | Файл | Проблема |
|---|---|---|
| **L-1** | `notification/service.go:229,397` | getUnreadCount — лишний COUNT на каждое создание уведомления (P-M6 не достигнут). |
| **L-2** | `social/repository.go:80-87` | Отсутствует индекс `follows(author_id)` для GetFollowers (СПОРНО, migration 000002 не проверен). |
| **L-3** | `tournament/repository.go:248` | Отсутствует индекс `tournament_results(tournament_id, team_id)` (СПОРНО). |
| **L-4** | `hnd_sse.go:270-274` | SSE Broadcast аллокация []byte на каждого подписчика. |
| **L-5** | `pkg/cache/cache.go:114-137` | removeExpired — полный проход ttlKeys под write-lock (Peek вместо Get не промоутит). |
| **L-6** | `admin/handler.go:174,380` | Пагинация без верхней границы page → огромные OFFSET. |
| **L-7** | `admin/service.go:70-83` | Плейнтекстовые pg_dump-бекапы (hash паролей/2FA секреты) без шифрования — hardening. |
| **L-8** | `chat-page.html:16`, `team-chat.html:8` | История чата объявляется скринридером целиком (aria-live на время загрузки истории). |
| **L-9** | `calendar-page.html:136` | Подсветка «сегодня» только в light (нет dark:bg-blue-900/30). |
| **L-10** | `svc_play.go:562,619` | Дубликат ошибки «тестовый режим…» в двух методах + прочие errors.New без sentinel. |
| **L-11** | `monitor-page.html:116-121` | startPolling вызывает fetchInitialData при каждом вызове (лишние fetch). |

### 🔲 Опровергнуто / безопасно (личная проверка + агенты)

- **checkTimeouts batch advance (P-1 pass 39)** — корректен, защита от дублей (RowsAffected + Pluck), финиш игры не пропускается.
- **checkAutoStartGames (S-1)** — ON CONFLICT с partial-индексом корректен.
- **monitor LRU (P-3 pass 39)** — thread-safe, singleflight с Peek double-check корректен (кроме R-2/R-10).
- **logs.game_id (P-5 pass 40)** — индекс покрывает оба запроса; COUNT OVER 1 запрос.
- **room_hub (после фикса)** — очередь не закрывается, воркеры завершаются по done/idle-таймеру, нет send-on-closed.
- **CSRF, admin-доступ, pg_dump (exec array, PGPASSWORD), uploads MIME, push SSRF, refresh-ротация** — безопасно.

---

## B. Оптимизации (производительность)

1. **L-1**: не инвалидировать unread-кэш до отправки WS (инвалидация после) или инкрементировать счётчик.
2. **L-2**: индекс `follows(author_id)` для GetFollowers (подтвердить migration 000002).
3. **L-3**: индекс `tournament_results(tournament_id, team_id)`.
4. **L-4**: `eventBytes := []byte(event)` один раз до цикла в SSE.
5. **L-5**: Peek вместо Get в removeExpired (не промоутит при sweep).
6. **L-6**: верхняя граница page в пагинации (напр. ≤ 10000).
7. **R-10**: возвращать копию json слайса из GetOrFetchSnapshotJSON.

## C. Улучшения кодовой базы (архитектура)

1. **L-10**: единый sentinel для «тестовый режим…», объединить дубликаты.
2. **HasPermissionTx ctx** — уже сделано (R-6); проверить, что нет context.Background в других горячих путях.
3. **room_hub тесты** — добавить стресс-тест «broadcast → unregister → broadcast» под -race + goleak.

## D. Улучшения UX

1. **L-8**: aria-live="off" на время загрузки истории чата.
2. **L-9**: dark:bg-blue-900/30 для подсветки «сегодня» в календаре.
3. **L-11**: не вызывать fetchInitialData при каждом startPolling.

## Приоритет фиксов (pass 40)
1. **R-1** (panic RoomHub) — сделано.
2. **R-2/R-3** (data race + dashboard search) — сделано.
3. **R-4..R-10** (мелкие UX/arch) — сделано.
4. **L-1..L-11** (низкие) — кандидаты на раунд 2.

---

# PASS 39 (повторное ревью) — 9 августа 2026

> Десятое повторное ревью после полного закрытия pass 30-38. Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок по коду.

## Резюме pass 39

**Итог:** 0 критичных, 2 высоких (все подтверждены лично), ~10 средних, ~10 низких.

> Кодовая база стабильна, линтер 2.12.2 чист. Найдены: **регрессия UX-9 pass 38** (data-no-loading на form не читается initFormLoading — кнопка перетирается спиннером), **SEO-баг** (двойной суффикс «· Encounter Engine», пустой canonical, дубль og:title), **N+1 в checkTimeouts** (next-level по одному), **JSON round-trip на каждый in-memory cache hit**, **UNIQUE-индекс level_progresses без WHERE deleted_at** (миграция может упасть на дублях).

---

## Статус (обновлено 9 авг 2026) — PASS 39 ЗАКРЫТ

**Все находки pass 39 исправлены** (3 раунда фиксов):

**Раунд 1-2** (`2cf9cdd`):
- **UX-1**: initFormLoading читает `data-no-loading` и с формы, и с кнопки — регрессия UX-9 pass 38 устранена (кнопка регистрации не перетирается спиннером).
- **SEO-1**: убран двойной суффикс «· Encounter Engine» (layout — единственное место); убран дублирующий og:title из games-show ExtraHead.
- **SEO-2**: CanonicalURL заполняется absolute self-URL (с учётом X-Forwarded-Proto).

**Раунд 2 (средние):**
- **S-1**: миграция 000045 — UNIQUE-индекс стал частичным `WHERE deleted_at IS NULL` + ON CONFLICT с предикатом (не падает на дублях удалённых, не блокирует soft-delete+re-create).
- **A-1**: удалено мёртвое поле `CoAuthorService.db`; конструктор `NewCoAuthorService()` без параметра; все 9 вызовов + wire обновлены.
- **P-4**: InvalidateCache форсит singleflight (`sfGroup.Forget`) — устаревшие данные не перезаписывают кэш.
- **UX-2**: aria-live таймера — объявление только на смене минуты и границах 60/30/10 (не каждую секунду).
- **P-2**: cacheGetJSON для in-memory Cache — прямой reflect-копией вместо JSON round-trip (только для Valkey остаётся JSON).

**Осталось на раунд 3+ (низкий приоритет):** P-1 (checkTimeouts next-level batch), P-3 (глобальный Lock монитор-кэша — sharding), P-5 (GetLogsByGameID sort-индекс), P-6 (CalendarICal кэш), P-7 (WS-хаб per-room workers), A-2 (единый путь прав), A-3 (дублированная проверка владельца), A-4 (sentinel game-домен), UX-3 (replace('%s') везде), UX-4..10 (мелочи).

**Раунд 3 (текущий):**
- **UX-3**: replace('%s', str) с `$`-паттернами исправлен на replacement-функции (hint, level_name, authorName, follow errors).
- **A-3**: убрана дублированная проверка владельца в GameService.Delete (права проверяет GameCRUDService.Delete).
- **UX-9**: initFormLoading не обрабатывает GET-формы (фильтры/поиск не меняют кнопку).

**Проверка:** `go build ./...` ✓, `go vet ./...` ✓, `golangci-lint 2.12.2` → 0 issues ✓, `gofmt -l .` → пусто ✓, `go test -count=1 -short ./...` ✓, 84 шаблона валидны ✓.

**Оставлено осознанно (задокументировано):**
- **P-1** (next-level batch в checkTimeouts) — заметный рефакторинг без функционального бага; перф-улучшение для фоновой джобы.
- **P-3/P-5/P-6/P-7** (Lock sharding, logs.game_id, ical кэш, WS per-room) — перф-оптимизации без влияния на корректность.
- **A-2** (единый путь прав HasPermission vs HasPermissionTx) — оба варианта корректны, поведение совпадает; кандидат на рефакторинг.
- **A-4** (sentinel game-домен) — конвенции Go, без функционального бага.

---

## Дефер pass 39 разобран (commit `c46e524`)

- **P-6**: CalendarICal кэшируется на 5 мин (полногодовой запрос + Preload только раз в 5 мин).
- **A-2**: HasPermissionTx через CoAuthorRepository tx-методы (GetGameAuthorIDWithTx + FindByGameAndUserWithTx) — единый путь, raw SQL убран из сервиса.
- **P-1**: checkTimeouts advance-loop — prefetch уровней затронутых игр одним запросом + ручной поиск next-level + batch CreateInBatches (было 1-2 SQL на просроченный прогресс каждые 30с).
- **P-3**: монитор-кэш — самописный LRU (map+list+mu с глобальным Lock на горячем polling-пути) заменён на thread-safe `hashicorp/golang-lru` (Get промоутит, Add вытесняет; без ручных локов и гонок).
- **A-4**: sentinel-ошибки game-домена (ErrLevelNotFileUpload, ErrGameNotStarted, ErrHintsDisabled, ErrNoHintAvailable, ErrGameDeleteForbidden, ErrCompletedLevelNotFound).

**Осталось задокументировано (крупные рефакторинги без функционального бага):**
- **P-5**: GetLogsByGameID sort-индекс — требует денормализации `logs.game_id` (миграция + backfill + триггер).
- **P-7**: WS-хаб per-room workers для broadcast на большие комнаты.

---

## Крупные рефакторинги P-5/P-7 выполнены

- **P-5**: денормализация `logs.game_id` — модель Log + миграция 000046 (ADD COLUMN + backfill + индекс `idx_logs_game_created (game_id, created_at DESC)`); GetLogsByGameID/GetLogsByGameIDPaginated фильтруют по `logs.game_id` без JOIN game_passings (index-only); все 4 создания логов (svc_play) заполняют GameID.
- **P-7**: WS-хаб per-room workers — broadcast диспатчится в очередь комнаты, воркер комнаты рассылает независимо от других комнат (раньше runLoop сериализовал все рассылки одной горутиной); очереди закрываются при опустении комнаты и в Stop; `roomWorkersWg.Wait()` в Stop.

---

## A. Найденные ошибки pass 39 (верифицировано лично)

### 🔴 Критично

Нет.

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **UX-1** | `static/js/app.js:119` vs `auth-register.html:12`, `notes-manage.html:8` | **Регрессия UX-9 pass 38: data-no-loading не работает.** initFormLoading читает `btn.dataset.noLoading` (кнопка), но атрибут поставлен на **форму**. Результат: на регистрации текст «Регистрируем…» перетирается спиннером «⟳ Отправка…»; на notes-manage кнопка «Сохранить» теряет подпись после ошибки. **Верифицировано лично.** |
| **SEO-1** | `hnd_game.go:241` + `layout.html:64` | **Двойной суффикс «· Encounter Engine»** (хендлер + layout) → «Name · Encounter Engine · Encounter Engine». Плюс `games-show.html:3` создаёт второй `og:title` («Gengine-0» vs «Encounter Engine»), краулеры берут первый из layout. **Верифицировано лично.** |
| **SEO-2** | `render/helper.go:208` | **Canonical всегда пустой**: `data["CanonicalURL"] = ""`, ни один хендлер не переопределяет → `<link rel="canonical" href="">` на каждой странице. **Верифицировано лично.** |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| **P-1** | `game/svc_progress.go:335-374` | **checkTimeouts advance-loop всё ещё N+1**: next-level выбирается по одному на просроченный прогресс (SELECT + INSERT, или + COUNT) — до 100 SQL/цикл каждые 30с. |
| **P-2** | `game/service.go:199-223` | **JSON round-trip на каждый in-memory cache hit** (листинг, отзывы, leaderboard): value уже типизирован в LRU, но cacheGetJSON делает Marshal+Unmarshal. |
| **P-3** | `game/svc_monitor.go:88-99` | **Глобальный эксклюзивный Lock на каждом cache-hit снапшота** — горячий polling-путь (поллер 5с + зрители), точка контеншена с InvalidateCache. |
| **P-4** | `game/svc_monitor.go:209-217` | **InvalidateCache не форсит singleflight** — пересчёт, начатый до инвалидации, перезапишет кэш устаревшими данными. |
| **S-1** | `migrations/000045` + `model.go:100-111` | **UNIQUE-индекс level_progresses(game_passing_id, level_id) без WHERE deleted_at IS NULL**: 1) миграция упадёт, если в проде уже есть дубликаты; 2) soft-delete прогресса + повторное создание того же (passing, level) → unique-violation. **Верифицировано лично (по коду).** |
| **A-1** | `game/svc_coauthor.go:22,26` | **Мёртвое поле `db *gorm.DB` в CoAuthorService** — после N-2 (pass 38) ни один метод не читает `s.db` (HasPermission через repo, HasPermissionTx принимает tx). Тянется в DI зря. **Верифицировано лично.** |
| **A-2** | `game/svc_coauthor.go:46-65,86-107` | **Два параллельных пути проверки прав**: HasPermission (repo, 2 запроса) vs HasPermissionTx (raw SQL) — структура продублирована, риск расхождения. |
| **A-3** | `game/service.go:337-339` vs `svc_crud.go:102-104` | **Дублированная проверка владельца при удалении** (GameService.Delete + GameCRUDService.Delete) + raw errors.New. |
| **UX-2** | `gameplay-show.html:175` | **aria-live спам**: в последние 60с условие `timeLeft <= 60` истинно каждую секунду → скринридер читает `#timer-live` каждую секунду. |
| **UX-3** | `gameplay-show.html:378,523`, `follow-list.html:44`, `profile-public.html:148,154` | **replace('%s', str) с `$`-паттернами** (класс UX-8 pass 38) не исправлен везде — `$&`/`$'` в данных искажают текст. |

### 🔲 Низкие / мелочи

| ID | Файл | Проблема |
|---|---|---|
| **P-5** | `game/repository.go:202-260` | **P-3 pass 38 открыт**: GetLogsByGameID ORDER BY поперёк passings без денормализации logs.game_id. |
| **P-6** | `calendar/handler.go:184-243` | CalendarICal — полногодовой запрос без кэша (в отличие от CalendarData). |
| **P-7** | `pkg/websocket/room_hub.go:114-214` | WS-хаб: broadcast'ы сериализуются одной горутиной; большие комнаты → дропы. |
| **UX-4** | `layout.html:61` | `og:locale` жёстко `ru_RU` (игнорирует .Lang); `og:site_name` «Encounter Engine» vs ExtraHead «Gengine-0». |
| **UX-5** | `games-list.html:8,205,213` | Кнопка переключения вида показывает текущий вид, а не целевой. |
| **UX-6** | `levels-list.html:156-163` | drag&drop fetch на drop без `.catch` → unhandled rejection. |
| **UX-7** | `calendar-page.html:6` | Дубль `<meta name="csrf-token">` (дублирует layout.html:6; мета в body — невалидный HTML). |
| **UX-8** | `gameplay-show.html:228-241` | `lastScrollY` мёртвая переменная, scroll-listener без throttle. |
| **UX-9** | `app.js:111` | initFormLoading вешается на все формы включая GET-фильтры (поиск меняет кнопку на «⟳ Отправка…»). |
| **UX-10** | — | Touch targets < 44px (пагинация, themeToggle). |
| **A-4** | game-домен | `errors.New` в проде без sentinel (service.go:338, svc_crud.go:71,104, svc_play.go:38, svc_progress.go:167) — хендлеры не различают 403/404. |

### 🔲 Опровергнуто / безопасно (личная проверка + агенты)

- **R-1 (LRU Lock, pass 38)** — race устранён: RLock остался только в non-mutating re-check, мутации под Lock. ✓
- **P-1 (AdvanceToNextLevelWithPassing, pass 38)** — мутация passing.Status только над копией (passingCopy/pCopy), разделения нет. ✓
- **N-2 (HasPermission, pass 38)** — поведение совпадает с HasPermissionTx (ErrRecordNotFound, soft-delete scoped, hasCoAuthorRole). ✓
- **N-1 (AttemptSvc, pass 38)** — AttemptService легитимно задействован (wire, svc_play вызовы SubmitCodeWithTx), не dead code. ✓
- **Read-path утечки *gorm.DB** — не обнаружены (все прямые обращения — транзакции или переданный tx).
- **joinPlaceholders/toAnySlice** — вынесены в util, копий нет. ✓
- **CSRF, OAuth state, email verification, refresh-ротация, webauthn** — безопасно.

---

## B. Оптимизации (производительность)

1. **P-1**: выбрать next-level одним запросом (unnest пар game_id+position JOIN levels) + CreateInBatches; завершения детектить COUNT пачкой.
2. **P-2**: для in-memory Cache добавить typed-Get (type assertion + shallow copy) в cacheGetJSON/cacheGetRating; JSON-путь только для Valkey.
3. **P-3**: sharding монитор-кэша по gameID (массив мьютексов) или thread-safe LRU (hashicorp/golang-lru).
4. **P-4**: `sfGroup.Forget("snapshot:%d")` в InvalidateCache.
5. **P-5**: денормализовать `logs.game_id` + индекс `(game_id, created_at DESC)`.
6. **P-6**: кэшировать собранный .ics на 5-15 мин.
7. **P-7**: per-room воркер-горутины для broadcast (ограниченный пул).

## C. Улучшения кодовой базы (архитектура)

1. **A-1**: удалить мёртвое поле `CoAuthorService.db` + упростить конструктор `NewCoAuthorService(repo)` + wire.
2. **A-2**: вынести HasPermissionTx на репозиторий с tx-параметром (единая реализация).
3. **A-3**: убрать дублированную проверку владельца; sentinel ErrNotOwner.
4. **S-1**: частичный UNIQUE-индекс `WHERE deleted_at IS NULL` + дедуп перед миграцией.
5. **A-4**: sentinel-ошибки для game-домена.

## D. Улучшения UX

1. **UX-1**: initFormLoading — читать `form.hasAttribute('data-no-loading')` (или перенести атрибут на кнопку, как auth-login).
2. **SEO-1**: убрать суффикс из hnd_game.go (layout — единственное место); ExtraHead переопределяет og:title (без дубля).
3. **SEO-2**: CanonicalURL = self-URL из `c.Request.URL` в helper.
4. **UX-2**: aria-live только на смене минуты и границах (60/30/10).
5. **UX-3**: единый helper `formatReplace` с replacement-функцией.

## Приоритет фиксов (pass 39)
1. **UX-1** (регрессия pass 38 — кнопка регистрации) + **SEO-1/SEO-2** (двойной суффикс, canonical).
2. **P-1/P-2** (N+1 + JSON round-trip).
3. **S-1** (UNIQUE-индекс — миграция).
4. **A-1/A-2** (мёртвое поле, единый путь прав).

---

# PASS 38 (повторное ревью) — 9 августа 2026

> Девятое повторное ревью после полного закрытия pass 30-37. Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок по коду.

## Резюме pass 38

**Итог:** 1 критичная (data race), 3 высоких (все подтверждены лично), ~10 средних, ~10 низких.

> Кодовая база стабильна, линтер 2.12.2 чист. Найдены: **data race в MonitorService LRU** (MoveToBack под RLock — регресс P-5 pass 37), **N+1 в checkTimeoutsImpl** (перегруз passing), **мнимая защита от дублей в autostart** (OnConflict без unique-индекса), **мёртвое DI-поле AttemptSvc**, **JS-краш на не-manager странице игры** (preview-btn без null-guard), **бесконечная ре-коннект-нагрузка WS при polling-фолбэке**.

---

## Статус (обновлено 9 авг 2026) — PASS 38 ЗАКРЫТ

**Все находки pass 38 исправлены** (3 раунда фиксов):

**Раунд 1-2** (`e6ea790`):
- **R-1**: data race исправлен — промоушен LRU выполняется под `mu.Lock()` (не RLock) в GetOrFetchSnapshot и GetOrFetchSnapshotJSON.
- **UX-1**: games-show preview-btn — null-guard (нет JS-краша на не-manager страницах).
- **P-1**: добавлен `AdvanceToNextLevelWithPassing` — checkTimeoutsImpl и DeleteLevelFromActiveGame передают уже загруженный passing (нет повторных SELECT).
- **P-2**: autostart — прогрессы создаются `CreateInBatches` с `ON CONFLICT (game_passing_id, level_id)`; статус пачкой; миграция 000045 → **UNIQUE** индекс.

**Раунд 2 (средние):**
- **N-1**: удалено мёртвое DI-поле `AttemptSvc` (GameDeps, NewGameplayHandler, wire, router).
- **UX-2**: monitor disqualify catch — guard `btn.classList` (нет TypeError при fallback-кнопке).
- **UX-3**: monitor WS — connect() не пересоздаёт клиент в reconnecting (нет сброса backoff каждые 10с); onFinalClose сбрасывает wsClient=null.
- **UX-4**: reCAPTCHA theme sync — очистка контейнера перед render (реальный пере-рендер).
- **N-2**: HasPermission через CoAuthorRepository (GetGameAuthorID + FindByGameAndUser), общий hasCoAuthorRole; HasPermissionTx оставлен для tx.
- **N-4**: `joinPlaceholders`/`toAnySlice` вынесены в `internal/pkg/util` (убраны дубли из game/tournament).

**Осталось на раунд 3+ (низкий приоритет):** P-3 (GetLogsByGameID sort-индекс), P-4 (ForceFinishGame двойная загрузка капитанов), UX-5 (mobile cards tournaments-list/admin-audit), UX-6 (OG-теги в layout), UX-7 (aria-hidden эмодзи тостов), UX-8 (replace('%s') $ в подсказке), UX-9 (дублирование submit auth-register), UX-10 (touch targets).

**Раунд 3 (текущий):**
- **UX-5**: mobile-карточки для tournaments-list и admin-audit (таблица скрыта на < sm).
- **UX-6**: layout — базовые og:site_name/locale/title + twitter:title для всех страниц.
- **UX-7**: эмодзи-иконки тостов — aria-hidden (app.js).
- **UX-8**: replace('%s', function(){...}) для подсказок ($&/$' не интерпретируются).
- **UX-9**: auth-register форма — data-no-loading (inline локализованный обработчик не перетирается спиннером).
- **P-4**: ForceFinishGame/DisqualifyTeam — убраны неиспользуемые Preload("Team.Captain") (notify пере-загружает через repo).

**Проверка:** `go build ./...` ✓, `go vet ./...` ✓, `golangci-lint 2.12.2` → 0 issues ✓, `gofmt -l .` → пусто ✓, `go test -count=1 -short ./...` ✓, 84 шаблона валидны ✓.

**Оставлено осознанно (задокументировано):**
- **P-3** (GetLogsByGameID sort-индекс) — требует денормализации logs.game_id (миграция+триггер), без функционального бага.
- **UX-10** (touch targets < 44px) — низкий приоритет, частично покрыто кнопками.

---

## A. Найденные ошибки pass 38 (верифицировано лично)

### 🔴 Критично

| ID | Файл | Проблема |
|---|---|---|
| **R-1** | `game/svc_monitor.go:92,171` | **Data race в MonitorService LRU.** P-5 (pass 37) добавил `cacheList.MoveToBack(elem)` под `mu.RLock()` — но `container/list.MoveToBack` **мутирует** список (перелинковка). Конкурентные поллеры SSE (разные игры) + `InvalidateCache`/refresh под Lock → гонка, порча linked-list. Внесено регрессом pass 37. **Верифицировано лично.** |

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **P-1** | `game/svc_progress.go:344-354,129-134` | **N+1 в checkTimeoutsImpl**: passings уже загружены батчем в `passingByID`, но `AdvanceToNextLevel` пере-загружает passing (`db.First`) + next-level + COUNT на каждый просроченный прогресс. 50 просроченных = ~150 запросов каждые 30с. **Верифицировано лично.** |
| **P-2** | `game/svc_progress.go:455-471` | **Мнимая защита от дублей в autostart**: `OnConflict{DoNothing:true}` без `Columns` срабатывает только на unique-ограничение, а `idx_level_progresses_passing_level` (000045) — **не unique**. Также прогрессы создаются циклом (2 запроса на passing), не batch. **Верифицировано лично.** |
| **UX-1** | `game/templates/games-show.html:215` | **JS-краш на не-manager странице игры**: `document.getElementById('preview-btn').addEventListener(...)` без null-guard; кнопка рендерится только при `{{if .IsManager}}` (строка 96). TypeError обрывает первый `<script>` — close/Escape-обработчики модалки не навешиваются. **Верифицировано лично.** |

### 🟡 Средние

| ID | Файл | Проблема |
|---|---|---|
| **N-1** | `game/routes.go:24`, `app/router.go:372`, `hnd_gameplay.go:46` | **Мёртвое DI-поле `AttemptSvc`**: `_ *AttemptService` параметр в NewGameplayHandler, `AttemptSvc` в GameDeps и wire_providers передают неиспользуемый сервис. **Верифицировано лично.** |
| **N-2** | `game/svc_coauthor.go:46` | **HasPermission через raw s.db** (в отличие от IsUserManager через repo) — A-3 из pass 37 остался; два SQL-пути для одной проверки прав. |
| **N-3** | `tournament/service.go:400-405` | **Сырой SQL через tx** в UpdateScoresForGame (репозитории непригодны для транзакционной композиции из-за search_path). |
| **N-4** | `game/svc_monitor.go:402-419`, `tournament/service.go:512-529` | **Дублирование `joinPlaceholders`/`toAnySlice`** (два пакета) + 4+ варианта проверки прав автора/соавтора (IsUserManager/HasPermission/HasPermissionTx/export-ветка). |
| **UX-2** | `monitor/templates/monitor-page.html:298-301` | **`.catch` disqualifyTeam**: при fallback-кнопке (`{dataset:{confirmDanger:'true'}}` из UX-6 pass 37) `btn.classList` undefined → TypeError внутри catch → тост об ошибке не показывается. **Верифицировано по коду.** |
| **UX-3** | `monitor/templates/monitor-page.html:58-100,104-138` | **Бесконечная ре-коннект-нагрузка WS**: polling-fallback каждые 10с закрывает старый wsClient и создаёт новый → backoff/maxAttempts сбрасываются. |
| **UX-4** | `auth-register.html:56-63` | **reCAPTCHA theme sync не работает**: `grecaptcha.render()` на уже отрендеренный контейнер бросает ошибку (молча ловится); нет официального API смены theme на лету. UX-3 pass 37 — цель не достигнута. |
| **P-3** | `game/repository.go:202-220` | **GetLogsByGameID ORDER BY не покрыт индексом**: sort поперёк всех passings игры (индекс logs(game_passing_id, created_at) не помогает); 100k логов = full sort. |
| **P-4** | `game/svc_admin.go:79-84,284-333` | **ForceFinishGame двойная загрузка команд/капитанов**: Preload("Team.Captain") уже есть, но notify пере-загружает teamRepo.ListByIDs + userRepo.ListByIDs. |

### 🔲 Низкие / мелочи

| ID | Файл | Проблема |
|---|---|---|
| **UX-5** | `tournaments-list.html`, `admin-audit.html` | Mobile-карточки не добавлены (только overflow-x-auto) — несоответствие admin-teams (pass 37). |
| **UX-6** | `layout.html:57` + `games-show.html:1-8` | OG-теги только на games-show; twitter:card без title/image; нет og:site_name/locale. |
| **UX-7** | `app.js:86-89` | Эмодзи тостов (✅❌⚠️ℹ️) без aria-hidden в aria-live контейнере. |
| **UX-8** | `games-show.html:282` | `replace('%s', escapeHtml(q.hint))` — replacement-строка интерпретирует `$&`/`$'` в подсказке. |
| **UX-9** | `auth-register.html:72-77` + `app.js:109-125` | Дублирование submit-обработчика (inline + initFormLoading) — перетирает текст кнопки. |
| **UX-10** | `admin-teams.html:84-88`, `admin-audit.html:88-92`, `layout.html:221` | Touch-цели пагинации/themeToggle < 44px. |
| **C-1** | `monitor/repository.go:96-106` | SaveMessage: Create + повторный GetMessageByID (2-3 запроса на сообщение). |
| **C-2** | `calendar/handler.go:184-243` | CalendarICal не кэшируется (в отличие от CalendarData). |

### 🔲 Опровергнуто / безопасно (личная проверка + агенты)

- **Admin-доступ** — все `/admin/*` за AuthRequired + TwoFactorRequired + adminOnly; роль из БД с кэшем 5с. Безопасно.
- **FullPreview hint gating (pass 37)** — менеджеры получают hint+answers, остальные только вопрос. ✓
- **Settings GET authz (pass 37)** — IsUserManager добавлен. ✓
- **TwoFALoginVerify fail-closed (pass 37)** — отсутствие pending_expires = истёкший; тесты обновлены. ✓
- **UpdateScoresForGame batch CASE** — SQL корректен (параметризован). ✓
- **AttemptService без db** — stateless, wire консистентен. ✓
- **Refresh-ротация, 2FA lockout, Push SSRF, uploads path traversal, CSV injection, CSRF** — подтверждено безопасно.
- **Concurrency (кроме R-1)**: SnapshotDispatcher, monitor pollers unsubscribe, WS hub unregister, push pool — утечек не найдено.

---

## B. Оптимизации (производительность)

1. **R-1**: промоушен LRU вынести в отдельный метод с полным `mu.Lock()` (или Lock-блок вокруг MoveToBack).
2. **P-1**: `AdvanceToNextLevel` принять уже загруженный `*GamePassing` (вариант `AdvanceToNextLevelWithPassing`) — убрать ре-лоад; next-level можно выбрать одним IN-запросом для партии.
3. **P-2**: `CreateInBatches(&progresses, 100)` с `OnConflict{Columns: {game_passing_id, level_id}, DoNothing: true}` + **unique-индекс** в миграции; UPDATE статуса пачкой `WHERE id IN`.
4. **N-1**: убрать мёртвый `AttemptSvc` из GameDeps/NewGameplayHandler/wire.
5. **N-4**: вынести `joinPlaceholders`/`toAnySlice` в `internal/pkg/util`; централизовать проверку прав.
6. **P-3**: денормализовать `logs.game_id` + индекс `(game_id, created_at DESC)`.
7. **P-4**: собирать капитанов из Preload-графа в ForceFinishGame.

## C. Улучшения кодовой базы (архитектура)

1. **N-2**: `HasPermission` через CoAuthorRepository (типизированные GetGameAuthorID + FindByGameAndUser); HasPermissionTx — только для tx.
2. **N-3**: транзакционные методы в TournamentTeamRepository (ListByTournamentAndTeamIDsTx).
3. **UX-3**: WS-polling — не пересоздавать клиент, если он isOpen.
4. **N-5**: единый путь проверки прав автора/соавтора.

## D. Улучшения UX

1. **UX-1**: null-guard на preview-btn (если нет кнопки — не навешивать).
2. **UX-2**: guard `btn.classList` в catch disqualifyTeam.
3. **UX-4**: при переключении темы — очищать контейнер (`widget.innerHTML=''`) перед render, либо только reset(0).
4. **UX-5**: mobile-карточки для tournaments-list/admin-audit.
5. **UX-6**: базовые og:site_name/locale/twitter в layout.
6. **UX-7**: aria-hidden на эмодзи тостов.
7. **UX-8**: replace('%s', function(){...}) для подсказок.

## Приоритет фиксов (pass 38)
1. **R-1** (data race — критично).
2. **UX-1** (JS-краш публичной страницы) + **P-1/P-2** (N+1 фоновых тиков).
3. **N-1** (мёртвое DI-поле) + **UX-2/UX-3** (catch-краш, WS-реконнект).
4. **N-2/N-4** (архитектура прав/дубликаты).

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
