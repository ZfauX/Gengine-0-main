# 🕵️ Gengine‑0

**Платформа для создания и проведения городских, полевых и онлайн‑квестов**

Gengine‑0 позволяет авторам проектировать многоуровневые игры с вопросами и кодами, командам — проходить их в реальном времени, а организаторам — следить за прогрессом через мониторинг с живой картой игроков. Поддерживаются турниры, рейтинги, чаты (общий/командный/личный), голосование «чёрный ящик», галерея фотографий, платежи и многое другое.

---

## 📋 Возможности

- 🔐 **Аутентификация** — JWT + refresh-токены (ротация с отзывом семейств), OAuth2 (Яндекс, VK), WebAuthn/Passkeys, 2FA (TOTP + backup-коды)
- 🎮 **Конструктор игр** — черновики, публикация, уровни, вопросы, ответы, файловая загрузка, импорт/экспорт (CSV, Excel, PDF)
- 👥 **Команды и турниры** — роли (капитан/зам/игрок), группы (основной состав/резерв), приглашения, турнирные таблицы
- 🗺️ **Геолокация игроков** — отправка координат водителей, карта в мониторинге (Leaflet + live-обновление)
- 📊 **Мониторинг в реальном времени** — WebSocket + SSE, прогресс команд, чат
- 💬 **Чаты и комнаты** — общий/капитанский/командный/флудилка/личный/серверный, права участников
- 🗳️ **Голосование (чёрный ящик)** — автор запускает голосование, участники выбирают лучший ответ
- 💳 **Платежи** — ЮKassa (создание платежа, IP-аутентифицированный вебхук, статусы, страница платежей)
- 📸 **Фотогалерея** — загрузка снимков с привязкой к уровням
- 📄 **Экспорт** — CSV, PDF, Excel, iCal
- 🌐 **i18n** — Русский и английский языки (256+ строк)
- 🛡️ **Безопасность** — CSP с nonce, CSRF, rate-limit (fail-closed для критичных путей), httpOnly cookies, строгие права доступа (в т.ч. к геоданным и Phase-3 маршрутам)
- 🎨 **Интерфейс** — Tailwind CSS v4, CSS-переменные, тёмная тема, PWA, Service Worker
- 📈 **Наблюдаемость** — zerolog, Prometheus, pprof, health-check, Sentry

---

## 🧰 Технологический стек

| Компонент | Технология |
|-----------|-----------|
| **Язык** | Go 1.25 |
| **HTTP-фреймворк** | Gin |
| **ORM** | GORM (PostgreSQL) |
| **Миграции** | golang-migrate — 59 индивидуальных + squashed-набор (7 файлов) для свежих БД |
| **Аутентификация** | JWT, OAuth2 (Яндекс, VK), WebAuthn/Passkeys, 2FA (TOTP) |
| **WebSocket** | Gorilla WebSocket + RoomHub (комнаты, лимиты, reconnecting-клиент) |
| **SSE** | Server-Sent Events (уведомления о старте/уровне/подсказке/финише) |
| **Кэш** | In-memory LRU + Valkey (Redis-compatible), единая TTL-семантика |
| **Rate-limit** | Глобальный/логин/регистрация/коды/API/SSE; критичные — fail-closed |
| **CSRF** | gorilla/csrf с глобальным fetch-перехватчиком |
| **Стилизация** | Tailwind CSS v4 (CSS-first), `@theme`, тёмная тема |
| **Логирование** | zerolog + lumberjack (ротация) |
| **Метрики** | Prometheus client_golang (+ метрики WS/SSE-подключений) |
| **Профилирование** | pprof на отдельном порту (PPROF_ENABLED) |
| **Сборка CSS** | Tailwind CLI v4 (~170ms) |
| **DI** | google/wire |
| **Тестирование** | testing + testify + go-sqlmock + gomock + Playwright (E2E) |

---

## 🚀 Быстрый старт

### Предварительные требования

- Go 1.25
- PostgreSQL 18+
- Node.js 18+ и npm (сборка CSS, E2E-тесты)

