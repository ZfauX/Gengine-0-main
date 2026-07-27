# Deep Review (pass 12) — июль 2026

## Покрытие

| Инструмент | Результат |
|-----------|-----------|
| `golangci-lint run ./...` | Чисто |
| `go vet ./...` | Чисто |
| `go build ./...` | OK |
| `go test ./... (short)` | 35/35 пакетов, все зелёные |
| `go test -tags=integration` | 12/12 |

---

## 🔴 CRITICAL — нужно исправить немедленно

### C1. SSE TOCTOU race — запись в ResponseWriter после выхода из handler

**Файл:** `internal/domain/game/sse_handler.go:163-192`

**Проблема:** `Broadcast()` копирует список сессий под `mgr.mu.Lock()`, отпускает блокировку, а затем пишет в `s.w` (ResponseWriter) каждой сессии. Между проверкой `<-s.done` (неблокирующее чтение) и захватом `s.mu.Lock()` SSE handler может выйти (context cancelled), ResponseWriter становится невалидным. Gin может переиспользовать соединение для другого запроса → паника или повреждение данных.

**Race window:**
```
Broadcast()                     SSE handler (sseConnect)
──────────────────              ──────────────────────────
<-s.done → false                c.Request.Context().Done()
(window opens)                  UnregisterSession(session)
                                close(s.done)
                                handler returns, RW invalidated
s.mu.Lock()
s.w.Write(...) ← PANIC/corruption
```

**Фикс:** Добавить флаг `s.closed` под `s.mu`, проверять его при записи.

---

### C2. RoomHub self-deadlock — повторный Lock()

**Файл:** `internal/pkg/websocket/room_hub.go:145-155`

**Проблема:** В `runLoop()` канал `unregister` вызывает `h.decConnection()` (который внутри делает `h.mu.Lock()`), а затем снова `h.mu.Lock()` на следующей строке. `sync.Mutex` в Go не реентерабельный → **deadlock** при первом же отключении клиента.

```go
case client := <-h.unregister:
    h.decConnection(client.RemoteIP)  // h.mu.Lock() внутри
    h.mu.Lock()                        // ДЕДЛОК!
```

**Фикс:** Вызывать `decConnectionNoLock` напрямую.

---

### C3. RatingService.Scan — всегда возвращает (0, 0, nil)

**Файл:** `internal/domain/game/rating_service.go:140-150`

**Проблема:** `Scan(map[string]any{...})` с pointer-значениями не работает в GORM. GORM ожидает указатель на struct или slice struct-ов. `map[string]any` с `&avgRating` внутри игнорируется → функция всегда возвращает `(0, 0, nil)` для всех игр. Это ломает:
- Сортировку игр по рейтингу
- Отображение рейтинга на карточке игры
- Фильтрацию по рейтингу

```go
Scan(map[string]any{"avg_rating": &avgRating, "count": &count})  // НЕ РАБОТАЕТ
```

**Фикс:** Использовать struct destination + `WithContext(ctx)`.

---

### C4. Notification WebSocket — context.Background() вместо request context

**Файл:** `internal/domain/notification/routes.go:67`

**Проблема:** Создаётся контекст от `context.Background()` вместо `c.Request.Context()`. При отключении клиента горутина не завершается 60 секунд (readTimeout).

```go
ctx, cancel := context.WithCancel(context.Background())  // ❌
defer cancel()
go func() {
    ws.HandleWebSocketWithContext(ctx, client)
}()
```

**Фикс:** `context.WithCancel(c.Request.Context())`

---

### C5. Tournament Upsert — ошибка тени

**Файл:** `internal/domain/tournament/service.go:292`

**Проблема:** `return err` вместо `return upsErr`. При ошибке `Upsert` транзакция не откатывается.

```go
if upsErr := s.tournamentResultRepo.Upsert(ctx, result); upsErr != nil {
    return err  // ДОЛЖНО БЫТЬ: return upsErr
}
```

**Фикс:** `return upsErr`

---

### C6. Rate limiting не работает на password reset

**Файл:** `internal/domain/user/routes.go:56-62`

