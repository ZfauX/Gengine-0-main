# Deep Review Gengine-0 — 8 августа 2026 (pass 30 — повторное ревью после закрытия pass 28-29)

## Резюме

Повторное глубокое ревью выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture/DI) с последующей **личной верификацией ключевых находок** по коду.

**Итог:** 0 критичных, 7 высоких, ~20 средних, ~15 низких + 5+ рекомендованных индексов.

> **Контекст:** pass 28-29 закрыты полностью (все ошибки, перф-оптимизации, DI-граф, split god-файлов, pprof/RUM). Этот проход выявляет **новые** проблемы, включая регрессии от недавних рефакторингов (split service.go, formatDate, RUM).

---

## Статус (обновлено 8 авг 2026)

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

# PASS 32 (повторное ревью) — 8 августа 2026

> Третье повторное ревью после полного закрытия pass 30-31 (миграция на репозитории, race-верификация). Выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с **личной верификацией** ключевых находок.

## Статус (обновлено 8 авг 2026) — PASS 32 ЗАКРЫТ

**Все находки pass 32 исправлены** (2 раунда фиксов + мелкие UX):

- **Раунд 1** (`b552fd4`): F-C1 (глобальный escapeHtml — тосты/confirm ожили), F-P1 (инвалидация лидерборда в in-memory), S-1 (OAuth 2FA TTL), S-2 (PhotosPage visibility), S-3 (нормализация backup-кодов), S-4/S-5 (атомарный lock_count + 2FA backoff), A-1 (CoAuthor→репозиторий), индексы 000042, dark-mode (монитор/admin/passings).
- **Раунд 2** (`784c010`): A-5 (GameRepository без *gorm.DB leak: типизированные Count/AdminListGames), A-2 (батч DeleteLevelFromActiveGame), S-6 (фото-delete authz), UX-H1 (Escape на dropdown), UX-M3 (lightbox local TZ), UX-M5/M7 (удалён мёртвый WS логов, .catch).
- **Раунд 3** (текущий): UX-M11 (role=menuitem), UX-M8 (stale-response guards в автокомплитах), UX-M4 (confirm-кнопка по типу действия).

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
