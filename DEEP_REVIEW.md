# Deep Review Gengine-0 — 7 августа 2026 (pass 29 — повторное ревью после закрытия pass 28)

## Резюме

Повторное глубокое ревью выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с последующей **личной верификацией всех ключевых находок** (включая опровержение).

**Итог:** 1 критичный баг, 5 высоких, ~15 средних, ~20 низких + оптимизации (4+ индексов, 4 кэш/запросные стратегии) + UX/архитектурные предложения.

> **Контекст:** pass 28 (прошлый отчёт) закрыт полностью. Этот проход выявляет **новые** проблемы, часть из которых появилась/подтвердилась после прошлых фиксов. Все найденные уязвимости уровня 0-1 проверены лично по коду.

---

## A. Найденные ошибки (верифицировано лично)

### 🔴 Критично

| ID | Файл | Проблема |
|---|---|---|
| **CRIT-1** | `user/templates/user-2fa-enable.html:16` + `two_factor_service.go:43-53` | **Утечка TOTP-секрета третьей стороне.** `QRURL` = `otpauth://totp/...?secret=<S>` вставляется в `<img src="https://api.qrserver.com/v1/create-qr-code/?data={{.QRURL|urlquery}}">`. Секрет 2FA уходит на внешний сервис через URL (попадает в их логи/прокси/HTTP-рефереры) — де-факто компрометация 2FA. Проверено: `GenerateQRCodeURL` возвращает `key.URL()` (содержит secret). |

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **HIGH-1** | `user/webauthn_handler.go:128` | **2FA step-up в WebAuthn сломан (функциональный баг).** `set2FAVerified` пишет `int64`-таймстамп, а проверка `sess.Get(session2FAKey(userID)) != true` сравнивает с `bool` → условие **всегда истинно** → пользователь с включённой 2FA **никогда** не может зарегистрировать passkey (всегда 403). Подтверждено чтением `two_factor_middleware.go:30-47` (там корректный `is2FAVerified` через switch по типу). |
| **HIGH-2** | `user/templates/profile-show.html:26`, `profile-public.html:13` | **Сломанная инициала аватара для кириллицы.** `{{slice .User.Name 0 1}}` режет по байтам: для «Андрей» → первый байт `0xD0` (незавершённая UTF-8-руна) → рендерится `�`. Для русскоязычной аудитории аватар всегда битый. |
| **HIGH-3** | `static/css/output.css` (собранный) vs `static/css/app.css:108` | **Контраст ошибок форм не AA.** В `output.css` `.field-error{color:#ef4444}` (3.86:1, нужно 4.5:1); исходник `app.css` содержит `#dc2626` (4.5:1) — сборка рассинхронизирована (stale `make css`). |
| **HIGH-4** | `monitor/routes.go:183` + `monitor/handler.go:989-1041` | **`/voting/vote` — оракул по кодам ответа.** Ошибка «вы не являетесь участником этой команды» возвращается как **400 BadRequest**, а не 403. По разнице кодов/сообщений можно перечислять session_id/team_id и проверять существование голосований/команд. Доступ защищён в сервисе (`service.go:133-139`), но сигнатура кода ошибочна. |
| **HIGH-5** | `user/service_test.go:517-519` | **`OAuthService.Authenticate` без тестов** — пустой `t.Skip`. Security-critical путь (линковка аккаунтов, создание по email, race 23505, VK emailVerified). Нет `httptest`-моков OAuth-эндпоинтов. |

### 🟡 Средние (выборка)

