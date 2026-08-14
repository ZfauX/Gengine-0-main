# Gengine-0: Pod-деплой (app + PostgreSQL + Valkey)

Запуск всего стека в одном pod через `podman play kube` (совместимо с k8s).

## Запуск

```bash
# 1. Сборка образа приложения
podman build -t gengine:latest -f Dockerfile .

# 2. Запуск pod (app + db + valkey в общем network namespace)
podman play kube deploy/pod/gengine-pod.yaml

# 3. Проверка
podman pod ps                 # gengine-pod — Running
podman ps | grep gengine-pod  # db, valkey, app
curl http://<host>:8080/healthz
```

Контейнеры внутри pod общаются через `127.0.0.1` (общий network namespace).
Порт app проброшен наружу: `hostPort: 8080`.

## Что внутри

| Контейнер | Образ | Назначение |
|---|---|---|
| `db` | `postgres:18-alpine` | PostgreSQL (том `gengine-pgdata`) |
| `valkey` | `valkey/valkey:7.2-alpine` | Valkey (том `gengine-valkeydata`) |
| `app` | `localhost/gengine:latest` | Приложение (entrypoint применяет миграции) |

## ВАЖНО: секреты и конфигурация

- **Секреты** (`DB_PASSWORD`, `JWT_SECRET`, `SESSION_SECRET`, `ADMIN_PASSWORD`) в
  манифесте — ТЕСТОВЫЕ. В production используйте k8s Secrets / `podman secret`
  или `envFrom`.
- **`TRUSTED_PROXIES` не задан** → Secure-флаг CSRF/session cookie = false →
  формы работают по HTTP. При reverse-proxy с TLS задайте `TRUSTED_PROXIES`
  (иначе Secure-флаг станет true и HTML-формы сломаются — см. AGENTS.md).
- **Rate-limiters** подняты (100000) для тестов; в production — реальные
  значения (см. `.env.example`).
- **Миграции**: entrypoint (`entrypoint.sh`) применяет их при старте с
  advisory lock — безопасно даже при нескольких репликах.

## Известные ограничения (исправлены в PASS-14)

- **Squashed-миграции несамодостаточны**: файлы `migrations_squashed/`
  ссылаются на таблицы из других файлов в неверном порядке. Для СВЕЖИХ БД
  runner теперь применяет поштучные `migrations/` (полностью рабочие).
  Squashed-набор используется только для уже существующих squashed-БД
  (version <= 9).
- **`CREATE INDEX CONCURRENTLY` в миграциях 44-51** заменён на обычные
  `CREATE INDEX IF NOT EXISTS` — golang-migrate шлёт файл одним Exec,
  а PostgreSQL отклоняет CONCURRENTLY в multi-statement batch.

## Остановка

```bash
podman pod stop gengine-pod
podman pod rm gengine-pod      # -f чтобы удалить не глядя
podman volume rm gengine-pgdata gengine-valkeydata
```

## Альтернативы

- **docker-compose**: `podman compose up -d --build` (см. `docker-compose.yml`).
- **systemd units**: `deploy/systemd/` (rootless Podman).
- **N инстансов**: `deploy/multi-instance/` (pub/sub через Valkey, PASS-12).
