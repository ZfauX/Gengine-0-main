# Deep Review (pass 13) — июль 2026

## Покрытие

| Инструмент | Результат |
|-----------|-----------|
| `golangci-lint run ./...` | Чисто |
| `go vet ./...` | Чисто |
| `go build ./...` | OK |
| `go test -short ./...` | 35/35 ✅ |

> Предыдущий раунд (pass 12) исправил 15 проблем (10 Critical + 5 High).
> Этот раунд (pass 13) находит **новые** проблемы, не замеченные ранее.

---

## 🔴 CRITICAL — нужно исправить немедленно

### C1. CSP nonce: inline `onclick` handlers не работают (21 нарушение)

**Файлы:** 13 файлов в `internal/domain/*/templates/*.html`

**Проблема:** Политика CSP (`security.go`) запрещает `'unsafe-inline'` и `'unsafe-hashes'`. Все HTML-атрибуты `onclick`, `onkeypress`, `onsubmit`, `ondragstart` и т.д. **молча блокируются браузером**. Кнопки выглядят кликабельными, но ничего не происходит.

**Поражённый функционал:**
| Файл | Хендлер | Что сломано |
|------|---------|-------------|
| `levels-list.html:18` | `ondragstart/ondragover/ondrop` | Drag & drop сортировка уровней |
| `levels-list.html:30-33` | `onclick="duplicateLevel/moveLevel/deleteLevel"` | Кнопки действий над уровнем |
| `webauthn-manage.html:7` | `onclick="registerPasskey()"` | Добавление passkey |
| `webauthn-login-button.html:2` | `onclick="loginWithPasskey()"` | Вход по passkey |
| `games-photos.html:53` | `onclick="closePhotoModal()"` | Закрытие модалки фото |
| `games-list.html:68` | `onclick="window.location.href"` | Клик по карточке игры |
| `offline.html:23` | `onclick="location.reload()"` | Кнопка "Повторить" |
| `errors-*.html:5 файлов` | `onclick="history.back()"` | Кнопки "Назад" |
| `monitor-page.html:188` | `onclick="disqualifyTeam()"` | Дисквалификация команды |

**Фикс:** Заменить все HTML-атрибуты `on*` на `addEventListener` в `<script nonce="{{.csp_nonce}}">` блоках.

---

### C2. Inline `onkeypress` заблокирован CSP (5 файлов)

**Файлы:** `auth-login.html:17`, `auth-register.html:18`, `auth-reset.html:18`, `auth-forgot.html:20`, `gameplay-test.html:35`

**Проблема:** `onkeypress="if(event.key === 'Enter') this.form.submit()"` заблокирован CSP. К счастью, Enter и так работает через нативную отправку формы. Это dead code, который нужно удалить.

---

### C3. 2FA Enable — верификация всегда проваливается

**Файл:** `internal/domain/user/two_factor_handler.go:57-110`

**Проблема:** Флоу включения 2FA сломан:
1. `EnableForm()` генерирует секрет и показывает QR-код
2. Секрет **нигде не сохраняется** (не в сессии, не в форме)
3. `Enable()` достаёт пользователя из БД — в `user.TwoFactorSecret` **пусто** (2FA ещё не включена)
4. `VerifyCode(user.TwoFactorSecret, input.Code)` проверяет код против пустой строки → всегда false

**Результат:** 2FA невозможно включить. Секрет должен передаваться через скрытое поле формы или храниться в сессии между шагами.

---

### C4. 2FA — отсутствуют 3 шаблона

**Файлы:** `internal/domain/user/templates/` — нет файлов:
- `user-2fa-enable.html`
- `user-2fa-enabled.html`
- `user-2fa-disable.html`

**Проблема:** Обращение к `/user/2fa/enable` или `/user/2fa/disable` вызывает панику шаблонизатора (missing template). Весь пользовательский интерфейс 2FA не работает.

---

### C5. `renderGameplayError` — паника в шаблоне

**Файл:** `internal/domain/game/gameplay_handler.go:123-129`
**Темплейт:** `gameplay-show.html` (обращается к `{{.Level.Name}}`, `{{.Level.Questions}}`)

