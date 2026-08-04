# Deep Review Gengine-0 — 4 августа 2026

Полный повторный аудит проекта: безопасность, корректность, производительность, UX/фронтенд.
Проведён 4 ревью-агентами (код, безопасность, производительность, UX) + выборочная верификация ключевых находок.

**Легенда:** 🔴 critical · 🟠 high/серьёзно · 🟡 medium · 🟢 хорошо

---

## 1. 🔴 Критические проблемы

### 1.1 Безопасность

**S1. `.env` с живыми секретами в репозитории**
- Файл `.env` лежит на диске с реальными `JWT_SECRET`, `SESSION_SECRET`, `VAPID_PRIVATE_KEY`, `DB_PASSWORD`, `ADMIN_PASSWORD`.
- `ADMIN_PASSWORD=123456789101112` — проходит проверку `requireStrongPassword` только по длине (≥12), без проверки сложности.
- `JWT_SECRET` содержит спецсимволы `|&'\` и завершающий `\r` — godotenv может парсить некорректно.
- **Действие:** проверить `git log --all -- .env`; если файл попал в историю — удалить из истории и **ротировать все секреты**.

**S2. IDOR: логи любой игры видны любому авторизованному**
- `internal/domain/monitor/routes.go:140` — `/games/:id/logs` под `AuthRequired` **без** `gameManager`.
- `handler.go:734 ListLogs` не проверяет права (просто `GetLogsByGameID`). Логи содержат попытки ввода кодов, подсказки, тайминги.
- **Фикс:** добавить `gameManager` к `/games/:id/logs` (и `/logs/ws`) или проверку `IsUserManager` внутри `ListLogs` (в `LogsWS:778` уже есть).

**S3. Обход 2FA через OAuth**
- `internal/domain/user/auth_handler.go:781` — `OAuthCallback` выпускает JWT без проверки `TwoFactorEnabled`, в отличие от парольного логина.
- **Фикс:** в `OAuthCallback` при `user.TwoFactorEnabled` перенаправлять на `/auth/2fa/login` с `pending_user_id`.

**S4. Нет rate limit на ввод TOTP**
- `routes.go:74-75` — `/auth/2fa/login` и `/auth/2fa/verify` без лимита. 6-значный код можно перебирать.
- **Фикс:** `middleware.LoginRateLimit(5*time.Minute, 5)` на оба эндпоинта + блокировка сессии после N ошибок.

**S5. WebSocket origin — prefix-match**
- `notification/routes.go:36` — `strings.HasPrefix(origin, "http://"+host)` — `https://example.com.evil.com` пройдёт для `example.com`.
- **Фикс:** exact match через `url.Parse` + `net.SplitHostPort` (как в `monitor/handler.go:35-59`).

### 1.2 Корректность

**B1. Владелец не может открыть собственный скрытый профиль**
- `profile_handler.go:148-151` — проверка `ProfileVisibility == "hidden"` стоит **до** вычисления `IsOwner` (строка 180).
- Пользователь со скрытым профилем получает 403 на `/users/{id}` собственного профиля.
- **Фикс:** пропускать блокировку при `currentUserID == userID`.

**B2. Push-уведомления игнорируют настройку `push_enabled`**
- `notification/service.go:225-279` (`sendWebPush`) отправляет на все подписки при каждом `Create`, не читая `GetSettings().PushEnabled`.
- **Фикс:** в `sendWebPush` проверять `PushEnabled` (и `BrowserEnabled` для WS).

**B3. Сброс пароля не отзывает сессии**
- `user/service.go:719-742` — после `ResetPassword` не вызывается `RevokeAllUserTokens`; ранее выданные refresh/JWT остаются валидными. Шаги не обёрнуты в транзакцию.
- **Фикс:** вызвать `RevokeAllUserTokens` и завернуть в `db.Transaction`.

**B4. Push-подписка маскирует ошибку БД под «создать»**
- `push_handler.go:64-86` — `First(&existing)` при реальной ошибке БД (не `ErrRecordNotFound`) молча уходит в `Create` → дубликаты.
- **Фикс:** `errors.Is(result.Error, gorm.ErrRecordNotFound)`; валидировать `keys.p256dh`/`keys.auth`.

