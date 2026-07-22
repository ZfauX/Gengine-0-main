# Makefile для Gengine-0

# Переменные
BINARY_NAME=gengine
MAIN_PATH=./cmd/server
SWAGGER_CMD=swag
SWAGGER_INIT=swag init -g $(MAIN_PATH)/main.go -o ./docs
GO=go
GOLANGCI=golangci-lint

.PHONY: all build run test lint swagger clean help

# По умолчанию: сборка
all: build

# Сборка приложения
build:
	$(GO) build -ldflags "-X main.version=$(shell git describe --tags --always --dirty) -X main.buildDate=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" -o $(BINARY_NAME) $(MAIN_PATH)

# Запуск миграций
migrate:
	$(GO) run ./cmd/migrate

# Запуск приложения (сборка + запуск)
run: build
	./$(BINARY_NAME)

# Запуск без сборки (для разработки)
dev:
	$(GO) run $(MAIN_PATH)/main.go

# Запуск тестов
test:
	$(GO) test -v -race -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Быстрые тесты без базы данных (только unit-тесты)
test-short:
	$(GO) test -v -short -race -cover ./...

# Тесты с race detector в отдельном job
test-race:
	$(GO) test -v -race -count=1 ./...

# Тесты с PostgreSQL (требуют running DB)
test-integration:
	$(GO) test -v -race -cover -tags=integration ./...

# Запуск линтера
lint:
	$(GOLANGCI) run ./...

# Генерация Swagger-документации
swagger:
	$(SWAGGER_INIT)

# Очистка артефактов сборки
clean:
	rm -f $(BINARY_NAME)
	rm -f coverage.out coverage.html
	rm -rf ./docs

# Проверка наличия зависимостей
deps:
	$(GO) mod download
	$(GO) mod tidy

# Установка инструментов (swag, golangci-lint) при необходимости
install-tools:
	$(GO) install github.com/swaggo/swag/cmd/swag@latest
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Команда для CI (полная проверка)
ci: deps lint test swagger build

# Помощь
help:
	@echo "Доступные команды:"
	@echo "  make build          - Сборка приложения"
	@echo "  make run            - Сборка и запуск"
	@echo "  make dev            - Запуск без сборки (go run)"
	@echo "  make test           - Запуск тестов с покрытием"
	@echo "  make test-short     - Быстрые тесты без DB (unit-tests)"
	@echo "  make test-race      - Тесты с race detector"
	@echo "  make test-integration - Тесты с PostgreSQL"
	@echo "  make lint           - Запуск golangci-lint"
	@echo "  make swagger        - Генерация Swagger-документации"
	@echo "  make clean          - Очистка артефактов"
	@echo "  make deps           - Загрузка зависимостей"
	@echo "  make install-tools  - Установка swag и golangci-lint"
	@echo "  make ci             - Полная проверка (deps, lint, test, swagger, build)"