**Проблема:** `/auth/forgot` POST и `/auth/reset` POST не имеют rate limiting middleware. В отличие от логина (`LoginRateLimit`) и регистрации (`RegistrationRateLimit`). Это позволяет:
1. **User enumeration** — разные сообщения об ошибке для существующего/несуществующего email
2. **Brute force** reset-кода — 6-символьный код без троттлинга

**Фикс:** Добавить `PasswordResetRateLimit` middleware (например, 3/min).

---

### C7. Malformed HTML в layout.html — склеенные meta-теги

**Файл:** `internal/domain/user/templates/layout.html:14`

```html
<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">arge_image">
```

**Проблема:** Тег `twitter:card` `summary_large_image` склеился с предыдущей строкой. `>` после `black-translucent"` преждевременно закрывает meta. `twitter:card` отсутствует на всех страницах, кроме `games-show.html`.

---

### C8. json.Marshal errors silently ignored

**Файлы:**
- `internal/domain/user/webauthn_handler.go:134, 270`
- `internal/pkg/cache/valkey.go:281`

**Проблема:** `json.Marshal` может вернуть ошибку (например, из-за несериализуемого поля). В webauthn_handler это приведёт к сохранению `"null"` в сессии → пользователь получит "Неверные данные сессии". В valkey — сохранятся `null` байты.

**Фикс:** Проверять ошибку, возвращать 500 при ошибке маршалинга.

---

### C9. i18n не подключён к шаблонам

**Файлы:** Все 60+ шаблонов в `internal/domain/*/templates/*.html`

**Проблема:** Пакет `internal/pkg/i18n/` содержит 256 строк на русском и английском, функции `T()` и `TF()`. Но они **не зарегистрированы** в `template.FuncMap` шаблонизатора. Все тексты в шаблонах — хардкод на русском. Переключение языка через `i18n.Middleware()` не работает, потому что шаблоны не используют `{{ T "key" }}`.

**Фикс:** Зарегистрировать `i18n.T` и `i18n.TF` в `template.FuncMap` через middleware (извлекать язык из контекста).

---

### C10. Missing icon-180x180.png — 404 на iOS

**Файлы:** `internal/domain/user/templates/layout.html:22`, `static/icons/`

**Проблема:** На каждой загрузке страницы на iOS браузер пытается загрузить `/static/icons/icon-180x180.png`, который не существует (404). Нет apple-touch-icon для ретина-дисплеев.

---

## 🟠 HIGH — нужно исправить в ближайшее время

### H1. SSE Stop() race с Broadcast()

**Файл:** `internal/domain/game/sse_handler.go:86-99`

`Stop()` очищает `m.sessions` под lock-ом, но `Broadcast()` уже скопировала список сессий до lock-а и будет писать в невалидные ResponseWriter-ы.

### H2. Mutex held across I/O в SSE Broadcast

**Файл:** `internal/domain/game/sse_handler.go:181-189`

`Write()` и `Flush()` выполняются под `s.mu.Lock()`. Если клиент медленный (TCP backpressure), все Broadcast-ы к этому клиенту блокируются.

### H3. defer в цикле внутри транзакции

**Файл:** `internal/domain/level/level_progress_service.go:338-357`

`defer onCommitCopy()` в цикле `for` внутри транзакции. Все `defer`-ы выполняются LIFO после коммита транзакции.

### H4. Fire-and-forget goroutines в auth_handler

**Файл:** `internal/domain/user/auth_handler.go:451`

Горутина отправки email без errgroup, без WaitGroup, без recovery. При shutdown сервера продолжает работать.

### H5. /metrics и /swagger доступны всем аутентифицированным пользователям

**Файл:** `internal/app/router.go:97-110`

Используется `OptionalAuth` + проверка `userID != 0` — но не проверяется роль admin. Любой залогиненный пользователь видит полный Swagger и метрики Prometheus.

### H6. Logout — GET запрос (CSRF-forced)

**Файл:** `internal/domain/user/routes.go:52`

GET logout — может быть вызван через `<img src="...">`. Должен быть POST.

### H7. Password min length: 6 в binding vs 8 в валидаторе

**Файл:** `internal/domain/user/handler.go:49`

```go
type RegisterInput struct {
    Password string `form:"password" binding:"required,min=6,max=72"`
```