### Установка и запуск

```bash
# 1. Клонирование
git clone https://github.com/ZfauX/Gengine-0-main.git
cd Gengine-0

# 2. Go-зависимости
go mod tidy

# 3. Node.js-зависимости (CSS + Playwright)
npm install

# 4. Настройка .env
cp .env.example .env
# Отредактируйте .env: DB_HOST, DB_PASSWORD, JWT_SECRET, SESSION_SECRET, ADMIN_EMAIL/PASSWORD

# 5. Сборка CSS (Tailwind v4)
npm run build:css    # или make css

# 6. Запуск миграций БД (свежая БД применяет squashed-набор автоматически)
go run ./cmd/server -migrate

# 7. Запуск сервера (dev)
go run ./cmd/server

# 8. Полная сборка
make build            # CSS + Go binary
```

Сервер будет доступен на `http://localhost:8080`.
- Swagger: `/swagger/index.html`
- Health: `/healthz`
- Метрики: `/metrics` (только админ + 2FA)
- pprof: `/debug/pprof/*` на порту `6060` (включается `PPROF_ENABLED=true`)

---

## 📋 Команды

| Команда | Описание |
|---------|----------|
| `make build` | Сборка CSS + Go binary |
| `make dev` | `go run ./cmd/server` |
| `make css` | Сборка Tailwind CSS |
| `make test` | Все тесты с покрытием |
| `make test-short` | Unit-тесты (без БД) |
| `make test-integration` | Интеграционные тесты (`-tags=integration`, требуется PostgreSQL) |
| `make test-e2e` | Playwright E2E (требует e2e-сервер на `:8081`, см. `.env.e2e`) |
| `make lint` | `golangci-lint run ./...` |
| `make swagger` | Генерация Swagger-документации |
| `go generate ./internal/app/` | Регенерация wire DI (после изменения конструкторов) |

### E2E-тесты (Playwright)

```bash
# Один раз: установить браузер
npx playwright install chromium

# Настроить e2e-окружение (см. .env.e2e): БД gengine_e2e, RATE_LIMIT_*=100000,
# TRUSTED_PROXIES пуст (иначе CSRF-кука станет Secure). Применить миграции:
#   go run ./cmd/server -migrate  (с env из .env.e2e)
# Запустить сервер на :8081, затем:
make test-e2e
```

Покрытие: регистрация → логин → дашборд, создание команды/игры, профиль, публичные страницы, общий чат, сценарии с двумя пользователями (личный чат). Service Workers блокируются в конфиге Playwright — иначе SW подставляет устаревший CSRF-токен.

---

## 🗂️ Структура проекта

```
cmd/server/main.go          — точка входа, graceful shutdown, pprof
internal/
  app/                      — DI (wire), роутер
  config/                   — env-конфигурация с валидацией
  db/                       — подключение, миграции, admin seed
  domain/                   — бизнес-логика по доменам:
    {user,game,level,team,tournament,monitor,calendar,social,notification,admin,export,payment}/
      model.go              — GORM-модели
      service.go            — бизнес-логика (без HTTP)
      handler.go            — HTTP-хендлеры (gin)
      routes.go             — маршруты + middleware
      templates/            — HTML-шаблоны
  pkg/                      — переиспользуемые модули:
    cache/                  — in-memory LRU + Valkey
    websocket/              — RoomHub (WS-хаб + клиент)
    middleware/             — auth, CSRF, gzip, CSP, rate-limit, bodylimit
    email/                  — SMTP с persistent-очередью
    i18n/                   — интернационализация (256 строк, ru+en)
    metrics/                — Prometheus-метрики
    render/                 — HTML-рендеринг
    storage/                — файловое хранилище
migrations/                 — SQL-миграции (59 файлов)
migrations_squashed/        — сгруппированный набор (7 файлов) для свежих БД
e2e/                        — Playwright-тесты + конфиг
static/                     — CSS, JS, PWA (manifest.json + sw.js)
```

---

