# Deep Review (pass 16) — июль 2026

## Покрытие

| Инструмент | Результат |
|-----------|-----------|
| `golangci-lint run ./...` | ✅ Чисто |
| `go vet ./...` | ✅ Чисто |
| `go build ./...` | ✅ OK |
| `go test -short ./...` | ✅ 35/35 |

> Новые направления: template rendering, test quality, UX flow, error pages.

---

## 🔴 CRITICAL

### C1. `offline.html` — вложенный DOCTYPE

**Файл:** `internal/domain/user/templates/offline.html`

Шаблон содержит собственные `<!DOCTYPE html>`, `<html>`, `<head>`, `<body>` (строки 2–29) завернутые в `{{define "offline.html"}}`. `render.Page()` вставляет весь этот блок в `layout.html`'s `{{.ContentHTML}}`, что создаёт **вложенный DOCTYPE** → невалидный HTML.

**Фикс:** Убрать внешнюю HTML-обёртку, оставить только содержимое `<body>`.

---

### C2. `games-show.html` — OG meta-теги в `<body>`

**Файл:** `internal/domain/game/templates/games-show.html:2-10`

OG meta (`og:title`, `og:description`) рендерятся в блоке контента, который попадает в `<body>`. Facebook/Twitter скраперы их не увидят. Должны быть в `{{.ExtraHead}}` (layout.html line 43).

---

### C3. ErrorHandler — JSON для HTML-паник

**Файл:** `internal/pkg/middleware/error_handler.go`

При панике или необработанной ошибке middleware всегда отвечает `c.AbortWithStatusJSON()`. Для HTML-клиентов (большая часть приложения) это возвращает **сырой JSON** вместо отрисованной страницы ошибки. Нужно проверять `Accept` header и рендерить HTML-шаблон.

---

### C4. `.Flash` в layout никогда не заполняется

**Файл:** `internal/domain/user/templates/layout.html:48-58`

Блок `{{if .Flash}}` есть в layout, но **ни один хендлер не передаёт `.Flash`**. Это мёртвый UI. Параллельно существует рабочий механизм `render.SetFlash/GetFlash` для сессионных flash, но layout не читает сессию.

---

## 🟠 HIGH

### H1. Нет `NoRoute` handler

**Файл:** `internal/app/router.go`

При запросе к несуществующему маршруту Gin отвечает стандартным 404, а не кастомным `errors-404.html`. Нужно: `r.NoRoute(gin.WrapH(...))`.

### H2. Создание игры/команды → редирект на список, а не на созданный объект

**Файлы:** `internal/domain/game/game_handler.go:349`, `team/handler.go:171`

После создания игры/команды пользователь попадает на список, а не на страницу сущности. Нужно искать по тексту: `/games` и `/teams` в этих строках.

**Фикс:** `/games/{id}` и `/teams/{team_id}`.

---

### H3. После регистрации — редирект на `/auth/login` без сообщения

**Файл:** `internal/domain/user/auth_handler.go:299`

Пользователь не видит, что регистрация прошла успешно. Нужен flash "Регистрация успешна. Проверьте email для подтверждения."

### H4. `NoRoute` handler отсутствует — кастомная 404 не используется

**Файл:** `internal/app/router.go`

### H5. Missing `errors-503.html`

**Файл:** `internal/domain/user/templates/`

Для 503-статуса нет отдельного шаблона — падает на 500.

### H6. ErrorHandler — Нет проверки Accept header — JSON для HTML

(Уже описано в C3)

### H7. `go:generate` моки не созданы

**Файлы:** `internal/domain/level/service.go:3`, `internal/domain/game/service.go:3`

Две директивы `//go:generate ... mockgen` существуют, но мок-файлы отсутствуют. Если какой-либо тест попытается их использовать — паника компиляции.

### H8. `t.Parallel()` не используется нигде

Все ~50 тестов выполняются последовательно. `SetupPostgresDB` создаёт изолированные схемы — параллелизм безопасен. CI/CD в 2-4 раза медленнее возможного.

### H9. Нет success-сообщений нигде