GIN binding проверяет min=6, сервис проверяет min=8 — несоответствие.

### H8. RatingService — контекст не пробрасывается

**Файл:** `internal/domain/game/rating_service.go:28-89, 91-99, 140-150`

`UpdateRatingsForGame` принимает `ctx`, но все DB-запросы внутри него делаются **без** `WithContext(ctx)`. Запросы не отменяются при timeout/cancellation.

### H9. N+1 в RatingService.UpdateRatingsForGame

**Файл:** `internal/domain/game/rating_service.go:45-85`

На каждое прохождение (passing) — отдельный запрос членов команды. 50 прохождений × 5 участников = 301 запрос.

### H10. TournamentService.Apply без транзакции

**Файл:** `internal/domain/tournament/service.go:121-193`

`AddTeam` успешен, затем `CreatePassing` падает на 3-й итерации — команда зарегистрирована в турнире без прохождений. Нужен `Transaction`.

### H11. Cookie Secure flag зависит от X-Forwarded-Proto

**Файл:** `internal/domain/user/handler.go:13-32`

`isHTTPS()` проверяет `X-Forwarded-Proto` — заголовок, который клиент может подделать, если приложение не за reverse proxy. Для cookie `jwt`, `refresh_token`, CSRF это критично.

### H12. Debounced search — полная перезагрузка страницы

**Файл:** `internal/domain/game/templates/games-list.html:234-239`

debounce 300ms → `form.submit()` → полная перезагрузка страницы. Скролл теряется, UX ужасный. Нужен AJAX/fetch.

### H13. Auto-save сохраняет пароли в localStorage

**Файл:** `static/js/app.js:230-267`

`initAutoSaveDrafts()` сохраняет ВСЕ поля формы. Если форма с атрибутом `data-autosave` содержит поле пароля — пароль попадает в localStorage в открытом виде.

---

## 🟡 MEDIUM — стоит исправить

### M1. isStopped() использует Lock() вместо RLock()

**Файл:** `internal/pkg/websocket/room_hub.go:216-220`

Чтение поля `stopped` под полной блокировкой сериализует всех читателей.

### M2. Cache removeExpired блокирует все операции

**Файл:** `internal/pkg/cache/cache.go:95-106`

Write lock на всё время итерации всех ключей (тысячи). Блокирует Get/Set/Delete.

### M3. Global rate limiter vars без синхронизации

**Файл:** `internal/pkg/middleware/rate_limiter.go:247-301`

`globalRateLimiter` и др. читаются без атомарных операций или mutex.

### M4. Uploads без Content-Disposition

**Файл:** `internal/app/router.go:175`

`/uploads/` раздаётся через `r.Static` — файлы могут отображаться в браузере inline (особенно PDF).

### M5. HSTS только при HTTPS (не на первом HTTP)

**Файл:** `internal/pkg/middleware/security.go:72-75`

HSTS не отправляется на первый HTTP-запрос → возможен SSL stripping.

### M6. Встроенные onclick обработчики нарушают CSP nonce

**Файлы:** `internal/domain/level/templates/levels-list.html:30-33`, `webauthn-manage.html:24`

`onclick="duplicateLevel(...)"` не работает с nonce-based CSP. Использовать `addEventListener`.

### M7. Валидация ошибок строковыми совпадениями

**Файл:** `internal/domain/game/templates/gameplay-show.html:277-287`

```js
if (errorMsg.indexOf('already completed') !== -1 || errorMsg.indexOf('завершён') !== -1)
```

Хрупко. Использовать числовые error-коды с сервера.

### M8. Старые WebSocket соединения не закрываются при reconnect

**Файл:** `static/js/ws-client.js:24-38`

При вызове `connect()` на уже CONNECTING сокете старый `this.ws` не закрывается.

### M9. Отсутствует favicon для разных устройств

В layout есть ссылки на favicon.ico, но нет PNG-favicon для современных браузеров.

### M10. Нет print стилей

При печати страницы навигация, кнопки и оверлеи выглядят плохо.

### M11. Touch-устройства: hover-состояния залипают

Нет `@media (hover: hover)` для ховеров. На мобильных после тапа ховер остаётся активным.

### M12. Leaflet assets не кешируются Service Worker