## 🎨 Стилизация (Tailwind CSS v4)

Проект использует **Tailwind CSS v4** с CSS-first конфигурацией.

### Ключевые особенности
- **`@theme`** в `app.css` — определяет цвета, шрифты, тени (вместо `tailwind.config.js`)
- **`@custom-variant dark (&:where(.dark, .dark *))`** — классовая тёмная тема
- **`@layer components`** — кастомные компоненты (`.btn`, `.card`, `.toast`) через чистый CSS
- **CSS-переменные** — `--color-bg-card`, `--color-text-primary` для тёмной темы
- **Сборка:** `tailwindcss -i ./static/css/app.css -o ./static/css/output.css --minify` (~170ms)
- **Watch-режим:** `npm run watch:css`

### Тёмная тема
Переключается классом `.dark` на `<html>`. `dark:` префиксы Tailwind работают во всех шаблонах.

---

## 🔒 Безопасность

- **Секреты** — все ключи в env, минимальная длина 32 символа, проверка сложности
- **JWT** — httpOnly cookie, refresh-токены в БД с отзывом (семейства)
- **CSRF** — gorilla/csrf, `X-CSRF-TOKEN` для AJAX, `_csrf` для форм
- **CSP** — `script-src 'nonce-...'`, `form-action 'self'`, строгие директивы
- **CORS** — настраивается через `CORS_ORIGINS`
- **Rate-limit** — глобальный, логин, регистрация, code submission, API, SSE;
  критичные лимитеры (логин/регистрация/2FA/коды/сброс) работают **fail-closed**
  при недоступности Valkey — отказ кэша не отключает защиту от брутфорса
- **Права доступа** — авторизация на геолокацию и Phase-3 маршруты (маршруты команд,
  время старта, ответы, статистика) через middleware `GameManager`; чат — единая
  проверка `canJoinRoom`; личные комнаты доступны только двум участникам
- **Платежи** — вебхук ЮKassa с IP-allowlist + подтверждением статуса через API,
  классификация ошибок (400/403/500) с алертами
- **WebAuthn/Passkeys** — FIDO2 аутентификация без пароля
- **2FA** — TOTP + backup-коды (для `/admin/*`)
- **Health-check** — мониторинг БД, диска, email-очереди, Valkey, WS Hub

---

## 🌐 Интернационализация

Пакет `internal/pkg/i18n` содержит 256+ строк на русском и английском. Используется middleware для переключения языка.
Сессии — подписаны и зашифрованы с использованием SESSION_SECRET.
Пароли — хешируются bcrypt, минимальная длина 8 символов.
Структурированные ошибки в API — единый формат ответа {error, code, details} для всех JSON-эндпоинтов.
CSP-заголовки — использование nonce вместо unsafe-inline для инлайн-скриптов и стилей. Загрузка внешних ресурсов (CDN) строго контролируется.
Самоподписанный TLS‑сертификат генерируется только для разработки; для production используйте Let's Encrypt или другой доверенный центр.

---

## 📈 Наблюдаемость

- **Логирование** — все компоненты пишут структурированные логи в JSON с помощью zerolog. Уровни настраиваются через `LOG_LEVEL`.
- **Метрики Prometheus** — `/metrics` (защищён: админ + 2FA). Счётчики и гистограммы HTTP‑запросов, бизнес-метрики (игры, команды, пользователи, WebSocket- и SSE-подключения и т.д.).
- **pprof** — отдельный сервер на `PPROF_PORT` (по умолчанию 6060), включается `PPROF_ENABLED=true`; не экспонируется на основном порту. Подробнее — в разделе «Профилирование».
- **Health‑check** — `/healthz` проверяет подключение к БД и статус WebSocket-хаба.

---

## 🔬 Профилирование (pprof)

**Зачем нужен pprof-сервер.** Gengine‑0 — real-time платформа: WebSocket-чаты, SSE-уведомления, мониторинг игр, голосования. Под нагрузкой (много команд одновременно) могут возникнуть проблемы, которые сложно найти без профилировщика:

| Проблема | Профиль pprof |
|---|---|
| CPU-пик | `profile` — видно, какая функция ест процессор |
| Утечка памяти | `heap` — видно, кто не освобождает память |
| «Зависший» сервер / утечка goroutine | `goroutine` — видно, какие горутины не завершаются (например, тысячи висщих WS-соединений) |
| Долгие блокировки | `mutex` / `block` — видно, где споры за мьютексы |

**Почему отдельный сервер, а не на основном порту:**

1. **Безопасность** — `/debug/pprof/*` раскрывает внутренности рантайма (стек-трейсы, пути к файлам, имена функций). Нельзя отдавать это всем на основном порту.
2. **Не мешает проду** — в production pprof выключен по умолчанию (`PPROF_ENABLED=false`), сайт не несёт лишней нагрузки.
3. **Удобство диагностики** — при проблеме включаешь флаг, снимаешь профиль за 30 секунд, выключаешь.

**Как пользоваться:**

```bash
# 1. Запустить сервер с профилированием
PPROF_ENABLED=true go run ./cmd/server

# 2. Снять профиль CPU (30 секунд) и открыть интерактивный анализ
go tool pprof http://localhost:6060/debug/pprof/profile

# 3. Посмотреть, кто занимает память
go tool pprof http://localhost:6060/debug/pprof/heap

# 4. Проверить goroutine (утечки соединений)
go tool pprof http://localhost:6060/debug/pprof/goroutine

# 5. Веб-интерфейс вместо CLI (откроется на :8081)
go tool pprof -http=:8081 http://localhost:6060/debug/pprof/heap
```

**Что уже находили с его помощью:** в ходе аудита были найдены и исправлены утечки именно этого класса — не прерывавшийся по `ctx.Done()` read loop в `ChatWS` (goroutine висела до 60 сек при silent-disconnect) и SSE `writeLoop`, который не закрывал сессию при ошибке записи (сессия оставалась зарегистрированной и получала heartbeat вечно). Это типичные «pprof-баги» real-time приложений.

---

## 📚 API Документация

Swagger-документация генерируется автоматически. После запуска сервера доступна по адресу:
`http://localhost:8080/swagger/index.html`

Для обновления документации выполните:
```bash
swag init -g cmd/server/main.go
```

Документация в формате JSON: `http://localhost:8080/swagger/doc.json`

---

## 🛠️ Разработка и тестирование

**Линтер**
```bash
golangci-lint run ./...
```

**Unit-тесты (без БД)**
```bash
go test -short ./...
```

**Интеграционные тесты (с PostgreSQL)**
Требуется настроенная тестовая БД (см. `internal/testutil/postgres.go`).
```bash
go test -tags=integration ./...
```

**E2E-тесты (Playwright)** — см. раздел «Команды».

**Бенчмарки**
```bash
go test -bench=. ./internal/pkg/cache/... ./internal/pkg/middleware/...
```

**Профилирование**
```bash
PPROF_ENABLED=true go run ./cmd/server
# затем: go tool pprof http://localhost:6060/debug/pprof/profile
```

**Сборка**
```bash
CGO_ENABLED=0 go build -o gengine.exe -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty) -X main.buildDate=$(date -u '+%Y-%m-%d_%H:%M:%S')" ./cmd/server
```

**Миграция схемы БД**
```bash
gengine -migrate
```

**Docker**
```bash
docker-compose up
```

---

## 🤝 Contributing

Мы приветствуем ваши pull request'ы! Пожалуйста, перед отправкой убедитесь, что существующие тесты проходят, и добавляйте новые для своих изменений.

1. Форкните репозиторий.
2. Создайте ветку для своей фичи (`git checkout -b feature/amazing`).
3. Запустите линтер и тесты: `make lint`, `go test -short ./...` (и `go test -tags=integration ./...` при изменении БД-логики).
4. Создайте pull request.

Подробнее в CONTRIBUTING.md.

---

## 📧 Контакты

Вопросы и предложения: откройте issue в репозитории.