**B5. «Безвозвратное» удаление пользователя = soft delete**
- `user/repository.go:187` + `admin/handler.go:309` — `Delete(&User{})` — soft delete (встроен `gorm.Model`). Email (`uniqueIndex`) навсегда занят → повторная регистрация невозможна. Зависимые записи не чистятся.
- **Фикс:** очистка зависимостей или `Unscoped().Delete` + понятное сообщение.

**B6. Паника в template-функции `loop`**
- `templatefuncs/funcs.go:88-94` — `make([]int, end-start+1)` при `end < start` даёт отрицательную длину → паника на рендере страницы.
- **Фикс:** guard `if end < start { return []int{} }`.

---

## 2. 🟠 Серьёзные проблемы

### Безопасность

**S6. Limited SSRF через push-эндпоинт**
- `push_handler.go:44` принимает произвольный `endpoint`; `notification/service.go:255` делает исходящий POST при создании уведомления. Можно нацелить на внутренние адреса.
- **Фикс:** валидировать `https://` + допустимые push-домены; не следовать редиректам.

**S7. `X-Forwarded-Host` доверяется без проверки**
- `monitor/handler.go:41-43`, `notification/routes.go:33-35` — при прямом доступе атакующий подставляет заголовок и обходит origin-проверку.

**S8. Rate-лимиты обходятся по IP**
- Все лимитеры ключуются по `c.ClientIP()`; при неправильном `TRUSTED_PROXIES` атакующий может подменять `X-Forwarded-For`.

**S9. Session cookie не шифрован**
- `router.go:45` — `cookie.NewStore([]byte(secret))` — только HMAC-подпись без шифрования; клиент читает `pending_user_id`, `oauth_state`, `2fa_verified_*`.
- **Фикс:** передать два ключа (authentication + encryption).

**S10. Монитор: chat/voting без `gameManager`**
- `monitor/routes.go:106,128,183,195` — `/games/:id/chat`, `/chat-rooms`, `/voting/:session_id/results` под `AuthRequired`. `ChatRoomIDs` создаёт комнаты по произвольному gameID, `GetVotingResults` раскрывает результаты любой сессии.

**S11. Пропуски CSRF**
- `/auth/webauthn/*` полностью без CSRF (`app.go:89`) — стоит исключать только `login/begin`/`login/finish`.

**S12. HSTS может не отправляться**
- `security.go:72-74` — HSTS только при прямом TLS или `X-Forwarded-Proto: https`; если прокси не передаёт заголовок — HSTS не появится.

**S13. `GIN_MODE=debug` в проде**
- `.env:8` — включает verbose-режим (в т.ч. детали паник в ответах Gin).

### Корректность

**B7. `LevelService.Update` сбрасывает позицию уровня в 0**
- `level/service.go:102-114` — при `updated.Position == 0` проверка `ExistsByPosition` пропускается, но `level.Position = updated.Position` выполняется безусловно.
- **Фикс:** при 0 сохранять старую позицию.

**B8. Админское удаление игры не инвалидирует кэш и не чистит файлы**
- `admin/handler.go:452` — прямой `gameRepo.Delete` без `cache.DeleteByPrefix` и удаления обложки/фото. Кэшированная игра живёт до 5 мин, файлы остаются на диске.
- **Фикс:** вынести очистку+инвалидацию в общий сервисный метод (как в `GameService.Delete`).

**B9. Nil-риски `monitorSvc`**
- `svc_passing.go:268` и `svc_play.go:134` — `s.monitorSvc.*` без nil-проверки (в `broadcastSnapshot:521` проверка есть — непоследовательно).

**B10. Турнир: ошибка проглочена + TOCTOU**
- `tournament/service.go:217-224` — при ошибке `FindPassingsByGamesAndTeam` лог + пустой `existingMap` → дубликаты passings. Проверка участия вне транзакции.
- **Фикс:** возвращать ошибку; проверку внутрь транзакции или `OnConflict`.

