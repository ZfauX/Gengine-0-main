# Dockerfile
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Устанавливаем необходимые зависимости для сборки
RUN apk add --no-cache git ca-certificates tzdata

# Копируем go.mod и go.sum для кэширования зависимостей
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходный код
COPY . .

# Собираем приложение с флагом -migrate для возможности запуска миграций
RUN CGO_ENABLED=0 go build -o gengine -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev') -X main.buildDate=$(date -u '+%Y-%m-%d_%H:%M:%S')" ./cmd/server

# Финальный образ
FROM alpine:3.19

WORKDIR /app

# Устанавливаем PostgreSQL client для pg_dump (нужен для бэкапов)
RUN apk add --no-cache ca-certificates tzdata postgresql16-client

# Копируем бинарник
COPY --from=builder /app/gengine .

# Копируем папку с миграциями
COPY --from=builder /app/migrations ./migrations

# Копируем статику
COPY --from=builder /app/static ./static

# Копируем шаблоны — явно перечисляем все домены
COPY --from=builder /app/internal/domain/admin/templates ./internal/domain/admin/templates
COPY --from=builder /app/internal/domain/calendar/templates ./internal/domain/calendar/templates
COPY --from=builder /app/internal/domain/export/templates ./internal/domain/export/templates
COPY --from=builder /app/internal/domain/game/templates ./internal/domain/game/templates
COPY --from=builder /app/internal/domain/level/templates ./internal/domain/level/templates
COPY --from=builder /app/internal/domain/monitor/templates ./internal/domain/monitor/templates
COPY --from=builder /app/internal/domain/notification/templates ./internal/domain/notification/templates
COPY --from=builder /app/internal/domain/social/templates ./internal/domain/social/templates
COPY --from=builder /app/internal/domain/team/templates ./internal/domain/team/templates
COPY --from=builder /app/internal/domain/tournament/templates ./internal/domain/tournament/templates
COPY --from=builder /app/internal/domain/user/templates ./internal/domain/user/templates

# Создаём директории для логов, загрузок и бэкапов
RUN mkdir -p logs uploads backups

# Копируем entrypoint и делаем его исполняемым
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]