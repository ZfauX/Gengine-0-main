# Deep Review Gengine-0 — 7 августа 2026 (pass 28 — полное повторное ревью)

## Резюме

Повторное глубокое ревью выполнено **5 параллельными агентами** (frontend/UX, game/level/tournament/admin, security, performance/DB, tests/architecture) с последующей **личной верификацией всех ключевых находок** (включая опровержение ложных).

**Итог:** 1 критичный баг, 8 высоких, ~25 средних, ~20 низких + 10 UX-предложений + 6 индексов + 4 кэш-стратегии.

⚠️ Важно: G3 (ON CONFLICT game_passings) — **опровергнут**: миграция 000001 уже содержит `UNIQUE(game_id, team_id)`. PF2 (unbounded cache) — **смягчён**: прод задаёт maxSize=10000 явно.

---

## A. Найденные ошибки (верифицировано лично)

### 🔴 Критично

| ID | Файл | Проблема |
|---|---|---|
| **G1** | `game/svc_play.go:501,557` + `hnd_gameplay.go:505,583` | **Тестовые маршруты — backdoor в реальные игры.** `SubmitTestCode`/`SkipLevelTest` проверяют только `IsUserManager`, но **не проверяют `passing.Status == StatusTesting`**. Автор/модератор игры может по passingID реально стартовавшей команды создать `Attempt{Success:true}` + вызвать `CompleteLevel` (завершить её уровень) или пропустить уровень. |

### 🟠 Высокие

| ID | Файл | Проблема |
|---|---|---|
| **G2** | `game/hnd_review.go:41,72,103` | **Фича отзывов мертва.** Маршрут зарегистрирован как `/:id/review`, но handler читает `c.Param("game_id")` — параметра нет → `gameID=0` → всегда 403 «Вы не можете оставить отзыв». Тихо (без лога). |
| **G4** | `tournament/service.go:194-198` | **Cross-tournament сброс.** `RemoveGame` сбрасывает `tournament_scored=false, tournament_points=0` для ВСЕХ passings игры, не ограничиваясь командами этого турнира. Игра в двух турнирах → двойное начисление очков в другом. |
| **G5** | `game/svc_photo.go:61-69` | **Нарушение ролевой модели.** Любой соавтор (даже `observer`) может удалить фото игрока — проверка `co.Role` игнорируется. |
| **F1** | `i18n/en.go:757` | **Mojibake** в английской строке: `"game.show_delete_undo": "в†© Cancel deletion"` (вместо `"↩ Cancel deletion"`). Виден каждому англоязычному пользователю модалки удаления игры. |
| **F2** | `user/templates/layout.html:582` | **Stored-XSS-паттерн.** `n.url` вставляется в `href` без `escapeHtml` (title/message экранируются). Если URL уведомления станет контролируемым пользователем — атрибут-инъекция/`javascript:`-вектор. |
| **SEC1** | `admin/service.go:110-118` | **Backup path без confinement.** `Download` отдаёт `backup.FilePath` из БД как есть, без проверки, что он внутри `BackupDir`. Защита defense-in-depth (нужен доступ к БД), но легко закрыть. |
| **SEC2** | `app/router.go:253` + `storage/local_storage.go:126` | **Публичная раздача всех uploads.** `/uploads` — `r.Static`, без проверки видимости игры/принадлежности; предсказуемые имена `{userID}_{unixNano}.ext`. Фото приватных игр и файлы-ответы команд (данные решений) доступны анониму по перебору. |
| **F3** | `game/templates/co_authors-manage.html:16` | **Отсутствующий i18n-ключ** `coauthor.delete_confirm` — диалог подтверждения показывает сам ключ. Тест `TestAllKeysHaveEN` покрывает только словарь↔словарь, не шаблоны. |

### 🟡 Средние (выборка)

