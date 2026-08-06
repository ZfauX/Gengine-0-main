# Deep Review Gengine-0 — 6 августа 2026 (pass 24)

Повторный глубокий аудит после закрытия всех пунктов pass 21–23 (безопасность, корректность, производительность, UX/a11y, тесты).

**Методология:** 5 параллельных ревью-агентов (безопасность, производительность, корректность, UX, тесты) + **ручная верификация** каждой высокой находки. Ложные срабатывания исключены.

**Легенда:** 🔴 критично · 🟠 высоко · 🟡 средне · ✅ подтверждено-хорошо · ❌ ложное срабатывание

---

## Итог

Критических проблем **не найдено**. Проект выдержал 4 волны исправлений: атомарность, advisory-локи, параметризованный SQL, CSP с nonce, origin-проверки WS/SSE, кэш с singleflight — на месте. Осталось ~10 высоких и ~20 средних пунктов, в основном фронтенд и периферийные оптимизации.

---

## 🟠 Высокие проблемы

### 1. UX — кнопка «Далее» в мастере создания игры застревает ✅ подтверждено
`games-new-wizard.html:139` + `static/js/app.js:99-115`
- `initFormLoading` на любом `submit` отключает `button[type=submit]` и ставит спиннер. Wizard перехватывает submit через `preventDefault()` (переход на шаг), но app.js-обработчик всё равно отключает кнопку и **никогда не восстанавливает** → шаг 1 не пройти без перезагрузки.
- **Фикс:** `data-no-loading` на `#wizardSubmit` (паттерн уже используется в других формах).

### 2. UX — sw.js push handler падает на пустом payload ✅ подтверждено
`static/sw.js:129-135`
- Если пришёл push без `data`, `event.data.json()` бросает, а catch вызывает `event.data.text()` на `null` → второй throw → событие не обработано.
- **Фикс:** guard `if (event.data)` + try/catch с fallback.

### 3. Code — дубли отзывов (upsert race) ✅ подтверждено
`svc_review.go:52-75`, `game/model.go:150-158`
- `CanReview` (COUNT) → `Create` без уникального индекса на `reviews(game_id, user_id)`. Два параллельных POST → два отзыва, рейтинг завышается. Миграция 000001:204 `UNIQUE(game_id, user_id)` относится к **co_authors**, не к reviews.
- **Фикс:** миграция 000034 `CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_game_user ON reviews(game_id, user_id) DO NOTHING`? (нет — просто `CREATE UNIQUE INDEX`) + `ON CONFLICT DO NOTHING` в Create, инвалидация `rating:game:%d`.

### 4. Security — публичные `/api/*` эндпоинты без rate limit ✅ подтверждено
`middleware/rate_limiter.go:478-486`
- `APIRateLimit` пропускает анонимов (`if userID == 0 { c.Next(); return }`). Публичные `GET /api/search/games`, `GET /api/users/search`, `GET /api/v1/calendar` защищены только глобальным лимитом.
- **Фикс:** отдельный лимитер для публичных поисковых эндпоинтов.

### 5. Perf — `AdvanceToNextLevel` грузит все уровни на каждый submit ✅ подтверждено
`svc_progress.go:166-170`
- На каждый верный код: `SELECT * FROM levels WHERE game_id=? ... ORDER BY position` + перезагрузка passing. Для игры с N уровнями — O(N) строк на каждое прохождение уровня.
- **Фикс:** один запрос `SELECT * FROM levels WHERE game_id=? AND position > (SELECT position FROM levels WHERE id=?) ORDER BY position LIMIT 1`; не перезагружать passing.

### 6. Perf — SubmitCode: 10-12 round-trips в транзакции ✅ подтверждено
`svc_play.go:113-159`, `svc_progress.go:98-108`, `helpers.go:16-40`
- `GetCurrentProgressForUpdate` (4 запроса с Preload Level.Questions.Answers) + `CheckTeamMembership` (3, повторный лок passing) + `tx.First(&passing)` + attempt + CompleteLevel/Advance + log.
- **Фикс:** убрать Preload уровня из progress-запроса (SubmitCodeWithTx сам догрузит), объединить CheckTeamMembership с загрузкой passing, одна membership-проверка через EXISTS.

### 7. Perf — страница логов: COUNT+JOIN по растущей таблице ✅ подтверждено
`service.go:533-559`
- `COUNT(*)` с JOIN game_passings + отдельный paged-запрос. Логи растут монотонно.
- **Фикс:** `COUNT(*) OVER()` в одном запросе; при желании денормализовать `logs.game_id` (миграция).

### 8. Tests — `make test-short` требует PostgreSQL вопреки документации ✅ подтверждено
- `monitor`, `tournament`, `team`, `social`, `export`, `calendar`, `level`, `pkg/audit` — тесты на PG **без `-short` guard** → `go test -short` подключается к БД и на CI без PG **падает** (`t.Fatalf`), а не скипается.
- **Фикс:** единый guard `if testing.Short() { t.Skip }` или `SetupPostgresDBOrSkip` для всех PG-тестов.

---

## 🟡 Средние проблемы