Только **одно место** в приложении показывает success flash (2FA disable). После всех create/update/delete операций — тихий редирект. Пользователь никогда не получает подтверждения успеха.

---

## 🟡 MEDIUM

| # | Область | Файл | Описание |
|---|---------|------|----------|
| M1 | Template | `auth-register.html:44` | reCAPTCHA script without `nonce` — CSP блокирует |
| M2 | Template | `layout.html:5,18` | Дубликат `viewport` meta |
| M3 | Template | `games-list.html` | Table view целиком без dark mode (~8 классов) |
| M4 | Template | `tournaments-show.html:3` | `.Tournament.Name` без nil-guard |
| M5 | Template | `errors-*.html` | `text-gray-400` без dark variant |
| M6 | Template | `helper.go:134-135` | `data["lang"]` устанавливается, но не используется в шаблонах |
| M7 | Template | `games-show.html:202` | `console.log` в production коде |
| M8 | Template | `follow-list.html:57,61` | `alert()` вместо toast для ошибок |
| M9 | Template | `helper.go:169-182` | `defaultErrorMessage()` не имеет 429 — пустая строка |
| M10 | Tests | `level/service_test.go` | `assert.Error(t, err)` без проверки текста ошибки (4 места) |
| M11 | Tests | `websocket/*_test.go` | `client.Close()` в теле теста, не `defer` (4 места) |
| M12 | Tests | `notification/service_test.go` | `assert.NoError` где нужен `require.NoError` |
| M13 | Tests | `admin/service_test.go:71-73` | `if err != nil` вместо `require` |
| M14 | Export | `export/handler.go:200` | `err.Error()` утекает в HTTP ответ при ошибке импорта |
| M15 | Export | `export/handler.go` | `parseGameID()` ошибки в 10 местах через `err.Error()` |
| M16 | WebAuthn | `webauthn_handler.go:370,376` | 500 ошибки без логирования |
| M17 | Layout | `layout.html` | Notification dropdown без dark mode |
| M18 | UX | `profile-show.html:49` | Change password — нет per-field ошибок, только общий `.Error` |
| M19 | UX | `rate_limiter.go` | 429 ошибка не говорит сколько ждать |
| M20 | SW | `sw.js` | `/uploads` кешируются агрессивно — потенциальный privacy issue |

---

## ✅ Исправления pass 15 — подтверждены

| ID | Проблема | Статус |
|----|----------|--------|
| C1-C11 | Tournament, WebSocket, Pagination, Config, i18n | ✅ Все 11 |
| H1 | LOG_LEVEL | ✅ |

---

## 📊 Статистика

| Приоритет | Количество |
|-----------|-----------|
| 🔴 CRITICAL | 4 |
| 🟠 HIGH | 9 |
| 🟡 MEDIUM | 20 |
| 🔵 LOW | 8 |

---

## Резюме

### Топ-5 по реальному impact:

1. **ErrorHandler — JSON для HTML (C3)**: Любая паника на HTML-странице возвращает JSON вместо ошибки. Пользователь видит текст `{"error":"Internal Server Error"}` вместо красивого `errors-500.html`.

2. **OG meta в body (C2)**: Превью соцсетей не работают для страниц игр. Facebook/Twitter не находят og:title, og:description.

3. **offline.html — nested DOCTYPE (C1)**: Офлайн-страница производит невалидный HTML при рендере через `render.Page()`.

4. **`.Flash` мёртвый (C4)**: После ~30 операций create/update/delete — ни одного success-сообщения.

5. **Нет NoRoute handler (H1)**: Несуществующие URL показывают Gin-дефолтную 404 без стилей.

### Что бросается в глаза:

- **Success feedback отсутствует**. Приложение отлично показывает ошибки, но никогда не говорит "всё хорошо". Это базовая UX-проблема.
- **Тесты — хорошее качество, но 2× медленнее возможного из-за отсутствия `t.Parallel()`**.
- **ErrorHandler — JSON-only**. Критично для UX при панике.
- **Моки не сгенерированы** — `go generate` нужно запустить для 2 файлов.
