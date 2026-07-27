# 🕵️ Gengine‑0

**Платформа для создания и проведения городских, полевых и онлайн‑квестов**

Gengine‑0 позволяет авторам проектировать многоуровневые игры с вопросами и кодами, командам — проходить их в реальном времени, а организаторам — следить за прогрессом через мониторинг. Поддерживаются турниры, рейтинги, чат, голосование, галерея фотографий и многое другое.

---

## 📋 Возможности

- 🔐 **Аутентификация** — JWT + refresh-токены, OAuth2 (Google, GitHub, Яндекс), WebAuthn/Passkeys, 2FA
- 🎮 **Конструктор игр** — черновики, публикация, уровни, вопросы, ответы, файловая загрузка
- 👥 **Команды и турниры** — управление составом, приглашения, турнирные таблицы
- 📊 **Мониторинг в реальном времени** — WebSocket + SSE, прогресс команд, чат
- 🗳️ **Голосование (чёрный ящик)** — автор запускает голосование, участники выбирают лучший ответ
- 📸 **Фотогалерея** — загрузка снимков с привязкой к уровням
- 📄 **Экспорт** — CSV, PDF, Excel, iCal
- 🌐 **i18n** — Русский и английский языки (256+ строк)
- 🛡️ **Безопасность** — CSP с nonce, CSRF, rate-limit, httpOnly cookies, структурированные ошибки
- 🎨 **Интерфейс** — Tailwind CSS v4, CSS-переменные, тёмная тема, PWA, Service Worker
- 📈 **Наблюдаемость** — zerolog, Prometheus, health-check, Sentry

---

## 🧰 Технологический стек

| Компонент | Технология |
|-----------|-----------|
| **Язык** | Go 1.25 |
| **HTTP-фреймворк** | Gin |
| **ORM** | GORM (PostgreSQL) |
| **Миграции** | golang-migrate (18 миграций) |
| **Аутентификация** | JWT, OAuth2, WebAuthn/Passkeys, 2FA (TOTP) |
| **WebSocket** | Gorilla WebSocket |
| **SSE** | Server-Sent Events |
| **Кэш** | In-memory LRU + Valkey (Redis-compatible) |
| **CSRF** | gorilla/csrf с глобальным fetch-перехватчиком |
| **Стилизация** | Tailwind CSS v4 (CSS-first), `@theme`, тёмная тема |
| **Логирование** | zerolog + lumberjack (ротация) |
| **Метрики** | Prometheus client_golang |
| **Сборка CSS** | Tailwind CLI v4 (~170ms) |
| **DI** | google/wire |
| **Тестирование** | testing + testify + go-sqlmock + goleak |

---

## 🚀 Быстрый старт

### Предварительные требования

- Go 1.25
- PostgreSQL 18+
- Node.js 18+ и npm (только для сборки CSS)

### Установка и запуск

```bash
# 1. Клонирование
git clone https://github.com/ZfauX/Gengine-0.git
cd Gengine-0

# 2. Go-зависимости
go mod tidy

# 3. Node.js-зависимости (для сборки CSS)
npm install

# 4. Настройка .env
cp .env.example .env
# Отредактируйте .env: DB_HOST, DB_PASSWORD, JWT_SECRET, SESSION_SECRET, ADMIN_EMAIL/PASSWORD

# 5. Сборка CSS (Tailwind v4)
npm run build:css    # или make css

# 6. Запуск миграций БД
go run ./cmd/server -migrate

# 7. Запуск сервера (dev)
go run ./cmd/server

# 8. Полная сборка
make build            # CSS + Go binary
```

Сервер будет доступен на `http://localhost:8080`.
- Swagger: `/swagger/index.html`
- Health: `/healthz`
- Метрики: `/metrics`

---

## 📋 Команды

| Команда | Описание |
|---------|----------|
| `make build` | Сборка CSS + Go binary |
| `make dev` | `go run ./cmd/server` |
| `make css` | Сборка Tailwind CSS |
| `make test` | Все тесты с покрытием |
| `make test-short` | Unit-тесты (без БД) |
| `make test-integration` | Интеграционные тесты (`-tags=integration`) |
| `make lint` | `golangci-lint run ./...` |
| `make swagger` | Генерация Swagger-документации |
| `go generate ./internal/app/` | Регенерация wire DI (после изменения конструкторов) |

---

## 🗂️ Структура проекта

```
cmd/server/main.go          — точка входа, graceful shutdown
internal/
  app/                      — DI (wire), роутер
  config/                   — env-конфигурация с валидацией
  db/                       — подключение, миграции, admin seed
  domain/                   — бизнес-логика по доменам:
    {user,game,level,team,tournament,monitor,calendar,social,notification,admin,export}/
      model.go              — GORM-модели
      service.go            — бизнес-логика (без HTTP)
      handler.go            — HTTP-хендлеры (gin)
      routes.go             — маршруты + DI
      templates/            — HTML-шаблоны
  pkg/                      — переиспользуемые модули:
    cache/                  — in-memory LRU + Valkey
    websocket/              — RoomHub (WS-хаб + клиент)
    middleware/             — auth, CSRF, gzip, CSP, rate-limit, bodylimit
    email/                  — SMTP с persistent-очередью
    i18n/                   — интернационализация (256 строк, ru+en)
    render/                 — HTML-рендеринг
    storage/                — файловое хранилище
migrations/                 — SQL-миграции (18 файлов)
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
- **JWT** — httpOnly cookie, refresh-токены в БД с отзывом
- **CSRF** — gorilla/csrf, `X-CSRF-TOKEN` для AJAX, `_csrf` для форм
- **CSP** — `script-src 'nonce-...'`, `form-action 'self'`, строгие директивы
- **CORS** — настраивается через `CORS_ORIGINS`
- **Rate-limit** — глобальный, логин, регистрация, code submission, API
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

📈 Наблюдаемость
Логирование — все компоненты пишут структурированные логи в JSON с помощью zerolog. Уровни логирования настраиваются через переменную окружения LOG_LEVEL.
Метрики Prometheus — доступны на /metrics. Содержат счётчики и гистограммы HTTP‑запросов, бизнес-метрики (игры, команды, пользователи, WebSocket-соединения и т.д.).
Health‑check — эндпоинт /healthz проверяет подключение к базе данных и статус WebSocket-хаба.

📚 API Документация
Swagger-документация генерируется автоматически. После запуска сервера доступна по адресу:
http://localhost:8080/swagger/index.html
Для обновления документации выполните:
swag init -g cmd/server/main.go
Документация в формате JSON доступна по адресу:
http://localhost:8080/swagger/doc.json

🛠️ Разработка и тестирование
Запуск линтера
golangci-lint run

Запуск интеграционных тестов (с PostgreSQL)
Требуется настроенная тестовая БД (см. internal/testutil/postgres.go).
go test -tags=integration ./internal/domain/...

Сборка
CGO_ENABLED=0 go build -o gengine.exe -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty) -X main.buildDate=$(date -u '+%Y-%m-%d_%H:%M:%S')" ./cmd/server

Миграция схем бд
gengine -migrate

Docker
docker-compose up

🤝 Contributing
Мы приветствуем ваши pull request'ы! Пожалуйста, перед отправкой убедитесь, что существующие тесты проходят, и добавляйте новые для своих изменений.

Форкните репозиторий.
Создайте ветку для своей фичи (git checkout -b feature/amazing).
Запустите тесты (go test ./...).
Создайте pull request.
Подробнее в CONTRIBUTING.md.

📧 Контакты
Вопросы и предложения: откройте issue в репозитории.