| ID | Файл | Проблема |
|---|---|---|
| G6 | `level/service.go:123` | `Level.Type` перезаписывается пустой строкой при частичном POST (нет guard как у Position) — ломает ветвление геймплея. |
| G7 | `game/svc_admin.go:223-229` | `DeleteLevelFromActiveGame` hard-deletes уровень, но вопросы/ответы остаются (soft-delete) → сироты; история finished-progress чистится целиком. |
| G8 | `game/svc_play.go:353-363` | `AcceptBlackboxAnswer` требует членства в команде до проверки авторства — автор/модератор вне команды не может принять blackbox-ответ. |
| F4 | `game/templates/gameplay-show.html:299` | Классификация ошибок по русским regex (`/код|ответ/`) — в EN UI мис-классификация. Лучше `X-Error-Code`. |
| F5 | `layout.html:194,220` | `aria-pressed` темы жёстко `false` до JS — screen-reader получает неверное состояние. |
| F6 | `games-show.html:123,133` | Кнопки закрытия `&times;` без `aria-label`. |
| F7 | `monitor-page.html:168-181` | Несогласованное экранирование полей в innerHTML. |
| F8 | `sw.js:145-168` | `notificationclick` открывает `data.url` без проверки same-origin → фишинговый вектор. |
| F10 | `ru.go:1312` | Плюрализация хак `%d игр(ы)` — неграмотно для русского. |
| F12 | `ru.go:65-84` | `%w` в сообщениях, передаваемых в `T()/TF()` (fmt.Sprintf не понимает %w) → `%!w(...)`. |
| SEC3 | `storage/local_storage.go:205-218` | `Delete`: prefix-match containment обходится sibling-директориями; при `baseDir==""` проверка пропускается вовсе. |
| SEC4 | `game/svc_review.go:66` | Sanitize только на write (StrictPolicy) — безопасно сейчас, но хрупко для будущих `template.HTML`. |
| SEC5 | `game/hnd_autocomplete.go:50-56` | `ILIKE %q%` без экранирования `%`/`_` — перечисление результатов (лимитер 20/мин сглаживает). |
| G9-G13 | game/level | Мёртвые ветки, Duplicate без MiniGame, `_ = g.Wait()`, UseHint без FOR UPDATE, игнор parse-ошибки. |
| PF9 | `notification/service.go:238-304` | Синхронные HTTP-вызовы push-провайдеру в request-пути — блокирует обработчик. |
| PF5/PF6 | tournament/monitor | Filesort на leaderboard (`score DESC`) и чате (`created_at DESC`) без составных индексов. |
| PF10 | `svc_progress.go:421-452` | N+1-подобные циклы в фоновых воркерах (COUNT per game). |
| A4 | `user/auth_handler.go` | Дублирование render-блоков ошибок в Login/Register/Forgot/Reset — helper. |
| A6 | user/game | Непоследовательная модель ошибок: русские строки в services vs sentinel vs apperrors. |
| T1 | тесты | `render.SetTemplate(tmpl)` мутирует глобальный шаблон — риск interference. |
| T2 | `svc_snapshot_test.go`, `client_test.go` | `time.Sleep` в тестах — флаки. |
| T4 | тесты | Проверки на русские строки ошибок — хрупко при смене i18n. |

### 🟢 Опровергнутые (проверено — НЕ баги)
- **G3** — `ON CONFLICT (game_id, team_id)` без unique index: **миграция 000001 уже содержит `UNIQUE(game_id, team_id)`**.
- **PF2** — unbounded cache: **прод задаёт maxSize=10000**.
- **S1** (прошлый security): `.env`/секреты в git — **не отслеживаются** (подтверждено `git ls-files`).

---

## B. Оптимизации

### Индексы (рекомендованные CREATE INDEX)
```sql
CREATE INDEX IF NOT EXISTS idx_tournament_results_tournament_score
    ON tournament_results(tournament_id, score DESC);                    -- leaderboard (PF5)
CREATE INDEX IF NOT EXISTS idx_chat_messages_room_created
    ON chat_messages(room_id, created_at DESC);                          -- чат (PF6)
CREATE INDEX IF NOT EXISTS idx_level_progresses_unfinished_started
    ON level_progresses(started_at) WHERE finished_at IS NULL;          -- timeout worker (PF7)
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_name
    ON games(is_draft, visibility, name);                                -- листинг
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_starts
    ON games(is_draft, visibility, starts_at);                           -- календарь/листинг
CREATE INDEX IF NOT EXISTS idx_reviews_game_created
    ON reviews(game_id, created_at DESC);                                -- отзывы на странице игры
```