**B11. Утечка WebSocket-клиентов при обрыве**
- `pkg/websocket/client.go:147-172` — при ошибке `ReadMessage` read-loop `return` без `client.Close()`. Клиент живёт в комнате до ping-таймаута (~54с), копятся горутины.

**B12. Кэш рейтинга не инвалидируется при новых отзывах**
- `service.go:324-339` — `rating:game:%d` TTL 5 мин, но `ReviewService` не вызывает инвалидацию.

**B13. `Pluck` ошибка игнорируется**
- `monitor/service.go:73-78, 225-230` — результат `Pluck("email", &captains)` не проверяется → капитаны молча не получают письма.

**B14. `ChangePassword` не отзывает текущий JWT**
- `profile_handler.go:382-388` — отзываются только refresh-токены; кука `jwt` не очищается, JTI не блэклистится.

**B15. `UpdateProfile` без проверки уникальности email**
- `profile_service.go:97-102` — конфликт вернёт ошибку БД → 500 вместо понятного сообщения.

### Производительность

**P1. GORM `SkipDefaultTransaction` не установлен**
- `internal/db/db.go:38-40` — каждая single-row запись обёрнута в `BEGIN/COMMIT` (десятки тысяч `Create`/`Save`). **Самая дешёвая и широкая победа:** `SkipDefaultTransaction: true` (явные `.Transaction()` уже используются где нужно).

**P2. Утечка LRU-списка мониторинга**
- `svc_monitor.go:99-115` — при каждом рефреше снапшота старый элемент `cacheList` для gameID не удаляется → неограниченный рост списка (утечка памяти) и преждевременная эвикция.
- **Фикс:** `if elem, ok := s.cacheKeys[gameID]; ok { s.cacheList.Remove(elem) }` перед `PushBack`.

**P3. Агрегатные подзапросы по всей таблице**
- `svc_listing.go:44-52` — на каждый просмотр `/games` агрегируются ВСЯ `reviews` и `game_passings`.
- `svc_monitor.go:195-199` — на каждый снапшот агрегируется вся `attempts`.
- **Фикс:** фильтровать подзапросы тем же WHERE/скоупом игры; либо кэшировать агрегаты.

**P4. Двойной `Preload("Level.Questions.Answers")` в SubmitCode**
- `svc_play.go` + `svc_attempt.go:32` — уровень с вопросами/ответами грузится дважды в одной транзакции (8-10 SQL на отправку кода).
- **Фикс:** передавать загруженный Level; кэшировать `level:%d:questions`.

**P5. `Show` не использует готовый `GetGameWithStats`**
- `hnd_game.go:168-213` — 4 независимых запроса (`GetByID`+`IsUserManager`+`ListReviews`+`GetAverageRating`), хотя `GetGameWithStats` (svc_crud.go:183) собран именно для Show.
- **Фикс:** переключить Show на `GetGameWithStats`.

**P6. N+1 в `UpdateRatingsForGame`**
- `svc_rating.go:45-93` — запросы на каждый passing + каждого пользователя.
- **Фикс:** batch `INSERT ... ON CONFLICT` через `unnest`.

**P7. Кэш `game:%d:viewer:%d` дублируется на зрителя**
- `service.go:169,203` — 1000 просмотров = 1000 копий Game в памяти/Valkey; при кэш-хите для private всё равно SQL `IsUserManager`.
- **Фикс:** один `game:%d` + отдельный кэш прав.

**P8. Мёртвая инвалидация `games:list:`**
- `service.go:216,260,271` — `DeleteByPrefix("games:list:")` вызывается, но список **не кэшируется** → чистый SCAN по Valkey без пользы.

**P9. Двойная JSON-сериализация в `cacheGetGame` (Valkey)**
- `service.go:143-164` — до 4 marshal/unmarshal на кэш-хит. Хранить `[]byte` и десериализовать один раз.

**P10. `BroadcastToRoom` блокирует продюсера**
- `room_hub.go:268-274` — канал 64, при всплеске блокирует хендлеры после коммита транзакции.

