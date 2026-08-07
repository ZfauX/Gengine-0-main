# Deep Review Gengine-0 — 7 августа 2026 (pass 28 — полное повторное ревью)

## Резюме

Повторное глубокое ревью выполнено **5 параллельными агентами** (frontend/UX, game/level/tournament/admin, security, performance/DB, tests/architecture) с последующей **личной верификацией всех ключевых находок** (включая опровержение ложных).

**Итог:** 1 критичный баг, 8 высоких, ~25 средних, ~20 низких + 10 UX-предложений + 6 индексов + 4 кэш-стратегии.

⚠️ Важно: G3 (ON CONFLICT game_passings) — **опровергнут**: миграция 000001 уже содержит `UNIQUE(game_id, team_id)`. PF2 (unbounded cache) — **смягчён**: прод задаёт maxSize=10000 явно.

> **Статус на 7 авг 2026: все практические находки закрыты 8 коммитами**
> (`4e7dcc7`, `78ef202`, `cb89e31`, `9a1cbc8`, `ace253d`, `d971465`, `d0cba24`, `b993560`, `ad3e5af`, `d9ed171`, `abaf4fa`, `0478223`, `16a185d`, `2aba00d`).
> Легенда: ✅ исправлено · 🟡 частично/обоснованно оставлено · 🔲 не закрыто (осознанно).

---

## A. Найденные ошибки (верифицировано лично)

### 🔴 Критично

| ID | Статус | Коммит | Файл | Проблема |
|---|---|---|---|---|
| **G1** | ✅ | `b993560` | `game/svc_play.go` | **Тестовые маршруты — backdoor.** `SubmitTestCode`/`SkipLevelTest` теперь требуют `passing.Status == StatusTesting`; +2 теста отклонения реального passing. |

### 🟠 Высокие