| ID | Файл | Проблема |
|---|---|---|
| MED-1 | `pkg/sqlutil/sqlutil.go:121-134` | `EscapeLike` не экранирует сам backslash `\`. Поиск с завершающим `\` даёт паттерн с «висячим» escape → ошибка/неверные результаты; `\%` даёт ложные совпадения. |
| MED-2 | `admin/templates/admin-audit.html:76,79,88,92`, `admin-users.html`, `admin-games.html` | Пагинация/фильтры без `| urlquery`: `?query={{$.Query}}` — user-ввод; можно подменить `role`/`status` (параметровая инъекция в href). |
| MED-3 | `notification/service_test.go:323-329` | Мёртвый тест — двойной `t.Skip`; `GetByUser` (пагинация/дефолты) не покрыт ни одним исполняемым тестом. |
| MED-4 | `tournament/service.go`, `monitor/service.go` | **Строковые ошибки вместо sentinel** (`errors.New("только автор...")`) — хендлеры различают «нет прав»/«не найдено»/«уже сделано» сравнением строк (хрупко при локализации). |
| MED-5 | `layout.html:113-148` | ~28 мёртвых `data-i18n-*` атрибутов (не читаются `tI18n`) — ~2 КБ на страницу, признак незавершённых фич (draft-автосейв, click-to-copy, ранговые тосты). |
| MED-6 | `games-photos.html:50-54,194-221` | Lightbox фото не `role="dialog"`/`aria-modal`; `closePhotoModal` не возвращает фокус на `.photo-thumb` (в отличие от других модалок). |
| MED-7 | `monitor-page.html:262-278` | Дисквалификация: нет обратной связи при успехе, кнопка не блокируется от повторного клика (двойной POST). |
| MED-8 | `profile-show.html:225-270` | Мёртвый JS-блок настроек уведомлений (элементов на странице нет, фича переехала). |
| MED-9 | `games-list.html:236` | Лишний `fetch('/api/users/preferences/games-view')` для анонимов (всегда 401+fallback); можно рендерить серверно (уже так и есть). |
| MED-10 | `monitor-page.html:202` | `data-team-id="${team.team_id}"` в innerHTML-атрибуте без `escapeHtml` (числовое сейчас — будущий риск при расширении формата). |
| MED-11 | `gameplay-show.html:280`, `webauthn-login-button.html:55,59` | `window.location.href = r.url/data.redirect` без same-origin проверки (сейчас серверные значения безопасны, но нет защиты в глубину). |
| MED-12 | `export/service.go:85-601` | 5 методов Export (CSV/PDF/Excel) без тестов — самые объёмные и ломкие. |
| MED-13 | `calendar/handler.go:165,227,236` | `CalendarICal`/`escapeICalText`/`sanitizeHost` без тестов (экранирование iCal — типичный источник багов). |
| MED-14 | `calendar/handler_test.go:322-324,407-409` | Integration-тесты с маскирующим `t.Skip("no events returned...")` — пусто «зелёные» при регрессии. |
| MED-15 | `social/service.go:12` | Мёртвый sentinel `ErrNotFollowing` — объявлен, нигде не возвращается. |

### 🟢 Опровергнутые (проверено — НЕ проблемы)
- **`make test-short` без БД** — `testutil/postgres.go:49-51` корректно делает `t.Skip` при `testing.Short()`. Все доменные тесты с `SetupPostgresDB` скипаются в short — соответствует документации.
- **Path traversal (uploads/backup)** — `uploads.go normalizeUploadPath` + category-whitelist + `LocalStorage` (IsAbs/`..`/Rel) — корректно.
- **SQL injection (ORDER BY)** — whitelist в `svc_listing.go:138-163` + `sqlutil.AddOrder` — корректно.
- **CSRF / OAuth state / JWT / refresh-ротация** — defense-in-depth работает (origin-guard + SameSite + constant-time compare + family-revoke).
- **Refresh-токены в БД** — SHA-256 hash, атомарный ClaimAndCreate, revoke семьи при reuse/fingerprint — корректно.

---

## B. Оптимизации

### Индексы (рекомендованные CREATE INDEX — отсутствуют в миграциях 000001–000037)
```sql
CREATE INDEX IF NOT EXISTS idx_external_logins_provider_external
    ON external_logins(provider, external_id);                 -- OAuth FindOrCreate (user/repository.go:526)
CREATE INDEX IF NOT EXISTS idx_external_logins_user_id
    ON external_logins(user_id);                               -- DELETE при удалении пользователя (repository.go:374)
CREATE INDEX IF NOT EXISTS idx_player_ratings_score
    ON player_ratings(score DESC);                             -- лидерборд ORDER BY score (svc_rating.go:197)
CREATE INDEX IF NOT EXISTS idx_teams_name_trgm
    ON teams USING gin (name gin_trgm_ops);                    -- админ-поиск ILIKE (team/repository.go:142,156)
-- опционально:
CREATE INDEX IF NOT EXISTS idx_level_progresses_passing_level
    ON level_progresses(game_passing_id, level_id);            -- Vote attempts-subquery (monitor/service.go:155)
CREATE INDEX IF NOT EXISTS idx_game_passings_game_status_unscored
    ON game_passings(game_id, status) WHERE tournament_scored = false; -- UpdateScoresForGame (tournament/service.go:354)
