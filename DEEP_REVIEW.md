# Deep Review (pass 14) — июль 2026

## Покрытие

| Инструмент | Результат |
|-----------|-----------|
| `golangci-lint run ./...` | ✅ Чисто |
| `go vet ./...` | ✅ Чисто |
| `go build ./...` | ✅ OK |
| `go test -short ./...` | ✅ 35/35 |
| `go generate ./internal/app/` | ✅ Не требуется |

> После pass 13 (29 исправлений), pass 14 проверяет качество исправлений и ищет
> проблемы, не замеченные в предыдущих раундах.

---

## 🔴 CRITICAL

### C1. 2FA Disable — несоответствие полей формы

**Файл:** `internal/domain/user/two_factor_handler.go:238`, `user-2fa-disable.html:22`

**Проблема:** Шаблон отправляет поле `password`, хендлер читает `code`.

| Компонент | Поле |
|-----------|------|
| Шаблон `<input>` | `name="password"` (подпись: «Подтвердите паролем») |
| Хендлер `struct { Code string \`form:"code"\` }` | Читает несуществующий `code` |

**Следствие:** `ShouldBind` всегда возвращает ошибку required. Хендлер редиректит `/user/2fa/disable` без flash. **Пользователь никогда не сможет отключить 2FA.**

---

### C2. 2FA — CSRF-токен не передаётся в шаблоны

**Файл:** `internal/domain/user/two_factor_handler.go:70, 114, 181, 214`

**Проблема:** Ни один из 4 вызовов `render.Page()` в 2FA-хендлерах не передаёт `"csrf": csrf.GetToken(c)`. Все POST-формы содержат `{{.csrf}}`, который будет пустой строкой → **CSRF middleware ответит 403 Forbidden** на все запросы включения/отключения 2FA.

**Сравнение:** Домашняя страница (`router.go:200`) правильно передаёт `"csrf": csrf.GetToken(c)`.

---

## 🟠 HIGH

### H1. 2FA Disable не проверяет пароль пользователя

**Файл:** `internal/domain/user/two_factor_handler.go:253-261`

Хендлер `Disable` не аутентифицирует пользователя повторно. При наличии активной сессии злоумышленник может отключить 2FA. Даже после исправления C1 (поля) остаётся проблема: для отключения 2FA нужно требовать **текущий пароль** (а не TOTP).

---

### H2. `SubmitFile` — 3 error-пути без PassingID/Level (JS сломан)

**Файл:** `internal/domain/game/gameplay_handler.go:304-335`

```go
render.Page(c, http.StatusBadRequest, "gameplay-show.html", gin.H{
    "Error": "Размер файла не должен превышать 10 МБ",
    "csrf":  csrf.GetToken(c),
    // ❌ Нет PassingID, Level, Attempts
})
```

Шаблон ожидает `data-passing-id="{{.PassingID}}"` на контейнере для JS-инициализации. Без него таймер, SSE, AJAX-отправка не работают. Пользователь видит статичную страницу без контекста уровня.

**В 3 местах:** строки 305, 318, 331.

---

### H3. Нет секции 2FA на странице профиля

**Файл:** `internal/domain/user/templates/profile-show.html`

Нет ссылки «Включить 2FA» / статуса «2FA включена». Пользователь не узнаёт о существовании 2FA без знания прямого URL `/user/2fa/enable`.

---

### H4. Backup-коды теряются после ухода со страницы

**Файл:** `internal/domain/user/templates/user-2fa-enabled.html`

Коды показываются один раз при включении. Если пользователь обновит страницу или перейдёт по ссылке — коды потеряны. Нет страницы повторного просмотра или регенерации.

---

## 🟡 MEDIUM

### M1. Missing VerificationCode в service_test.go

**Файл:** `internal/domain/user/service_test.go:396-401`

Тест создаёт `EmailVerificationToken` без `VerificationCode`:
```go
token := &EmailVerificationToken{
    UserID:    user.ID,
    TokenHash: hashToken("validtoken"),
    ExpiresAt: time.Now().Add(time.Hour),
    // ❌ VerificationCode не указан
}
```

В PostgreSQL `uniqueIndex` не допускает пустые строки `''`. Тест проходит только потому, что первый токен удаляется перед созданием второго (`VerifyToken` → `DeleteToken`). Если тесты когда-либо запустятся параллельно с `t.Parallel()`, упадёт unique constraint violation.