**P11. Отсутствующие индексы**
- `attempts.created_at` (фильтр «последние 5 мин»), `users.name/email` ILIKE (админ-поиск), составные `(game_passing_id, created_at)` для logs, `(user_id, status)` для invitations, `(session_id, voter_id)` UNIQUE для blackbox_votes.

**P12. Глобальный mutex кэша в горячем пути**
- `pkg/cache/cache.go:130-174` — каждый `Set` берёт два мьютекса + манипуляции с префиксами. Рассмотреть sharded locks.

**P13. SSE `Broadcast` сериализует JSON на каждого подписчика**
- `hnd_sse.go:224-240` — маршалить один раз вне цикла.

---

## 3. 🟡 Средние проблемы (UX и код)

### UX / Фронтенд

**UX1. Двойное подтверждение: нативный `confirm()` + кастомная модалка**
- app.js:120-131 (`data-confirm-form`) + нативные инлайн-обработчики в 7+ шаблонах (`games-show`, `games-photos`, `questions-show`, `questions-list`, `levels-show`, `teams-members`, `admin-backups`). Пользователь закрывает два диалога.

**UX2. Отмена подтверждения оставляет кнопку submit заблокированной**
- `initFormLoading` (app.js:84-96) блокирует кнопку до того, как document-обработчик отменит submit → после «Отмена» кнопка навсегда disabled.
- **Фикс:** пропускать `data-confirm-form` в `initFormLoading` или возвращать кнопку при отмене.

**UX3. Кнопка push на `/settings/notifications` не работает**
- `notification-settings.html:58-62` содержит только `#enable-push`, а `initPushSubscription` (app.js:365) требует все три элемента → early return. (На профиле работает.)

**UX4. Пустой список пользователей в админке → вечные skeleton-строки**
- `admin-users.html:78` — в `{{else}}` ветке рендерится `{{template "skeleton-table" 5}}` + «Нет пользователей».

**UX5. `lang="ru"` захардкожен в `<html>`**
- `layout.html:2` — при EN-интерфейсе скринридеры и браузерный переводчик работают неверно.
- **Фикс:** `lang="{{.Lang}}"`.

**UX6. Карточки `role="link"` ломают клики по вложенным ссылкам**
- `games-list.html:68` — внутри кликабельной карточки лежат `<a href=".../edit">`; document-click уводит на URL карточки вместо ссылки. Вложенная интерактивность невалидна для ARIA.

**UX7. Автокомплит конфликтует с фильтром на `/games`**
- `games-list.html:18` `id="search"` + `initSearchAutocomplete` (app.js:488) — дропдаун `/api/search/games` «угоняет» фильтр списка.
- **Фикс:** сменить id поля фильтра на `filter-search`.

**UX8. Двойное SSE на геймплее → дублирующие тосты**
- `gameplay-show.html:2` (app.js init) + `gameplay-show.html:431` (инлайн `EventSource`). Оставить одно.

**UX9. Escape в кастомной модалке → ReferenceError**
- app.js:164-169 — `resolve` не в лексической области keydown-обработчика → незавершённый Promise.

**UX10. Счётчик отмены удаления убегает в 2 раза быстрее**
- `games-show.html:364-398` — два таймера декрементят счётчик → «0» за 2,5 с, реальное удаление через 5 с.

**UX11. Офлайн-тост «вечный»**
- app.js:17-30 — `showToast(offlineMsg, 'warning', 0)` — duration 0, тост не исчезает, стакается.

**UX12. Чекбоксы уведомлений в профиле не отражают сохранённые настройки**
- `profile-show.html:102-127` — `checked` захардкожены; серверные настройки не подставляются.

**UX13. Доступность**
- Нет skip-link, нет `aria-live` на тостах, бургер без видимого фокуса, модалки без focus-trap, календарь не работает с клавиатуры, чат-поля без label, уведомления недоступны в мобильном меню.

**UX14. `table { min-width: 600px }` для всех таблиц на мобильных**
- layout.html:83-86 — ненужный горизонтальный скролл у компактных таблиц.