**Проблема:** `renderGameplayError` передаёт только `PassingID`, `Error`, `csrf`. Но шаблон немедленно обращается к `.Level` (nil) → `html/template` паникует.

**Вызывается из:** `SubmitCode:199`, `SubmitFile:266,272`.

---

### C6. Admin handlers — редирект без flash-сообщения

**Файл:** `internal/domain/admin/handler.go:206-275, 370-391`

**Проблема:** 3 хендлера (`ToggleAdmin`, `DeleteUser`, `DeleteGame`) при ошибке делают редирект на список без flash-сообщения. Пользователь видит успешный редирект без индикации ошибки.

```go
if err := h.userRepo.Delete(c.Request.Context(), req.ID); err != nil {
    log.Error().Err(err).Msg("DeleteUser: failed")
    c.Redirect(http.StatusFound, "/admin/users")
    return  // пользователь НЕ ВИДИТ, что произошла ошибка
}
```

**Фикс:** Использовать `render.SetFlash(c, "error", msg)` перед редиректом.

---

### C7. `initAutoSaveDrafts` — пароли сохраняются в localStorage

**Файл:** `static/js/app.js:234`

```js
var fields = form.querySelectorAll('input, textarea, select'); // включает type="password"
```

**Проблема:** Если форма с `data-autosave` содержит поле пароля, он сохраняется в localStorage в открытом виде.

**Фикс:** `input:not([type="password"]):not([type="hidden"])`

---

### C8. WebSocket — orphaned connections при reconnect

**Файл:** `static/js/ws-client.js:24-38`

**Проблема:** `connect()` создаёт новый `WebSocket`, не закрывая старый, если он ещё в `CONNECTING` или `OPEN`. Старый сокет теряет ссылку, но продолжает висеть на сервере.

**Фикс:** Проверять `this.ws.readyState` и закрывать перед созданием нового.

---

## 🟠 HIGH — нужно исправить в ближайшее время

### H1. Leaflet assets не кешируются Service Worker

**Файл:** `static/sw.js:4-11`

`STATIC_ASSETS` не включает `/static/js/leaflet.js` и `/static/css/leaflet.css`. Офлайн-карты не работают.

### H2. Password hint mismatch: `minlength="6"` vs backend `min=8`

**Файлы:** `auth-register.html:33`, `auth-reset.html:16`

HTML5 validation позволяет 6 символов, но хинт и бэкенд требуют 8.

### H3. SSE indicator interval — утечка при множественных вызовах

**Файл:** `static/js/app.js:658-666`

`checkInterval` — локальная переменная. При повторных вызовах `initSSEGameNotifications()` старый interval не очищается. Есть 30s safety cap, но при многих вызовах intervals накапливаются.

### H4. ChatWS — логирует не ту ошибку

**Файл:** `internal/domain/monitor/handler.go:389`

```go
log.Warn().Err(err).Str("room_id", roomID).Msg("ChatWS: room not found")
```

Логируется `err` (результат `strconv.Atoi`, который успешен), а должен быть `findErr` (результат DB-запроса).

### H5. ImportGame — пропущен `WithContext`

**Файл:** `internal/domain/export/handler.go:196`

`h.exportService.ImportGameFromCSV(h.db, gameID, file)` — передаёт `h.db` напрямую, а не `h.db.WithContext(c.Request.Context())`. Транзакция импорта не отменяется при disconnect.

### H6. MonitorWS/LogsWS — нет `c.Abort()` после upgrade

**Файл:** `internal/domain/monitor/handler.go:273`

После WebSocket upgrade не вызывается `c.Abort()`. Gin может попытаться записать ответ в уже захваченное соединение. `ChatWS` (line 416) делает `c.Abort()` правильно.

### H7. `decConnection` удалена, остался вызов из cleanup (исправлено)

**Статус:** ✅ **ИСПРАВЛЕНО** — `decConnection` удалена, `cleanupInactiveClients` вызывает `decConnectionNoLock` напрямую.

### H8. Export team query — пропущен `WithContext`

**Файл:** `internal/domain/export/handler.go:439`