---

### M2. Duplicate `audit.Service` instance в админке

**Файл:** `internal/domain/admin/routes.go:29`, `internal/app/router.go:212`

`admin.RegisterRoutes()` возвращает `*audit.Service`, но `router.go:212` игнорирует возвращаемое значение. Основной `audit.Service` создаётся в `NewDependencies()` (`app.go:33`). Два независимых экземпляра — если сервис имеет внутреннее состояние (буферизированные события), они расходятся.

---

### M3. `SubmitCode` — errors без .Level как fallback

**Файл:** `internal/domain/game/gameplay_handler.go:188-210`

При ошибках валидации рендер без `.Level` и `.Attempts`. Nil-guards в шаблоне не дают упасть, но пользователь не видит уровень и историю попыток (плохой UX).

---

### M4. `ChatWS` — `findErr` не логируется

**Файл:** `internal/domain/monitor/handler.go:404`

```go
log.Warn().Uint("user_id", userID).Uint("game_id", *chatRoom.GameID).Msg("ChatWS: access denied, not a participant")
```
Отсутствует `.Err(findErr)`. При отладке потеряна информация о реальной причине отказа в доступе.

---

### M5. `MonitorData` — неверный префикс в логе

**Файл:** `internal/domain/monitor/handler.go:152`

```go
log.Error().Err(err).Uint("game_id", req.ID).Msg("MonitorWS: failed to get snapshot")
```
Должно быть `"MonitorData:"` — копипаста из MonitorWS хендлера.

---

### M6. QR-код через внешний API (приватность)

**Файл:** `internal/domain/user/templates/user-2fa-enable.html:17`

Секрет 2FA передаётся в URL внешнего сервиса `api.qrserver.com`. Рекомендация: генерировать QR-код локально (`github.com/skip2/go-qrcode`).

---

## ✅ Проверка исправлений pass 13

| # | Проблема | Статус |
|---|----------|--------|
| C1 | 21 inline onclick заблокированы CSP | ✅ Все заменены на addEventListener |
| C2 | 5 onkeypress заблокированы | ✅ Удалены |
| C3 | 2FA секрет теряется | ✅ Хранится в сессии |
| C4 | 3 шаблона 2FA отсутствуют | ✅ Созданы |
| C5 | renderGameplayError паникует | ✅ nil-guards + 2-level fallback |
| C6 | Admin редиректы без flash | ✅ SetFlash на всех error-путях |
| C7 | Пароли в localStorage | ✅ Исключены password/hidden поля |
| C8 | WebSocket orphaned connections | ✅ Закрытие старого WS перед reconnect |
| H1 | Leaflet не в SW cache | ✅ В STATIC_ASSETS |
| H2 | Password minlength=6 | ✅ Исправлено на 8 |
| H3 | SSE interval leak | ✅ module-level var + cleanup |
| H4 | ChatWS err→findErr | ✅ Исправлено |
| H5 | ImportGame без context | ✅ WithContext |
| H6 | c.Abort() после WS upgrade | ✅ MonitorWS + LogsWS |
| H8 | Export team query без context | ✅ WithContext |
| H9 | Rate limit error message | ✅ ErrRateLimitPasswordReset |

---

## 📊 Статистика

| Приоритет | Количество |
|-----------|-----------|
| 🔴 CRITICAL | 2 |
| 🟠 HIGH | 4 |
| 🟡 MEDIUM | 6 |
| ✅ Исправлено (pass 13) | 16/16 |

---

## Резюме

После 14 раундов ревью в проекте остались **2 критические и 4 высокие проблемы** — все в области 2FA (которая была реализована в pass 13):

1. **2FA Disable сломан** (C1) — форма отправляет password, хендлер читает code
2. **CSRF не передаётся** в 2FA-шаблоны (C2) — все POST-запросы получат 403
3. **Disable не проверяет пароль** (H1) — high severity
4. **SubmitFile ошибки без PassingID** (H2) — JS интерактив не работает
5. **2FA нет в профиле** (H3) — пользователь не может найти настройки
6. **Backup-коды теряются** (H4)

**Всё остальное:** 16/16 исправлений из pass 13 работают корректно. Линтер, тесты, архитектура — чисто.