```

### Запросы / кэш
| ID | Файл | Рекомендация |
|---|---|---|
| PF-1 | `game/svc_monitor.go:163-272` | Тяжёлая CTE снапшота на активную игру каждые ~30с: трёхуровневый `IN` по attempts → прямой JOIN через `level_progresses`; `EXPLAIN ANALYZE` перед изменениями. |
| PF-2 | `notification/service.go:268-294` | Синхронный web-push в request-path (сумма RTT по подпискам блокирует хендлер) → асинхронный worker/errgroup с bounded concurrency; 404/410-чистка в фоне. |
| PF-3 | `user/profile_service.go:43-78` | `GetPublicProfileStats` — 4 запроса (2 тяжёлых COUNT) на каждый публичный профиль → один SQL + кэш 1–5 мин. |
| PF-4 | `game/service.go:254-310` | После cache-hit `GetByID` всё равно `CanViewGame` → `IsUserManager` (2 запроса). Для `public && !is_draft` возвращать из кэша без проверки; для приватных — кэшировать результат проверки (TTL 30–60с). |
| PF-5 | `game/svc_progress.go:349-395` | `checkAutoStartGamesImpl` — N+1 (COUNT + FOR UPDATE + InitFirstLevel на каждое passing) → батч по game_id. |
| PF-6 | `admin/handler.go:105-149` | 5 последовательных COUNT на дашборд → errgroup/параллельно или кэш 1 мин. |
| PF-7 | `game/svc_review.go:115-124` | Дублирующий некэшируемый `ReviewService.GetAverageRating` (мимо `RatingService` с кэшем) — проверить вызовы и делегировать. |
| PF-8 | `level/service.go:187-205`, `tournament/service.go:112-126,419-426` | Поодиночные INSERT при Duplicate / AddGame / UpdateScores → bulk insert / batch upsert (unnest). |
| PF-9 | `game/service.go:558-584` | Полный COUNT логов на каждую страницу → окно/приблизительный count. |
| PF-10 | `game/svc_simulate.go:51-74`, `export/service.go` | Блокирующие sleep (simulate) и синхронные PDF/Excel в request-path → фоновый расчёт + статус. |

---

## C. Улучшения пользовательского опыта

1. **Инициал аватара рун-безопасно** (HIGH-2): вычислять на Go (`[]rune`), передавать в контекст — для русских имён.
2. **QR 2FA локально** (CRIT-1): генерировать QR на клиенте (qrcode.js локально) или Go-библиотекой — не передавать `otpauth://` наружу.
3. **Labels для полей ввода**: `gameplay-show.html:49` (файл), `notes-manage.html:9`, `errors-404.html:18`, админ-поиски — `aria-label` или скрытый `<label class="sr-only">`.
4. **Контраст field-error** (HIGH-3): перегенерировать `make css` (в `app.css` уже `#dc2626`).
5. **Lightbox фото как dialog** (MED-6): `role="dialog" aria-modal` + focus restore на thumb.
6. **Обратная связь при дисквалификации** (MED-7): disabled на время запроса + toast успеха.
7. **Убрать мёртвый JS/атрибуты** (MED-5, MED-8): удалить неиспользуемые `data-i18n-*` и блок настроек уведомлений в профиле.
8. **`urlquery` в админ-пагинации** (MED-2): `?query={{$.Query|urlquery}}` — защита от параметровой инъекции и правильная фильтрация.
9. **Same-origin проверка redirect** (MED-11): `redirect:'manual'` + `new URL(r.url).origin === location.origin`.
10. **Дедупликация optimistic chat** — уже есть; можно распространить на командный чат, если отличается.
11. **Локализация fallback в JS** (из аудита UX): русские fallback в `tI18n` перевести на EN; русские console-строки — в логи.
12. **`prefers-reduced-motion`** для `scrollIntoView` (calendar/wizard).

---

## D. Архитектурные улучшения (кодовая база)

1. **DI-полнота** (из аудита): сервисы строятся вручную в routes (notification, export, social, admin, monitor, calendar), репозитории дублируются (`team.NewGormTeamRepo` в админке, `TwoFactorService` второй раз). Перевести в wire — один граф.
2. **Sentinel-ошибки** (MED-4): ввести `ErrForbidden/ErrAlreadyExists/...` + `errors.Is` в хендлерах; локализация — только в render-слое.
3. **`user/service.go` — god-файл** (992 строки, 7 сервисов): разбить на `auth_service.go`, `oauth_service.go`, `password_reset_service.go`, `email_verification_service.go`, `dashboard_service.go` (pattern уже задан `refresh_token_service.go`/`two_factor_service.go`).
4. **D1 продолжение**: export-импорт-транзакция и tournament/monitor raw SQL — выносить в репозитории постепенно, начиная с высокорисковых веток.
5. **Дублирование моков**: `calendar/handler_test.go:30-127` и `notification/service_test.go` — ручные моки вместо сгенерированных `go.uber.org/mock`.
6. **Игнорирование ошибок в тестах**: `tournament/service_test.go:99,125-129`, `team/service_test.go:47-213`, `monitor/service_test.go:104-208` — `_`/`db.Create` без `require.NoError` → тесты «зелёные» при поломке БД-вставок.
7. **Хелперы тестов**: `createUser/createGame/createTeam/intPtr` дублируются в 6+ пакетах → вынести в `internal/testutil`.
8. **BackupService.Download** — условие `rel == ".." || len(rel)>=3 && rel[:3]==".."+sep` без скобок; добавить тест на `../evil`.

---

## Приоритет фиксов
1. **CRIT-1** — утечка TOTP-секрета (QR на стороннем сервисе) — срочно.
2. **HIGH-1** — сломанная регистрация passkey при 2FA (функциональный баг).
3. **HIGH-2 / HIGH-3** — кириллическая инициала + контраст (мелкие, но на виду).
4. **HIGH-4** — 400 вместо 403 в /voting/vote (оракул).
5. Индексы (секция B); **PF-2** (webpush async); **MED-3/14** (мёртвые тесты-скипы).

## Статус
Документ описывает **находки pass 29**. Код не менялся в рамках этого ревью (read-only). Находки верифицированы лично (CRIT-1, HIGH-1..5, MED-1, MED-2, HIGH-3, индексы, `testutil`-short). Решение: показать отчёт, затем закрывать раундами фиксов (как в pass 28).
