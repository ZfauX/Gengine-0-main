# Gengine-0: горизонтальное масштабирование (multi-instance)

> Документация по запуску нескольких инстансов приложения за балансировщиком.
> Покрывает: архитектуру, конфигурацию Valkey, WebSocket/SSE (pub/sub), nginx,
> systemd-шаблон N реплик, ограничения и порядок запуска.

---

## 1. Архитектура

```
                    ┌──────────────┐
   Пользователи ──► │   nginx      │  TLS-терминация, sticky sessions
                    └──────┬───────┘
                           │  http/ws (round-robin + ip_hash)
          ┌────────────────┼────────────────┐
          ▼                ▼                ▼
   ┌───────────┐    ┌───────────┐    ┌───────────┐
   │ gengine@1 │    │ gengine@2 │    │ gengine@3 │   ← идентичные контейнеры
   │  :8080    │    │  :8080    │    │  :8080    │
   └──┬─────┬──┘    └──┬─────┬──┘    └──┬─────┬──┘
      │     │          │     │          │     │
      │     └──────────┼─────┼──────────┼─────┘
      │                │     │          │
   ┌──▼──────┐   ┌─────▼─────▼──────────▼─────┐
   │PostgreSQL│  │   Valkey (один, общий)      │
   └─────────┘   │   - сессии  gengine:session:*│
                 │   - JTI     jti_blacklist:* │
                 │   - rate    лимитеры        │
                 │   - кэш     прикладной      │
                 │   - pub/sub gengine:ws,      │
                 │             gengine:sse     │
                 └─────────────────────────────┘
```

**Ключевой принцип: приложение stateless.** Всё общее состояние вынесено в
PostgreSQL и Valkey. Любой инстанс обслуживает любой запрос; добавление
реплики = увеличение числа контейнеров.

---

## 2. Что уже реализовано в коде (PASS-12)

| Подсистема | Как масштабируется | Файл |
|---|---|---|
| Server-side сессии | Valkey `gengine:session:*` (или in-memory fallback) | `internal/pkg/sessionstore` |
| JTI-blacklist (отзыв JWT) | Valkey `jti_blacklist:*` | `internal/domain/user/service.go` |
| Rate-limiters (login, register, per-user) | Valkey (критичные fail-closed) | `internal/pkg/middleware/rate_limiter.go` |
| Прикладной LRU-кэш | Valkey (или in-memory fallback) | `internal/pkg/cache` |
| **WebSocket (RoomHub)** | **Valkey pub/sub `gengine:ws`** | `internal/pkg/realtimebus`, `internal/pkg/websocket/room_hub_pubsub.go` |
| **SSE (SSEManager)** | **Valkey pub/sub `gengine:sse`** | `internal/pkg/realtimebus`, `internal/domain/game/sse_pubsub.go` |
| Миграции | **PostgreSQL advisory lock** (несколько инстансов не конфликтуют) | `internal/db/migrate.go` |

### WebSocket / SSE cross-instance (anti-эхо)

- Инстанс A вызывает `BroadcastToRoom(...)` / `SSEManager.Broadcast(...)`.
- Локальная рассылка идёт немедленно (как раньше), параллельно сообщение
  публикуется в Valkey pub/sub канал с полем `origin = instanceID`.
- Все инстансы подписаны на каналы `gengine:ws` / `gengine:sse`.
- Инстанс B получает сообщение, видит `origin != instanceID` и рассылает
  локальным клиентам. Инстанс A получает своё сообщение и **пропускает**
  (origin == instanceID) — каждый клиент получает сообщение ровно один раз.
- Без Valkey (`VALKEY_HOST` пуст) шина не активна: WebSocket/SSE работают
  **только локально** (одноинстансное поведение, как до PASS-12).

---

## 3. Конфигурация Valkey

В `.env` каждого инстанса:

```dotenv
VALKEY_HOST=valkey
VALKEY_PORT=6379
VALKEY_PASSWORD=...          # опционально, если включён requirepass
VALKEY_POOL_SIZE=20
VALKEY_MIN_IDLE_CONNS=5
VALKEY_MAX_RETRIES=3
```

**Поведение при недоступности Valkey:**
- Критичные rate-limiters (login/register/2FA/коды/сброс пароля) — **fail-closed**:
  запросы отклоняются (защита от брутфорса не отключается).
- Глобальный/SSE/API rate-limiters, сессии, кэш — **fail-open**: сайт работает,
  но cross-instance координация (сессии, blacklist, real-time) временно деградирует.
- WebSocket/SSE при недоступности Valkey: publish не блокирует локальную рассылку
  (fail-open), подписка восстанавливается автоматически с backoff.

---

## 4. nginx (балансировщик + TLS)

Конфиг `deploy/multi-instance/nginx.conf.example`:

```nginx
upstream gengine_backend {
    # ip_hash — привязка клиента к инстансу (sticky) для WebSocket/SSE.
    # Это не обязательно для корректности (pub/sub доставит сообщение через
    # любой инстанс), но снижает число пересоединений при броадкасте.
    ip_hash;
    server 127.0.0.1:8080;
    server 127.0.0.1:8081;
    server 127.0.0.1:8082;
}

server {
    listen 443 ssl http2;
    server_name gengine.example.com;

    ssl_certificate     /etc/nginx/ssl/fullchain.pem;
    ssl_certificate_key /etc/nginx/ssl/privkey.pem;

    # WebSocket upgrade
    location /ws/ {
        proxy_pass http://gengine_backend;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;
    }

    # SSE — отключаем буферизацию (иначе события накапливаются)
    location /game/ {
        proxy_pass http://gengine_backend;
        proxy_http_version 1.1;
        proxy_buffering off;
        proxy_cache off;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;
    }

    location / {
        proxy_pass http://gengine_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

> **Внимание (security, S-M4):** если TLS терминируется на nginx, каждый инстанс
> должен видеть, что соединение безопасно, иначе CSRF-cookie/JWT станут Secure
> и формы сломаются. Убедитесь, что `TRUSTED_PROXIES` в `.env` содержит адрес
> nginx (или установлен `FORCE_SECURE_COOKIE=true`). См. `.env.example`.

---

## 5. systemd: N реплик (шаблонный юнит)

`deploy/multi-instance/gengine-app@.service` — шаблон systemd, позволяющий
поднять любое число инстансов через `systemctl --user enable gengine-app@{1,2,3}`.

Каждый инстанс отличается только:
- номером (`%i` → `1`, `2`, `3`);
- внешним портом (`--publish 808{инстанс}:8080`);
- уникальным `SESSION_SECRET` (опционально; можно общий, но безопаснее per-instance)
  — ключи подписи/шифрования сессий производятся от него, общий секрет тоже
  корректен, т.к. сессии живут в общем Valkey.

---

## 6. Общий storage (uploads/backups)

**Ограничение:** `LocalStorage` пишет файлы (аватарки, фото уровней, CSV/PDF)
на диск контейнера. При N инстансах нужен общий сторадж, иначе файл, загруженный
через инстанс 1, недоступен через инстанс 2.

Варианты:
1. **Общий том** (NFS/EFS/CIFS) на все инстансы: `--volume /mnt/shared:/app/uploads:Z`.
2. **Podman named volume** на одном хосте (несколько контейнеров — один volume):
   `--volume gengine-uploads:/app/uploads:Z` (работает для нескольких контейнеров
   одного хоста, т.к. volume общий).
3. (Будущее) S3-реализация `storage.FileStorage` — интерфейс это позволяет.

> Для `gengine-backups` то же самое: бэкапы должны быть доступны из любого
> инстанса (или выполняться только на одном).

---

## 7. Email-очередь (ограничение)

`email.InitQueue` — persistent-очередь в БД + in-memory воркеры. При N инстансах
воркеры дублируются (несколько инстансов читают одну очередь). Рекомендация:
- поднять воркеры только на одном инстансе (`EMAIL_QUEUE_WORKERS=0` на остальных),
- либо принимать на себя advisory lock на обработку (будущая работа).

---

## 8. Порядок запуска (безопасный старт N инстансов)

1. **Миграции — одним процессом** (advisory lock уже защищает, но порядок чище):
   ```bash
   ./gengine -migrate          # один раз, перед поднятием реплик
   ```
   Либо просто запускайте реплики сразу — `MigrateFromDir` берёт
   `pg_advisory_lock`, остальные инстансы ждут и продолжают на готовой схеме.
2. Убедиться, что Valkey и PostgreSQL доступны (health-check `gengine-valkey.service`,
   `gengine-db.service`).
3. Поднять реплики:
   ```bash
   systemctl --user enable --now gengine-app@{1,2,3}
   ```
4. Проверить `/healthz` на каждом инстансе и через nginx.

---

## 9. Проверка работоспособности cross-instance

- **WebSocket**: два браузера в комнате чата, серверы на разных инстансах —
  сообщение доходит обоим (раньше только локальному).
- **SSE**: открыть SSE-страницу игры через инстанс 1, сделать действие,
  инициирующее broadcast на инстансе 2 — событие приходит.
- **Сессии**: залогиниться через инстанс 1, обновить страницу (попал на инстанс 2) —
  сессия жива (данные в общем Valkey).
- **Rate-limit**: 4-й неудачный login за 10 минут отклоняется независимо от того,
  на какой инстанс попал запрос (бюджет общий в Valkey).

---

## 10. Итоговая сводка

| Вопрос | Ответ |
|---|---|
| Сколько инстансов приложения? | **2+** (мин. для HA); масштабируется по нагрузке |
| Сколько БД? | **1 общая** PostgreSQL |
| Сколько Valkey? | **1 логический** (для HA — Sentinel/кластер) |
| Как соединяются? | App ↔ PostgreSQL (gorm-пул), App ↔ Valkey (go-redis), Client → nginx → App |
| WebSocket/SSE | **pub/sub через Valkey** (PASS-12) — cross-instance работает |
| Миграции | **advisory lock** — безопасный одновременный старт |
| Ограничения | Общий storage для uploads/backups; email-воркеры на одном инстансе |
