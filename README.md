# 🕵️ Gengine‑0

**Платформа для создания и проведения городских, полевых и онлайн‑квестов**

Gengine‑0 позволяет авторам проектировать многоуровневые игры с вопросами и кодами, командам — проходить их в реальном времени, а организаторам — следить за прогрессом через мониторинг. Поддерживаются турниры, рейтинги, чат, голосование за лучший ответ (чёрный ящик), галерея фотографий и многое другое.

---

## 📋 Возможности

- 🔐 **Аутентификация и авторизация** — JWT, OAuth2 (Google, GitHub, Яндекс), CSRF‑защита.
- 🎮 **Конструктор игр** — создание черновиков, публикация, настройки видимости и лимитов.
- 🧩 **Уровни и вопросы** — произвольные типы уровней, подсказки, штрафы, файловая загрузка ответов.
- 👥 **Команды и заявки** — управление составом, приглашения, капитанство.
- 📊 **Мониторинг в реальном времени** — WebSocket‑обновления прогресса команд.
- 💬 **Чат** — общий и командный чат с историей сообщений.
- 🗳️ **Голосование (чёрный ящик)** — автор запускает голосование, участники выбирают лучший ответ.
- 🏆 **Турниры** — объединение игр в серии, таблица результатов.
- ⭐ **Рейтинги и достижения** — очки за участие и победы, значки.
- 📸 **Фотогалерея** — загрузка снимков с привязкой к уровням.
- 📄 **Экспорт / импорт** — CSV и PDF отчёты, перенос уровней между играми.
- 🛡️ **Безопасность** — все секреты вынесены в переменные окружения, обязательная сложность паролей.
- 📈 **Наблюдаемость** — структурированное логирование (zerolog), метрики Prometheus, health‑check.

---

## 🧰 Технологический стек

- **Язык:** Go 1.22+
- **HTTP‑фреймворк:** Gin
- **ORM:** GORM (PostgreSQL)
- **Аутентификация:** JWT, OAuth2
- **WebSocket:** Gorilla WebSocket (мониторинг, чат)
- **CSRF:** utrack/gin‑csrf + сессии Gin
- **Логирование:** rs/zerolog
- **Метрики:** Prometheus client_golang
- **Тестирование:** стандартный `testing`, testify, SQLite in‑memory

---

## 🚀 Быстрый старт

### 1. Предварительные требования

- Go 1.22+
- PostgreSQL 14+ (или SQLite для тестов)
- (Опционально) Redis для кэширования сессий

### 2. Клонирование репозитория

git clone https://github.com/ZfauX/Gengine-0.git
cd Gengine-0

### 3. Настройка переменных окружения
Скопируйте .env.example в .env и заполните обязательные переменные.
Категорически нельзя запускать проект со значениями по умолчанию!

cp .env.example .env
nano .env   # или любой другой редактор
Обязательные переменные:

DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME — параметры PostgreSQL.
JWT_SECRET — случайная строка длиной не менее 32 символов (генерируйте, например, openssl rand -base64 32).
SESSION_SECRET — ещё одна надёжная случайная строка (≥ 32 байт).
ADMIN_EMAIL, ADMIN_PASSWORD — учётные данные первого администратора (пароль ≥ 12 символов).
Внешние сервисы (OAuth, Stripe, SMTP, reCAPTCHA) включаются только после установки соответствующих флагов *_ENABLED=true и указания ключей.

Пример минимальной рабочей конфигурации (без внешних сервисов):
PORT=8080
GIN_MODE=debug
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=gengine
DB_SSLMODE=disable
JWT_SECRET=my-super-secret-jwt-key-min-32-chars!
JWT_ACCESS_EXPIRY=15m
JWT_REFRESH_EXPIRY=168h
SESSION_SECRET=my-super-secret-session-key-32bytes!
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=strongpassword123