| ID | Статус | Коммит | Файл | Проблема |
|---|---|---|---|---|
| **G2** | ✅ | `b993560` | `game/hnd_review.go` | **Фича отзывов ожила:** handler читает `:id` (маршрут `/games/:id/review`). |
| **G4** | ✅ | `b993560` | `tournament/service.go` | **Cross-tournament сброс закрыт:** `RemoveGame` сбрасывает флаги только для команд этого турнира (subquery `tournament_teams`). |
| **G5** | ✅ | `b993560` | `game/svc_photo.go`, `hnd_photo.go` | **Ролевая модель:** удаление чужого фото требует `content_editor`/`moderator` (в сервисе и handler'е). |
| **F1** | ✅ | `b993560` | `i18n/en.go` | **Mojibake** → `"↩ Cancel deletion"`. |
| **F2** | ✅ | `b993560` | `layout.html` | **Stored-XSS закрыт:** `safeUrl()` — только same-origin относительные пути, запрет `javascript:`/`data:`/`//host`. |
| **SEC1** | ✅ | `b993560` | `admin/service.go` | **Backup confinement:** `Download` проверяет путь внутри `BackupDir` через `filepath.Rel`. |
| **SEC2** | ✅ | `b993560` | `app/uploads.go` + `router.go` | **Uploads под контролем:** кастомный handler — avatars публично, covers/photos по видимости игры, answers только участникам/менеджерам; path-traversal защита. |
| **F3** | ✅ | `b993560` | `i18n/ru.go`, `en.go` | **Ключ `coauthor.delete_confirm`** добавлен; D5-автотест не допустит рецидива. |

### 🟡 Средние (выборка)

| ID | Статус | Коммит | Файл | Проблема |
|---|---|---|---|---|
| G6 | ✅ | `ad3e5af` | `level/service.go` | `Type` сохраняется при частичном POST (guard как у Position); +тест. |
| G7 | ✅ | `ad3e5af` | `game/svc_admin.go` | `DeleteLevelFromActiveGame` удаляет answers→questions→minigame в FK-порядке; +тест. |
| G8 | ✅ | `ad3e5af` | `game/svc_play.go` | `AcceptBlackboxAnswer` разрешает автору **и** модератору (`HasPermissionTx RoleModerator`). |
| F4 | 🔲 | — | `gameplay-show.html` | Классификация ошибок по русским regex. Низкий риск (внутренние коды, не UX-критично) — не трогалось. |
| F5 | ✅ | (было раньше) | `layout.html` | `applyTheme` синхронизирует `aria-pressed` при загрузке — проверено. |
| F6 | ✅ | `d9ed171` | `games-show.html` | `&times;` получил `aria-label` (`common.close`). |
| F7 | ✅ | `d9ed171` | `monitor-page.html` | `escapeHtml` для `total_time`/`current_level` — консистентно. |
| F8 | ✅ | `ad3e5af` | `sw.js` | `notificationclick` открывает только same-origin относительные URL. |
| F10 | ✅ | `ad3e5af` | `i18n/ru.go` | Мёртвый ключ `%d игр(ы)` удалён. |
| F12 | ✅ | `ad3e5af` | `i18n/ru.go`, `en.go` | `%w` → `%v` в 40 ключах (Sprintf не умеет `%w`). |
| SEC3 | ✅ | `4e7dcc7` | `storage/local_storage.go` | `Delete` использует `filepath.Rel` (точная граница) + корректная обработка Unix-абсолютов. |
| SEC4 | 🟡 | — | `game/svc_review.go` | Sanitize на write — безопасно; защита от будущих `template.HTML` отложена (нет сценария). |
| SEC5 | ✅ | `ad3e5af` | `audit`, `admin/handler`, `team/repository` | ILIKE-экранирование через `BuildLikePattern` (в автокомплите уже было). |
| G9-G13 | ✅ | `ad3e5af` | game/level | G10 (Duplicate+MiniGame), G11 (`g.Wait` log), G12 (FOR UPDATE — уже было), parse-ошибки проверены. |
| PF9 | 🔲 | — | `notification/service.go` | Push в goroutine с `WithoutCancel` — отложено (VAPID обычно не настроен; риск не воспроизводится в тестах). |
| PF5/PF6 | ✅ | `ad3e5af` | миграция `000037` | 6 индексов (leaderboard, chat, timeout partial, listing ×2, reviews). |
| PF10 | 🔲 | — | `svc_progress.go` | Batch `GROUP BY game_id` для автостарта — отложено (работает корректно, только perf). |
| A4 | 🔲 | — | `auth_handler.go` | Дублирование render-блоков ошибок — стайл, отложено. |
| A6 | ✅ | `0478223` | `render/localize.go` | 3 новые ошибки hинт/теста в `errKeyMap` (локализация). |
| T1 | ✅ | `abaf4fa` | `render/helper.go` | `SetTemplateForTest` + `t.Cleanup` в 3 тест-файлах. |
| T2 | 🔲 | — | `svc_snapshot_test.go` | `time.Sleep` — отложено (не флакает на CI сейчас). |
| T4 | 🔲 | — | тесты | Проверки на русские строки — осознанно (отражают sentinel-контракт). |

### 🟢 Опровергнутые (проверено — НЕ баги)
- **G3** — `ON CONFLICT (game_id, team_id)` без unique index: **миграция 000001 уже содержит `UNIQUE(game_id, team_id)`**.
- **PF2** — unbounded cache: **прод задаёт maxSize=10000**.
- **S1** (прошлый security): `.env`/секреты в git — **не отслеживаются** (подтверждено `git ls-files`).

---

## B. Оптимизации

### Индексы — ✅ реализованы в миграции `000037` (`ad3e5af`)
```sql
CREATE INDEX IF NOT EXISTS idx_tournament_results_tournament_score
    ON tournament_results(tournament_id, score DESC);                    -- leaderboard (PF5) ✅
CREATE INDEX IF NOT EXISTS idx_chat_messages_room_created
    ON chat_messages(room_id, created_at DESC);                          -- чат (PF6) ✅
CREATE INDEX IF NOT EXISTS idx_level_progresses_unfinished_started
    ON level_progresses(started_at) WHERE finished_at IS NULL;          -- timeout worker (PF7) ✅
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_name
    ON games(is_draft, visibility, name);                                -- листинг ✅
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_starts
    ON games(is_draft, visibility, starts_at);                           -- календарь/листинг ✅
CREATE INDEX IF NOT EXISTS idx_reviews_game_created
    ON reviews(game_id, created_at DESC);                                -- отзывы ✅
```

### Кэш
| Пункт | Статус | Коммит | Примечание |
|---|---|---|---|
| **Reviews** (`reviews:game:%d`) | ✅ | (было раньше) | `ListByGame` + `GetAverageRating` кэшируются, инвалидация в `Create`. |
| **Versioned listing key** (`games:list:vN:*`) | ✅ | `d9ed171` | `games:list:version` — O(1) инвалидация вместо `DeleteByPrefix` SCAN. |
| **Snapshot attempts окно 1 час** | ✅ | `d9ed171` | Подзапрос `attempts` ограничен `created_at >= NOW()-interval '1 hour'`. |
| **Push в goroutine** (PF9) | 🔲 | — | Отложено — VAPID редко включён, не воспроизводится. |

### Прочее
- **Dead code** `CompleteLevelWithSSE`/`AdvanceToNextLevelWithSSE` — ✅ удалены (`ad3e5af`).
- Проверять `IsUserManager` только для draft/private/админ (PF13) — 🟡 не выделялось отдельно (в uploads-handler'е логика уже per-категория).
- Дубли индексов (000007/000008, 000013/000036) — 🔲 оставлены (пересечение не мешает, удаление рискованно).

---

## C. Улучшения пользовательского опыта

| # | Предложение | Статус | Коммит | Примечание |
|---|---|---|---|---|
| 1 | **Skeleton loaders** | ✅ | `16a185d` | monitor (3 карточки) + chat (5 полос), авто-удаление при данных. |
| 2 | **Optimistic chat** | ✅ | `16a185d` | Мгновенный пузырь + дедупликация echo через `recentSends`. |
| 3 | **Клавиатурные шорткаты** | 🟡 | `16a185d` | `/` и `n` на списке игр; Esc уже был; `?` (карта) не добавлен (нет глобального контекста). |
| 4 | **Empty states с CTA** | 🟡 | `abaf4fa` | Календарь — CTA «Создать игру» для авторизованных; монитор — без CTA (семантика просмотра). |
| 5 | **Мобильная полировка** | ✅ | `0478223` | `:active` pressed-state для `hover-lift` карточек. |
| 6 | **Dark mode sweep** | ✅ | `abaf4fa` | Confirm-модалка в app.js + пагинация games-list. |
| 7 | **Print styles** | ✅ | `0478223` | `@media print` — nav/footer/модалки скрыты. |
| 8 | **A11y focus** | ✅ | (было раньше) | `trapModalFocus` + `showModalConfirm` + delete modal. |
| 9 | **Локализованное время** | ✅ | `0478223` | `formatDate`/`formatDateTime` в 9 шаблонах (ru/en). |
| 10 | **404-страница** | ✅ | `0478223` | Поиск по играм + ключ `error.search_label`. |

---

## D. Архитектурные улучшения (кодовая база)

| # | Предложение | Статус | Коммит | Примечание |
|---|---|---|---|---|
| 1 | **Слоистость / repository-интерфейсы** | 🟡 | `16a185d`, `2aba00d` | NotificationService полностью переведён на `NotificationRepository` (10 методов). Высокорисковые потоки (`CalculateResults`, `UpdateRatingsForGame`, `checkTimeoutsImpl`) уже покрыты интеграционными тестами; export-import-транзакция осознанно не тронута. |
| 2 | **Разделение god-классов** | ✅ | `2aba00d` | `RefreshTokenService` выделен из `AuthService` (интерфейс `AccessTokenGenerator`). |
| 3 | **Единый error-контракт** | ✅ | `0478223` | 3 ошибки hint/test добавлены в `errKeyMap`; sentinel-паттерн уже был. |
| 4 | **DI manifest** | ✅ | `abaf4fa` | `GamePassingService`, `TwoFactorService`, `RefreshTokenService` в `services`; wire перегенерирован. |
| 5 | **Автотест i18n** | ✅ | `abaf4fa` | `TestAllUsedKeysExistInBothDictionaries` сканирует шаблоны+Go; поймал `common.characters` и несуществующий webauthn-ключ. |
| 6 | **Тесты** | ✅ | `abaf4fa`, `16a185d` | 2FA-тесты разблокированы (2), `render.SetTemplate` → `t.Cleanup`, +тест `UpdateRatingsForGame`. |

---

## Приоритет фиксов (исторически)

1. **G1** — backdoor через test-маршруты → ✅ `b993560`.
2. **G2** / **F1** / **F3** → ✅ `b993560`.
3. **G4/G5** / **SEC1/SEC2** → ✅ `b993560`.
4. **F2** — XSS в layout → ✅ `b993560`.
5. Индексы + кэш reviews + dead code → ✅ `ad3e5af`, `d9ed171`.

---

## Статус (актуально)

**Все практические находки pass 28 закрыты.** Работа велась раундами:
1. `4e7dcc7` — storage `Delete()` Unix-пути (SEC3) · `78ef202` — testDSN fallback · CI-fixes (docker)
2. `b993560` — G1-G5, SEC1/SEC2, F1-F3 + тесты
3. `ad3e5af` — G6-G10, F8/F10/F12, SEC5, индексы, dead code
4. `d9ed171` — PF1/PF3, F6/F7
5. `abaf4fa` — D4 (DI), D5 (i18n-автотест), D6 (t.Cleanup), 2FA-тесты, C4/C6
6. `0478223` — C7/C9/C10/C5, D3 (error-контракт)
7. `16a185d` — C1/C2/C3b, тест UpdateRatingsForGame
8. `2aba00d` — D2 (RefreshTokenService), D1 (NotificationRepository)

Осознанно оставлены 🔲: F4, SEC4, PF9, PF10, A4, T2/T4 (низкий риск / не воспроизводится / стайл) и крупная export-импорт-транзакция (риск > выгода при существующем тестовом покрытии).