### Кэш
- **Reviews**: кэшировать `reviews:game:%d` (короткий TTL), инвалидировать в `svc_review.go` — сейчас страница игры каждый раз тянет отзывы через `GetStats` (PF4).
- **Versioned listing key** `games:list:v1:*` вместо `DeleteByPrefix("games:list:")` (PF3): инвалидация O(1) вместо полного Valkey SCAN на каждый write.
- **Snapshot attempts**: ограничить подзапрос `attempts` окном `created_at >= now()-interval '1 hour'` (PF1) — тяжёлая агрегация каждую секунду на активных играх.
- **Push-отправку** вынести из request-пути в goroutine с `context.WithoutCancel` + per-subscription timeout (PF9).

### Прочее
- Проверять `IsUserManager` только для draft/private/админ (PF13) и `games_view` кэшировать (PF12).
- Batch: `GROUP BY game_id` для автостарта игр (PF10), передавать загруженный `passing` в `AdvanceToNextLevel` (PF7).
- Убрать дубли индексов (000007/000008 `idx_game_passings_game_status`, 000013/000036 `idx_games_name_trgm`).
- Очистить dead code: `CompleteLevelWithSSE`/`AdvanceToNextLevelWithSSE` (не вызываются).

---

## C. Улучшения пользовательского опыта

1. **Skeleton loaders** для monitor/chat-страниц (стартуют пустыми до первого снапшота).
2. **Optimistic chat**: мгновенный пузырь сообщения (в командном чате уже есть — перенести в общий).
3. **Клавиатурные шорткаты**: `?` — карта, `/` — фокус поиска, Esc закрывает модалки, `n` — новая игра.
4. **Empty states с CTA**: «Создайте игру» на пустом месяце календаря и в мониторе без данных.
5. **Мобильная полировка**: ripple/pressed для `data-href` карточек, active-состояния.
6. **Dark mode sweep**: заменить хардкод `bg-white`/`text-gray-700` на `dark:` варианты (тосты, заголовки карточек).
7. **Print styles**: `@media print` для результатов, турнирных таблиц (сейчас печатаются с навигацией).
8. **A11y focus**: единый focus-trap helper (модалки подтверждения/превью).
9. **Локализованное форматирование времени**: `Intl.DateTimeFormat` вместо жёсткого `M:SS`/`02.01.2006`.
10. **404-страница**: поиск + подсказки по недавним путям.

---

## D. Архитектурные улучшения (кодовая база)

1. **Слоистость**: 8 сервисов лежат на `*gorm.DB` и выполняют raw SQL (game ×5, tournament, notification, export). Ввести repository-интерфейсы для высокорисковых потоков (`CalculateResults`, `UpdateRatingsForGame`, `checkTimeoutsImpl`) → бизнес-логика тестируема без БД.
2. **Разделение god-классов**: `AuthService` (JWT+refresh+lockout+blacklist) → выделить `RefreshTokenService`; `GameService` на 7 файлах — зафиксировать границы.
3. **Единый error-контракт**: promote sentinel-ошибок, локализованные строки только в render-слое.
4. **DI manifest**: `services` struct в `init.go` неполон (нет GamePassingService, TwoFactorService) — синхронизировать.
5. **Автотест i18n**: проверять, что каждый ключ, используемый в шаблонах и Go-коде, существует в ru/en (F3 остался бы незамеченным).
6. **Тесты**: unskip 2FA/OAuth handler-тесты (сейчас `t.Skip`), добавить unit-тесты с mocks для *gorm.DB-сервисов, заменить `render.SetTemplate` на `t.Cleanup`.

---

## Приоритет фиксов
1. **G1** — backdoor через test-маршруты (секьюрити, срочно).
2. **G2** — мёртвые отзывы; **F1** — mojibake; **F3** — ключ i18n.
3. **G4/G5** — tournament/photo authz; **SEC1/SEC2** — backup/uploads.
4. **F2** — XSS-паттерн в layout (экранирование + scheme-валидация).
5. Индексы из секции B; кэш reviews; dead code.

## Статус
Документ описывает **находки pass 28**. Код не менялся в рамках этого ревью (read-only). Принято решение: сначала показать отчёт, затем закрывать баги отдельными раундами фиксов.