### 4. Установка зависимостей и запуск
go mod tidy
go run ./cmd/server
Сервер запустится на порту, указанном в PORT (по умолчанию 8080).
Метрики доступны на /metrics, health‑check на /healthz.

### 5. Запуск тестов
Все тесты выполняются из корня проекта:
go test ./internal/domain/... ./cmd/server/...

### Для просмотра покрытия:
go test ./internal/domain/... -coverprofile=coverage.out
go tool cover -html=coverage.out

### 🗂️ Структура проекта
cmd/server/          – точка входа, настройка роутера, фоновые задачи
internal/
  config/            – загрузка и валидация конфигурации (секретов)
  domain/
    admin/           – панель администратора (аудит, бэкапы)
    calendar/        – календарь игр
    export/          – экспорт в CSV/PDF, импорт из CSV
    game/            – игры, прохождения, уровни, попытки, соавторы, заметки, симуляция
    level/           – CRUD уровней, вопросов и ответов
    monitor/         – мониторинг, чат, голосование (чёрный ящик)
    social/          – рейтинг игроков, подписки на авторов
    team/            – команды, приглашения
    tournament/      – турниры и турнирные таблицы
    user/            – пользователи, аутентификация, профиль, достижения
  pkg/
    email/           – отправка email через SMTP
    middleware/       – аутентификация, сжатие, заголовки безопасности, кэш статики, CSRF
    storage/         – локальное файловое хранилище для аватаров и загрузок
    websocket/       – WebSocket‑хаб для real‑time уведомлений
  testutil/          – утилиты для тестов (настройка in‑memory БД)
static/              – статические файлы
templates/           – HTML‑шаблоны (по одному на каждый домен)
uploads/             – загруженные пользователями файлы
backups/             – автоматические бэкапы базы данных
.env.example         – образец файла окружения

### 🔒 Безопасность
Никаких жёстко заданных секретов — все ключи, пароли и токены загружаются из переменных окружения и проходят проверку минимальной длины и сложности.
CSRF‑защита — каждая форма содержит скрытый токен, AJAX‑запросы отправляют заголовок X‑CSRF‑TOKEN.
Валидация ввода — все входящие данные проходят строгую проверку через ShouldBind с тегами binding.
Сессии — подписаны и зашифрованы с использованием SESSION_SECRET.
Пароли — хешируются bcrypt, минимальная длина 8 символов.
Самоподписанный TLS‑сертификат генерируется только для разработки; для production используйте Let's Encrypt или другой доверенный центр.

### 📈 Наблюдаемость
Логирование — все компоненты пишут структурированные логи в JSON с помощью zerolog. Уровни логирования настраиваются через переменную окружения LOG_LEVEL.
Метрики Prometheus — доступны на /metrics. Содержат счётчики и гистограммы HTTP‑запросов.
Health‑check — эндпоинт /healthz проверяет подключение к базе данных и возвращает 200 OK или 503 Service Unavailable.

### 🤝 Contributing
Мы приветствуем ваши pull request'ы! Пожалуйста, перед отправкой убедитесь, что существующие тесты проходят, и добавляйте новые для своих изменений.
Форкните репозиторий.
Создайте ветку для своей фичи (git checkout -b feature/amazing).
Запустите тесты (go test ./...).
Создайте pull request.

### 📧 Контакты
Вопросы и предложения: откройте issue

VERSION=$(git describe --tags --always --dirty)
BUILD_DATE=$(date -u '+%Y-%m-%d_%H:%M:%S')
CGO_ENABLED=0 go build -o gengine -ldflags="-s -w -X main.version=$VERSION -X main.buildDate=$BUILD_DATE" ./cmd/server

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o gengine-linux ./cmd/server
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o gengine.exe ./cmd/server
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o gengine-darwin ./cmd/server
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o gengine-arm64 ./cmd/server

CGO_ENABLED=0 go build -o gengine -ldflags="-s -w" ./cmd/server
go build -o gengine.exe ./cmd/server

swag init -g cmd/server/main.go -o docs
