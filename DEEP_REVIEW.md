# Deep Review Gengine-0 — 8 августа 2026 (pass 30 — повторное ревью после закрытия pass 28-29)

## Резюме

Повторное глубокое ревью выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture/DI) с последующей **личной верификацией ключевых находок** по коду.

**Итог:** 0 критичных, 7 высоких, ~20 средних, ~15 низких + 5+ рекомендованных индексов.

> **Контекст:** pass 28-29 закрыты полностью (все ошибки, перф-оптимизации, DI-граф, split god-файлов, pprof/RUM). Этот проход выявляет **новые** проблемы, включая регрессии от недавних рефакторингов (split service.go, formatDate, RUM).

---

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
Документ описывает **находки pass 30**. Код не менялся (read-only). Ключевые находки верифицированы лично (H1, H2, H3, H4, H5, M1, M3, G-12, G-14). Решение: закрывать раундами фиксов, как в pass 28-29.
