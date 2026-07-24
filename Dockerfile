# Dockerfile
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Устанавливаем необходимые зависимости для сборки
RUN apk add --no-cache git ca-certificates tzdata

# Копируем go.mod и go.sum для кэширования зависимостей
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходный код
COPY . .

# Собираем приложение
RUN CGO_ENABLED=0 go build -o gengine -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev') -X main.buildDate=$(date -u '+%Y-%m-%d_%H:%M:%S')" ./cmd/server

# Финальный образ
FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata postgresql17-client

# Бинарник
COPY --from=builder /app/gengine .

# Миграции
COPY --from=builder /app/migrations ./migrations

# Статика
COPY --from=builder /app/static ./static

# HTML-шаблоны (нужны для рендеринга — ParseGlob использует "internal/domain/*/templates/")
COPY --from=builder /app/internal ./internal

# Директории для runtime-данных
RUN mkdir -p logs uploads backups

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