**Файл:** `static/sw.js:4-11`

leaflet.js и leaflet.css не в STATIC_ASSETS — офлайн-карты не работают.

### M13. Нет beforeunload для dirty-форм

При случайной навигации с заполненной формой данные теряются.

### M14. Аватар сабмитится без превью

Пользователь ткнул не в тот файл — он сразу улетел на сервер.

### M15. Скелетон всегда в DOM, виден при отключённом JS

`games-list.html:53-62` — skeleton всегда рендерится, контент начинается как `hidden`. Если JS не загрузился — skeleton виден вечно.

### M16. Секрет сессии используется и для CSRF

`app.go:86` — один и тот же `Session.Secret` для подписи cookie сессии и CSRF-токенов. Нарушение key separation.

---

## 🔧 Оптимизации производительности

| # | Область | Что делать | Оценка |
|---|---------|-----------|--------|
| P1 | `RatingService.UpdateRatingsForGame` | Batch-load team members через JOIN, не N+1 | ~5× быстрее |
| P2 | `level_progresses` partial index | `CREATE INDEX ... WHERE finished_at IS NULL` | ~10× для CheckTimeouts |
| P3 | `Cache.removeExpired` | Итерация под RLock, удаление под отдельным Lock | Не блокирует все операции |
| P4 | SSE запись | Использовать буферизированный канал вместо mutex+Write | Не блокирует Broadcast |
| P5 | `isStopped → RLock` | Разрешить параллельное чтение stopped | Снижает конкуренцию |
| P6 | `GetTeamsByUserID` | UNION вместо двух запросов | 2× меньше запросов |
| P7 | strings.Builder для SQL | Уже используется — отлично | ✅ |
| P8 | `Session(gorm.Session{NewDB: true})` | Изолировать Count от Find в пагинации | Стабильнее |

---

## 🎨 Улучшения пользовательского опыта

| # | Что | Где | Эффект |
|---|-----|-----|--------|
| U1 | Подключить i18n к шаблонам | `render/helper.go` + все шаблоны | Полноценная локализация (ru/en) |
| U2 | AJAX-поиск игр вместо `form.submit()` | `games-list.html` | Без перезагрузки |
| U3 | Превью аватара перед загрузкой | `profile-show.html` | Контроль качества |
| U4 | beforeunload для dirty-форм | Все формы | Не терять данные |
| U5 | Loading state на submit | Все формы | Обратная связь |
| U6 | Modal confirm вместо native confirm | `webauthn-manage.html`, `levels-list.html` | Консистентность UI |
| U7 | Error-коды вместо string match | `gameplay-show.html` | Надёжность |
| U8 | debounce → throttle для поиска | `games-list.html` | Меньше запросов |
| U9 | Print styles | `app.css` | Принт-френдли |
| U10 | `@media (hover: hover)` | `app.css` | Мобильный UX |
| U11 | aria-label на все иконки | layout.html, шаблоны | A11y |
| U12 | Label вместо placeholder | `home.html` | WCAG compliance |

---

## 📊 Статистика по приоритетам

| Приоритет | Количество | Описание |
|-----------|-----------|----------|
| 🔴 CRITICAL | 10 | Паника, потеря данных, race condition, security hole |
| 🟠 HIGH | 13 | Ошибки бизнес-логики, UX-баги, утечки памяти |
| 🟡 MEDIUM | 16 | Code quality, производительность, доступность |
| 🟢 LOW | 8 | Косметика, best practices |

---

## Заключение

Проект в хорошем состоянии, но есть **10 критических проблем**, которые нужно исправить до production:

1. **Race condition** в SSE (`sse_handler.go`)
2. **Self-deadlock** в RoomHub (`room_hub.go`)
3. **RatingService.Scan** — возвращает нули для всех игр
4. **Goroutine leak** в notification/routes.go
5. **Upsert error shadow** в tournament/service.go
6. **Rate limiting** на password reset
7. **Malformed HTML** в layout.html
8. **json.Marshal errors** игнорируются в 3 местах
9. **i18n** не подключён к шаблонам (ключевая фича не работает)
10. **Missing icon** 180×180 → 404 на iOS

После их исправления — Medium-приоритетные оптимизации производительности и UX.
