# Gengine on Podman + systemd (production)

Деплой Gengine в production с помощью **Podman rootless** и **systemd user units**.
Podman Desktop (разработка) использует те же образы — production-окружение отличается
только запуском через systemd.

## Компоненты

| Unit | Контейнер | Назначение |
|---|---|---|
| `gengine-db.service` | `postgres:18-alpine` | PostgreSQL (данные в volume `gengine-pgdata`) |
| `gengine-valkey.service` | `valkey/valkey:7.2-alpine` | Valkey — кэш + общий rate-limit (данные в `gengine-valkeydata`) |
| `gengine-app.service` | `gengine:latest` | Само приложение (uploads/backups в volumes) |

Все контейнеры в одной bridge-сети `gengine-net` — общаются по DNS-именам
(`db`, `valkey`, `gengine-app`).

## Подготовка

```bash
# 1. Сборка образа приложения
podman build -t gengine:latest .
podman tag gengine:latest gengine:$(git describe --tags --always)

# 2. Конфигурация
mkdir -p ~/.config/gengine
cp .env.example ~/.config/gengine/gengine.env
cp deploy/systemd/gengine-db.env ~/.config/gengine/gengine-db.env
# Отредактируйте оба файла:
#   gengine.env  — PORT=8080, DB_HOST=db, DB_PORT=5432, VALKEY_HOST=valkey,
#                  VALKEY_PORT=6379, JWT_SECRET, SESSION_SECRET, ADMIN_*, ...
#   gengine-db.env — POSTGRES_USER/PASSWORD/DB (ПЕРВЫЙ запуск создаёт БД)
#
# ВАЖНО: DB_USER / DB_PASSWORD / DB_NAME в gengine.env должны СОВПАДАТЬ
# с POSTGRES_USER / POSTGRES_PASSWORD / POSTGRES_DB в gengine-db.env —
# это данные для подключения приложения к PostgreSQL.

# 3. Установка unit-файлов (rootless)
install -Dm644 deploy/systemd/gengine-db.service ~/.config/systemd/user/
install -Dm644 deploy/systemd/gengine-valkey.service ~/.config/systemd/user/
install -Dm644 deploy/systemd/gengine-app.service ~/.config/systemd/user/
systemctl --user daemon-reload

# 4. Включить сервисы
systemctl --user enable --now gengine-db gengine-valkey gengine-app

# 5. Разрешить работу без входа в систему (важно для сервера)
loginctl enable-linger $USER
```

## Проверка

```bash
systemctl --user status gengine-app
podman ps                      # все три контейнера — Up
curl http://localhost:8080/healthz
journalctl --user -u gengine-app -f
```

> Примечание: при самом первом запуске `gengine-app` может несколько раз
> перезапуститься (Restart=always), пока `gengine-db` не будет готов принимать
> миграции. Это нормально — unit сам дождётся (RestartSec=5).

## Production-настройки env

- **За reverse-proxy/HTTPS-терминатором** (nginx/caddy/traefik) укажите в
  `gengine.env`: `TRUSTED_PROXIES=<CIDR прокси>` и `FORCE_SECURE_COOKIE=true` —
  иначе CSRF-кука будет без Secure и формы по HTTPS сломаются (см. AGENTS.md).
- **JWT/SESSION**: сгенерируйте длинные секреты:
  `openssl rand -base64 32` (JWT_SECRET, SESSION_SECRET, BACKUP_ENCRYPTION_KEY).
- **STRICT_CONFIG=true** включает строгие проверки конфига (обязательный
  `YKASSA_WEBHOOK_KEY` и т.п.).
- **Бэкапы**: каталог `/app/backups` — volume `gengine-backups`; включите
  `BACKUP_ENCRYPTION_KEY` для шифрования дампов (AES-256-GCM).

## Обновление версии

```bash
podman build -t gengine:latest .
podman tag gengine:latest gengine:$(git describe --tags --always)
systemctl --user restart gengine-app   # миграции применит entrypoint.sh
```

## Rootful (системные unit)

Если нужен rootful-режим (например, сервер без rootless-пользователя):
- переместите unit-файлы в `/etc/systemd/system/`, env-файлы в `/etc/gengine/`;
- в unit-файлах замените `%h/.config/gengine/` на `/etc/gengine/`;
- `systemctl enable --now ...` (без `--user`), `WantedBy=multi-user.target`.

## Логирование

`journalctl --user -u gengine-app` (app пишет и в stdout, и в `logs/app.log` в volume).