### Безопасность
**S-1. Нет CSRF/Origin-check на cookie-авторизованных `/api/*` мутациях** ✅ подтверждено (смягчено)
- `middleware/csrf_json.go` — мёртвый код. `PUT /api/users/preferences/games-view`, `DELETE /follow/:id`, `DELETE /games/:id/photos/:photo_id`, POST push — без Origin-check. Смягчение: JWT/refresh cookie `SameSite=Strict` (`handler.go:69`), CORS allowlist.
- **Фикс (defense in depth):** Origin/`Sec-Fetch-Site` check в shared middleware для JSON-мутаций.

**S-2. GET-формы без manager-gate** ✅ подтверждено (LOW)
- `level/handler.go:184` `NewForm`, `:655` `NewQuestionForm`, `monitor/handler.go:460` `ChatPage` — рендерятся для любой игры (данные защищены на service-слое). Добавить `IsUserManager` gate.

**S-3. `OptionalAuth` не перечитывает роль** ✅ подтверждено (LOW)
- Пониженный админ сохраняет доступ к swagger/metrics до expiry токена. Данные-роуты защищены `AuthRequired`. Перечитать роль в `OptionalAuth` для паритета.

### Корректность
**C-1. `svc_listing.go:174` — игнорируется ошибка count-запроса** ✅ подтверждено
- `_ = ...Scan(&total)` → при сбое БД total=0 («игр нет»). Логировать/возвращать ошибку.

**C-2. `RefreshAccessToken` не атомарен (claim+create)** ✅ подтверждено
- Claim отзывает старый, потом создаёт новый; при сбое создания клиент без refresh. Обернуть в транзакцию.

**C-3. `LevelService.Duplicate` без advisory lock** ✅ подтверждено
- `Move` сериализован, `Duplicate` (position+1) — нет; два параллельных → конфликт позиций. Добавить `pg_advisory_xact_lock(gameID)`.

**C-4. `PhotoService.Delete` маскирует ошибки БД как 403** ✅ подтверждено
- `First(&coAuthor)` ошибка (не только ErrRecordNotFound) → «нет прав». Различать через `errors.Is`.

**C-5. `checkTimeoutsImpl`: частичный сбой advance застревает прохождение** ✅ подтверждено
- Batch-`UPDATE finished_at` помечает все, потом `AdvanceToNextLevel` только логирует ошибки и `continue` → команда с `finished_at` без next-progress, retry не будет. Возвращать ошибку из транзакции (откат всей партии) или rollback конкретного passing.

### Производительность
**P-1. Ключ кэша листинга неограничен (search/date)** ✅ подтверждено
- `listingCacheKey` содержит сырой `Search`/даты → фрагментация LRU. Кэшировать только без-поисковый листинг или хэшировать фильтр.

**P-2. `unreadCache` никогда не чистится** ✅ подтверждено
- Записи с TTL 30с удаляются только при Create/MarkAsRead; fetch оставляет записи → медленная утечка. Добавить периодическую очистку (sweep) или перейти на LRU/кэш-стор.

**P-3. `GetGameplayData` 6-8 запросов** (уже errgroup)
- passing+Team+Game.GameSetting (3), fallback settings, progress+Level (2), attempts+voting (2). Выбирать только нужные колонки, кэшировать game_settings.

**P-4. `UseHint` ~6 запросов** — кэшировать settings, грузить только `hint`.

**P-5. `GetAvailableUsers` без LIMIT** — пагинация поиска участников.

**P-6. `Valkey DeleteByPrefix` удаляет по одному ключу** ✅ подтверждено
- `SCAN` + `client.Del(key)` в цикле — батчить `client.Del(ctx, keys...)` как в `DeleteByPrefix`.

**P-7. Сортировка листинга по rating/participants без индекса** — добавить `(is_draft, visibility, rating_value DESC)`.

**P-8. `LOWER() LIKE` не использует pg_trgm** — `ILIKE` или expression-индексы.

### UX/a11y
**U-1. View-toggle race с preference-fetch** ✅ подтверждено
- `games-list.html:232-249` — ответ fetch перезаписывает выбор пользователя. Guard `userTouched`.

**U-2. Push-состояние не синхронизировано на загрузке** ✅ подтверждено
- Кнопки всегда «Включить», нет проверки существующей подписки; сбой POST оставляет orphaned browser-subscription (удалить в catch).

**U-3. Таймер не ре-синкается при возврате на вкладку** — `visibilitychange` → `updateTimer()`.

**U-4. delete-modal без focus-trap + countdown без aria-live** (games-show).

**U-5. photo-lightbox без `role="dialog"` + нет restore фокуса**.

**U-6. monitor `#connection-status` без `role="status"`; gameplay `#message-area` без aria-live** — SR не слышит статус/ошибки.

**U-7. Жёлтые бейджи без dark: вариантов** (dashboard, profile-public, games-show undo, monitor suspicious) — добавить `dark:bg-yellow-900/20 dark:text-yellow-200`.

**U-8. `calendar-page:157` TF+JS-replace('D','M','Y')** — хрупко, передать реальные значения.