**UX15. Три реализации WebSocket + ws-client.js — мёртвый код**
- `team-chat`, `chat-page`, `monitor-page`, layout — каждый свой реконнект; `ws-client.js` не используется.

**UX16. Захардкоженная русская логика классификации ошибок**
- `gameplay-show.html:279,324` — `/код|code|answer|ответ/` и `'лимит подсказок'` — при EN-интерфейсе определение типа ошибки ломается.

**UX17. Несогласованные диалоги и loading-состояния**
- Смешение нативных `confirm()`/`alert()` и кастомной модалки (10+ мест); дубли loading-текста (auth-login, auth-register, gameplay).

**UX18. Прочее**
- `initSSEIndicator` (app.js:810) — мёртвый код (нет `#sse-status`).
- `w-7/10` — невалидный Tailwind-класс (`skeleton-table.html:8`).
- `'Inter'` объявлен, но не загружается (`app.css:32`).
- `setInterval(applyAutoTheme, 60000)` работает вечно, даже когда автосмена выключена.
- Wizard создания игры не работает без JS (`games-new-wizard.html`).
- `field-error` рендерится пустым без ошибки (`games-settings.html:26,35,44`).

### Код / архитектура

**C1. DB-запрос в middleware на каждый запрос**
- `app/router.go:54-60` + `middleware/auth.go:45,61` — загрузка `theme_settings` для каждого авторизованного запроса, включая `/api/*` и WS.
- **Фикс:** кэшировать в сессии/короткий TTL или грузить только для HTML-роутов.

**C2. Глобальное mutable-состояние `themeSettingsLoader`**
- `middleware/theme.go:14` — синглтон, сеттер из app-слоя; затрудняет тесты.

**C3. Хендлеры работают напрямую с `*gorm.DB`**
- ProfileHandler, GameplayHandler, PushHandler — вместо репозиториев (смешение слоёв).

**C4. Дублирование проверки прав владельца/капитана**
- 4+ места (crudService.Delete, GameService.Delete, CanManageTeam, InvitationService) — легко разойтись.

**C5. Ошибки сравниваются по `err.Error()`**
- `hnd_game.go:608-615` — хрупко; использовать sentinel-ошибки (`ErrPositionTaken` как образец).

**C6. Длинные функции**
- `svc_play.go:SubmitCode` (~70 строк), `svc_listing.go:ListFilteredPaginated` (~130 строк).

**C7. Hardcoded русские строки в сервисах**
- `notification/service.go:368-399`, `profile_handler.go:357`, `monitor/service.go:82-86` — при наличии i18n.

**C8. `err == gorm.ErrRecordNotFound` вместо `errors.Is`**
- `notification/service.go:137,161`.

**C9. Backend-файл с точностью до секунды перезапишется**
- `admin/service.go:64` — два бекапа в секунду. `exec.Command` без `CommandContext` — не отменяется при таймауте.

**C10. iCal подставляет `c.Request.Host`**
- `calendar/handler.go:169` — host-header injection в URL события.

**C11. Форма настроек уведомлений обнуляет отсутствующие флаги**
- `settings_handler.go:63-90` — bool-поля: новый чекбокс, не переданный старым фронтом, сбросит настройку.

**C12. `calculateProfileCompletion` считает подписки, которые не загружаются**
- `profile_handler.go:108` — `u.Subscriptions` не прелоадится (только `Achievements`), шестой пункт всегда пуст.

**C13. `ProfileService.GetThemeSettings` не различает «нет записи» и ошибку БД**
- `profile_service.go:107` — `Scan` пустой строки без ошибки; слой смешивает состояния.

---

## 4. ✅ Что сделано хорошо