`h.db.Table("teams")...` без `WithContext`, в отличие от соседних запросов (строки 429, 450).

### H9. Password reset rate limit — неспецифичная ошибка

**Файл:** `internal/pkg/middleware/rate_limiter.go:443`

`PasswordResetRateLimit` использует `ErrRateLimitGlobal` вместо собственного `ErrRateLimitPasswordReset`. Нет отдельного i18n-ключа. Пользователь видит общее сообщение "слишком много запросов".

---

## 🟡 MEDIUM

### M1. ImportGame — raw `err.Error()` в HTTP ответе

**Файл:** `internal/domain/export/handler.go:200`

```go
"Error": "Ошибка импорта: " + err.Error()
```

Может утечь внутренние детали (имена таблиц, constraint names) при ошибках GORM.

### M2. Redirect без `game_id` — битый URL

**Файл:** `internal/domain/game/gameplay_handler.go:337`

`/games/` + `c.Query("game_id")` + `/monitor` — если query-параметр отсутствует, URL становится `/games//monitor`.

### M3. Service Worker — нет background sync

Офлайн-отправка форм (например, создание игры) не работает. Формы теряются при отсутствии соединения.

### M4. View Transitions API — Chrome-only, нет fallback

`@view-transition { navigation: auto; }` — экспериментальная Chrome-фича. Safari/Firefox игнорируют, но навигация через HTMX уже даёт плавные переходы.

### M5. Skeleton loading всегда в DOM при выключенном JS

`games-list.html:53-62` — skeleton виден вечно, если JS не загрузился.

### M6. Аватар — нет превью перед загрузкой

Пользователь выбирает файл → форма сразу сабмитится. Нет подтверждения.

### M7. Debounced search — полная перезагрузка

`games-list.html:234-239` — 300ms debounce → `form.submit()`. Скролл теряется.

### M8. Нет `beforeunload` для dirty-форм

При случайной навигации данные форм теряются.

### M9. Cookie `refresh_token` заскоплен на `/auth/refresh`

Это корректно для безопасности, но при logout очищается только cookie с этим путём. Если другое приложение установило cookie с тем же именем на `/` — она останется.

---

## 🔧 Оптимизации производительности

| # | Область | Статус |
|---|---------|--------|
| P1 | `Cache.removeExpired` — write lock на всю итерацию | ❌ Не исправлено |
| P2 | `isStopped()` — `Lock()` вместо `RLock()` | ✅ **Исправлено** (убрана, код прямой) |
| P3 | RoomHub broadcast — stale room reference | ⚠️ Частично (убрали deadlock, остался stale ref) |
| P4 | N+1 в RatingService (passing → team members) | ❌ Не исправлено |
| P5 | Partial index для `level_progresses WHERE finished_at IS NULL` | ❌ Не исправлено |

---

## 📊 Статистика

| Приоритет | Новые | Всего (с pass 12 осталось) |
|-----------|-------|--------------------------|
| 🔴 CRITICAL | 8 | 8 |
| 🟠 HIGH | 9 | 9 |
| 🟡 MEDIUM | 9 | 9 |
| 🔵 LOW | 3 | 3 |

---

## Резюме

После исправления 15 проблем из pass 12, pass 13 обнаружил **новые 29 проблем**.

### Топ-3 по реальному impact:

1. **Inline onclick заблокированы CSP** (C1) — 21 интерактивный элемент не работает: пасскей аутентификация, drag & drop, кнопки навигации, дисквалификация команд. **Вся клиентская интерактивность через HTML-атрибуты сломана.**
2. **2FA Enable всегда проваливается** (C3) — секрет не передаётся между шагами. 2FA невозможно включить.
3. **2FA шаблоны отсутствуют** (C4) — страницы 2FA (/user/2fa/enable, /disable) вызывают панику.

### Что уже исправлено (в этом раунде):
- `golangci-lint` — чисто (удалена dead `decConnection`, gofmt)
- `decConnection` удалена, cleanup использует `decConnectionNoLock`
- Password rate limit middleware добавлен
- /swagger и /metrics требуют admin
- Logout — POST с CSRF
- isHTTPS — безопасен (только `c.Request.TLS`)
