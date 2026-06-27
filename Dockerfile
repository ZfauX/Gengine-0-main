# Dockerfile (оптимизированный)
FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Сборка статического бинарника
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server

# Финальный образ
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata postgresql18-client

WORKDIR /root/

# Копируем бинарник
COPY --from=builder /app/server .

# Копируем статику и загрузки (если нужны во время выполнения)
COPY --from=builder /app/static ./static
COPY --from=builder /app/uploads ./uploads

# Копируем только шаблоны (без Go-исходников) — это необходимо для LoadHTMLGlob
# TODO: в будущем перейти на embed, чтобы избавиться от копирования шаблонов
COPY --from=builder /app/internal/domain/admin/templates ./internal/domain/admin/templates
COPY --from=builder /app/internal/domain/calendar/templates ./internal/domain/calendar/templates
COPY --from=builder /app/internal/domain/export/templates ./internal/domain/export/templates
COPY --from=builder /app/internal/domain/game/templates ./internal/domain/game/templates
COPY --from=builder /app/internal/domain/level/templates ./internal/domain/level/templates
COPY --from=builder /app/internal/domain/monitor/templates ./internal/domain/monitor/templates
COPY --from=builder /app/internal/domain/social/templates ./internal/domain/social/templates
COPY --from=builder /app/internal/domain/team/templates ./internal/domain/team/templates
COPY --from=builder /app/internal/domain/tournament/templates ./internal/domain/tournament/templates
COPY --from=builder /app/internal/domain/user/templates ./internal/domain/user/templates

# Копируем миграции (если они используются во время выполнения)
COPY --from=builder /app/migrations ./migrations

EXPOSE 8080

CMD ["./server"]