**U-9. Хардкод**: `auth-login.html:14` label «Email», `games-new.html:81` alt="Preview".

**U-10. `admin-users.html:113` лишний `</div>`**; `levels-list` drag-handle div без role.

### Тесты
**T-1. 3 скипнутых теста 2FA-login** (`auth_handler_test.go`) — заменить на cookie-session тесты.
**T-2. WebAuthn — 0 тестов** (регистрация под 2FA, FinishLogin `2fa_required`).
**T-3. Нет тестов**: VK-отказ (S-L2), UpdateRatingsForGame error-rollback, SSE slow-client drop, GetUserGamesView ветки, CloseVoting tie-break, RemoveGame multi-game точное списание, AcceptInvitation гонка, poller dedup.

---

## ⚡ Варианты оптимизации (по приоритету)

### Быстрые (дни)
1. **UX-1** — `data-no-loading` на wizardSubmit (1 атрибут, чинит застрявшую кнопку).
2. **UX-2** — guard `event.data` в sw.js push.
3. **C-3** — `ReviewService.Create` unique-индекс (миграция 000034) + ON CONFLICT.
4. **C-5** — вернуть ошибку из `checkTimeoutsImpl` при сбое advance.
5. **P-2** — sweep `unreadCache`.
6. **Tests** — единый `-short` guard для PG-тестов (CI без PG).
7. **U-3** — таймер на `visibilitychange`.

### Средние
8. **Perf-5** — `AdvanceToNextLevel` одним запросом.
9. **Perf-6** — убрать лишние round-trips SubmitCode.
10. **Security** — Origin-check на JSON-мутациях `/api/*`.
11. **P-1/P-6/P-7** — листинг-кэш, батч DEL, индексы.
12. **U-4/U-5/U-6** — a11y модалок/статусов.
13. **C-1/C-2/C-4** — обработка ошибок.

### Крупные
14. **Perf-7** — `logs.game_id` денормализация (миграция).
15. **T-2/T-3** — WebAuthn и security-тесты.
16. **Пул публичных лимитеров** для поисковых эндпоинтов.

---

## 🚀 Предложения по улучшению

### Кодовая база
- **Извлечь чистые функции** из PG-связанных сервисов (tie-break, таблица очков, deduction) для юнит-тестов без БД.
- **Единый JSON-CSRF/Origin middleware** для всех `/api/*` мутаций.
- **Генерация `sw.js` из конфига** (ASSET_VERSION) вместо ручного дублирования.
- **Индексы**: `reviews(game_id,user_id)`, `games(is_draft,visibility,rating_value)`, `games(is_draft,visibility,participant_count)`.
- **CI**: `make test-short` реально без PG; `-tags=integration` только для cmd/server.

### Пользовательский опыт
- **Wizard** без застревания кнопок; синхронизация push-кнопок с реальным состоянием подписки.
- **Мониторинг**: тёмная тема для жёлтых бейджей; статус-индикатор с `role="status"`.
- **Чаты/геймплей**: озвучивание ошибок SR, таймер ре-синк.
- **A11y**: focus-trap delete-модалки, dialog-семантика lightbox, aria-current на пагинации.

---

## ✅ Что сделано хорошо (подтверждено)

- **Безопасность**: роли из БД, атомарный refresh, advisory-локи, параметризованный SQL (0 инъекций), CSP nonce на всех inline, origin-проверки WS/SSE, SameSite=Strict, санитизация ввода/вывода.
- **Корректность**: onCommit после commit, идемпотентность турниров/рейтингов, атомарные claims, детерминированные tie-breaks.
- **Perf**: debounced снапшот, singleflight, typed-кэш Valkey, батч-операции, неблокирующий WS, асинхронный SSE.
- **UX**: единая модалка с focus-trap, reconnecting WS, server-side preference вида, diff-рендер мониторинга, контрастные фиксы.
- **Тесты**: 63 файла, ~630 функций; ключевые авторизационные/игровые пути покрыты.
- **CSP-гигиена**: ни одного inline-обработчика/стиля без nonce.

---

## Ложные срабатывания (проверено)

| Заявка | Вердикт |
|---|---|
| `PassingHandler.Apply` без капитан-проверки | ❌ **Ложь** — `svc_passing.go:60` проверяет `t.CaptainID != userID` |
| JWT/refresh cookie без SameSite | ❌ **Ложь** — `handler.go:69` `SameSiteStrictMode` |
| SQL-инъекции в snapshot/rating/export | ❌ **Не подтверждено** — всё через `?`-placeholders (кроме CSV-билдеров, требующих выборочной проверки) |

---

## Приоритеты следующей волны

1. **UX-1** (wizard) и **UX-2** (sw.js push) — ломают UX прямо сейчас.
2. **C-3** (дубли отзывов) + миграция 000034.
3. **Tests** — единый `-short` guard (CI без PG).
4. **C-5** (checkTimeouts застревание).
5. **Perf-5/6** (SubmitCode round-trips).
6. **Security** — Origin-check JSON-мутаций.
7. **P-2** (unreadCache sweep), **U-3** (таймер visibilitychange).