- **Безопасность:** параметризованные запросы, белые списки сортировки, `EscapeLike`, CSRF через gorilla с `SameSite=Strict`, CSP с per-request nonce, sanitizer (bluemonday), защита path traversal + whitelist расширений + MIME-проверка, JWT JTI blacklist, refresh-token rotation + fingerprint, dummy bcrypt против email-энумерации, account lockout, удаление устаревших push-подписок (404/410).
- **Транзакции:** `SELECT ... FOR UPDATE` с фиксированным порядком блокировок (анти-дедлок), `OnConflict DoNothing` в Apply.
- **SSE/WS:** exact origin-check в мониторе, write deadline, heartbeat, mutex-защита, лимиты соединений per-IP.
- **Тема:** аккуратная JSON-сериализация, дефолты, валидация HH:MM, анти-FOUC скрипт в `<head>`, live-обновление.
- **Архитектура:** i18n 1442 ключа ru/en синхронны, `data-i18n-*` для JS, сервисный слой с проверками прав, фасад с `With*`-опциями.

---

## 5. Приоритеты исправления

### Первая волна (безопасность + блокеры)
1. **S1** — ротация секретов из `.env`, проверка git-истории.
2. **S2** — закрыть IDOR на `/games/:id/logs` (добавить `gameManager`).
3. **S3** — 2FA в `OAuthCallback`.
4. **S4** — rate limit на TOTP.
5. **S5** — exact-match origin в notification WS.
6. **B1** — владелец видит свой скрытый профиль.
7. **B4** — `errors.Is` в push-handler + валидация ключей.

### Вторая волна (корректность)
8. **B3** — отзыв сессий при сбросе пароля.
9. **B2** — уважать `push_enabled` в `sendWebPush`.
10. **B5** — жёсткое удаление пользователя + очистка зависимостей.
11. **B7** — позиция уровня.
12. **B8** — инвалидация кэша при админском удалении игры.
13. **B11** — `defer client.Close()` в WS read-loop.
14. **B12** — инвалидация кэша рейтинга при отзывах.

### Третья волна (производительность — дешёвое с большой отдачей)
15. **P1** — `SkipDefaultTransaction: true`.
16. **P2** — фикс утечки LRU мониторинга.
17. **P5** — Show через `GetGameWithStats`.
18. **P3/P4** — скоуп агрегатов, убрать двойной Preload.
19. **P7/P8/P9** — кэш: один объект игры, убрать мёртвую инвалидацию, одна сериализация.

### Четвёртая волна (UX)
20. **UX1/UX2** — единая логика подтверждения без двойных диалогов и «залипающих» кнопок.
21. **UX3** — починить push-кнопку на странице настроек.
22. **UX5** — динамический `lang`.
23. **UX6/UX7** — карточки-ссылки и конфликт `#search`.
24. **UX8/UX9/UX10/UX11** — дубли SSE, ReferenceError, таймер отмены, вечный офлайн-тост.
25. **UX13** — доступность: skip-link, aria-live, фокус-менеджмент.

---

## 6. Стратегические улучшения проекта

### Кодовая база
- **Устранить DB-запрос темы из middleware** (C1): кэшировать в сессии/claims или грузить только для HTML-маршрутов — убирает N+1-per-request.
- **Ввести sentinel-ошибки** по всему коду (C5) — заменить сравнение по `err.Error()`.
- **Вынести репозитории в хендлеры** (C3): ProfileHandler, GameplayHandler, PushHandler не должны работать с `*gorm.DB` напрямую.
- **Единый механизм прав** (C4): один `CanManageGame` на все домены.
- **Автоматическая проверка i18n**: CI-шаг, сравнивающий ключи ru/en.
- **Мониторинг производительности**: добавить метрики Prometheus на горячие пути (SubmitCode, ListFilteredPaginated) и тайминги SQL.
- **Логирование в JSON** в проде, `GIN_MODE=release`.

### Пользовательский опыт
- **Единый компонент подтверждения** (модалка + тосты) во всех местах вместо `confirm()`/`alert()`.
- **Пустые состояния** вместо skeleton во всех списках.
- **Прогрессивное улучшение wizard** создания игры (без JS — простая форма).
- **Персонализация**: сохранять «вид списка игр» (таблица/карточки) на сервере, а не в localStorage.
- **Единый WebSocket-клиент** (использовать `ws-client.js`) — консистентный реконнект и статусы.
- **A11y-проход**: skip-link, aria-live, фокус-трапы, `lang` из контекста.
