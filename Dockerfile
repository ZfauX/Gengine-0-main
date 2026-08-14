# Dockerfile
# 1.25.13 — патч с фиксами stdlib уязвимостей (см. .github/workflows/go.yml).
FROM golang:1.25.13-alpine AS builder

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
# alpine:3.23 — первый стабильный выпуск с пакетом postgresql18-client,
# который нужен для pg_dump (бэкапы) против PostgreSQL 18.
FROM alpine:3.23

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata postgresql18-client

# H3 (PASS-15): GOMEMLIMIT — в контейнере Go иначе считает память ВСЕЙ машины
# (cgroup-лимит игнорируется), что при пиковом heap даёт OOM-kill или GC-спайки.
# 384MiB ≈ 75% от типового лимита pod 512Mi. В k8s/podman задавайте близко к
# ресурсу контейнера (см. deploy/pod/README.md). Опционально GOGC=off при
# GOMEMLIMIT для минимального GC-оверхеда.
ENV GOMEMLIMIT=384MiB

# Бинарник
COPY --from=builder /app/gengine .

# Миграции
COPY --from=builder /app/migrations ./migrations

# Статика
COPY --from=builder /app/static ./static

# HTML-шаблоны (нужны для рендеринга — ParseGlob использует "internal/domain/*/templates/")
RUN mkdir -p /app/internal
COPY --from=builder /app/internal /tmp/internal
RUN for dir in /tmp/internal/domain/*/templates; do \
      if [ -d "$dir" ]; then \
        target="/app/internal/domain/$(basename $(dirname $dir))/templates"; \
        mkdir -p "$target"; \
        cp -r "$dir"/* "$target"/; \
      fi; \
    done && rm -rf /tmp/internal

# Директории для runtime-данных
RUN mkdir -p logs uploads backups

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
