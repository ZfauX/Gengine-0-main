# DEEP_REVIEW — Gengine-0 (PASS 15)

> Глубокое ревью после PASS-14 (docker/podman). Целевое окружение — **pod через podman** (app+PostgreSQL+Valkey в одном pod).
> Метод: pprof-профилирование в pod (PPROF_ENABLED, 127.0.0.1:6060) + 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая проверка каждого HIGH/MEDIUM finding по коду.
> Архив предыдущего: `DEEP_REVIEW_2026-08-14_pass14.md`.

---

## 🔬 pprof-результаты (PASS 15, в pod)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 20 в покое | ✅ Без утечек (стабильно с PASS-5..14). |
| **heap inuse** | **6.3 MB** (было 18.5 MB в PASS-13) | ✅ **P-1 подтверждён на практике**: swagger за build-tag убран из сборки → webdav memFile (9.5MB) отсутствует. Остальное — рантайм-инициализация. |
| **cpu (лёгкая нагрузка)** | 1% (300ms samples) | ⚠️ text/template walk 23%, syscall 23%, zerolog 10%, htmlReplacer 10%. Рендер снизился (P-2 precompute T() работает), но content-рендер анонимных страниц всё ещё выполняется на кэш-хите (perf #2). |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback, включён через env в pod-манифесте. |

**Вывод**: утечек нет, P-1 дал −60% heap. Главные остаточные статьи: HTML-кэш анонимов не достигает цели для content-части (perf #2), отсутствие GOMEMLIMIT в контейнере (perf #1), консольный формат логов (perf #5).

---

## 🔴 HIGH

### H1. HTML-кэш анонимов: на cache-hit всё равно исполняется content-шаблон 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/render/helper.go:254,281`.
- **Проблема**: `tryServeAnonCache` вызывается ПОСЛЕ `tmpl.ExecuteTemplate(buf, contentTemplate, data)` (строка 254). На кэш-хите самый дорогой шаг — исполнение content-шаблона (home.html/games.html, «37% template walk» из pprof) и сборка `ContentHTML` — выполняется впустую. Кэш спасает только рендер layout.
- **Фикс**: перенести `tryServeAnonCache` выше `ExecuteTemplate` content (данные для ключа/lang/csrf/nonce готовы до рендера). `isCacheableAnon` не зависит от content — перестановка безопасна.

### H2. Подстановка nonce/CSRF делает 2–3 полные копии HTML на каждый hit/miss 🔍✅
- **Файл**: `internal/pkg/render/htmlcache.go:119-126,142-149`.
- **Проблема**: на кэш-хите: `string(body)` → 2× `strings.ReplaceAll` → `[]byte(html)`; на miss-записи то же. Для страниц 50–100KB это 2–3 полные копии + аллокации на каждый запрос к `/` и `/games`.
- **Фикс**: `bytes.ReplaceAll(body, noncePlaceholder, nonceBytes)` без конверсий string, писать в буфер из `sync.Pool`.

### H3. Отсутствует GOMEMLIMIT/GC-настройка в контейнере 🔍✅
- **Файлы**: `Dockerfile:16-17`, `entrypoint.sh`, `cmd/server/main.go`.
- **Проблема**: `debug.SetMemoryLimit`/`GOMEMLIMIT` не настроены. В pod с cgroup-лимитом Go считает память всей машины → при пиковом live-heap либо OOM-kill, либо GC-спайки.
- **Фикс**: `ENV GOMEMLIMIT=384MiB` (≈75-80% лимита pod) или `debug.SetMemoryLimit` в main.

### H4. Email: двойная отправка одного письма (гонка retry-очереди и batch-воркера) 🔍✅
- **Файл**: `internal/pkg/email/email.go:393` (`processRetryJob`), `:243-255` (`processPendingEmails`), `:359-376` (`scheduleRetry`).
- **Проблема**: `scheduleRetry` кладёт задачу в in-memory retryQueue, но НЕ обновляет `scheduled_at` в БД → batch-воркер подхватывает те же письма (status=retry, scheduled_at=NULL). `processRetryJob` отправляет ДО атомарного claim (строка 393 до 407). Окно: оба пути шлют одно письмо → дубликат получателю.
- **Фикс**: claim в `processRetryJob` перед отправкой (`UPDATE … SET status='sending' WHERE id=? AND status IN (pending,retry)`) + выставлять `scheduled_at` при `scheduleRetry`.

### H5. Trusted-device cookie 2FA не отзывается и переживает смену пароля/отключение 2FA 🔍✅
- **Файл**: `internal/domain/user/two_factor_middleware.go:40-99`; `auth_handler.go:396`.
- **Проблема**: stateless HMAC cookie `2fa_trusted` (`userID:expiry`, 30д). Очистка только при logout. `ChangePassword` (`profile_handler.go:417-480`) и `Disable` 2FA (`two_factor_handler.go:579-690`) cookie НЕ снимают. Украденный cookie даёт обход 2FA до 30 дней даже после смены пароля.
- **Фикс**: `clearTrustedDeviceCookie(c)` при смене пароля и отключении 2FA (+ желательно версия/серийный номер в HMAC).

### H6. Trusted-device cookie с жёстко зашитым `secure=false` 🔍✅
- **Файл**: `two_factor_middleware.go:48`.
- **Проблема**: cookie, дающий обход 2FA, передаётся по HTTP (MITM), в отличие от JWT/refresh (setSecureCookie с учётом HTTPS/ForceSecureCookie). При `FORCE_SECURE_COOKIE` остальные куки Secure, а эта — нет.
- **Фикс**: учитывать `cfg.Server.ForceSecureCookie`/TLS при установке (передавать Secure-флаг).

---

## 🟠 MEDIUM

### M1. HTML-кэш анонимов: TZOffset не входит в ключ кэша 🔍✅
- **Файл**: `internal/pkg/render/htmlcache.go:69-76`.
- **Проблема**: `data["TZOffset"]` из cookie первого анонима рендерится в даты; второй аноним с другим tz получает чужие даты до TTL (30с).
- **Фикс**: добавить TZOffset в `anonCacheKey`.

### M2. `/profile/update` без rate-limit и без счётчика неверных паролей 🔍✅
- **Файл**: `internal/domain/user/routes.go:119`, `profile_handler.go:357-368`.
- **Проблема**: bcrypt-проверка при смене email без rate-limit и lockout. С украденной сессией — перебор пароля → смена email → сброс пароля → захват аккаунта.
- **Фикс**: `middleware.UploadRateLimit` (или отдельный лимитер) на `/profile/update` + счётчик неудач.

### M3. N+1 в DashboardTeams: 3 коррелированных подзапроса на каждую команду 🔍✅
- **Файл**: `internal/domain/user/repository.go:298-308`.
- **Проблема**: `completed_levels`/`total_levels`/`current_position` — 3 подзапроса на строку (3×N). Нет индекса на `level_progresses.finished_at`.
- **Фикс**: `LEFT JOIN LATERAL`/`GROUP BY` + `COUNT(*) OVER (PARTITION BY ...)`, индекс по `(game_passing_id, finished_at)`.

### M4. Консольный формат логов (zerolog ConsoleWriter) = 10% CPU 🔍✅
- **Файл**: `cmd/server/main.go:154-161`.
- **Проблема**: `LOG_FORMAT=console` — посимвольное форматирование + ANSI в контейнере, где вывод всё равно в podman-логи.
- **Фикс**: в pod `LOG_FORMAT=json`.

### M5. Секреты и pprof в `deploy/pod/gengine-pod.yaml` (copy-paste в prod) 🔍✅
- **Файл**: `deploy/pod/gengine-pod.yaml:25-28,79-86,103-104`.
- **Проблема**: DB_PASSWORD/JWT/SESSION/ADMIN_PASSWORD — тестовые, `RATE_LIMIT_*=100000` (лимиты выключены), `PPROF_ENABLED=true`.
- **Фикс**: комментарии-предупреждения + документация; в production — secrets/`podman secret`.

### M6. Миграции: обычный `CREATE INDEX` вместо CONCURRENTLY блокирует запись на больших таблицах 🔍✅
- **Файл**: `migrations/000044-000051`, `internal/db/migrate.go`.
- **Проблема**: CONCURRENTLY заменён на обычный `CREATE INDEX` (чтобы golang-migrate не падал) — при деплое на больших таблицах блокирует запись.
- **Фикс**: для крупных БД — фоновый индекс вручную; для новых — IF NOT EXISTS. Задокументировать trade-off.

### M7. `sessions.Sessions` middleware: потенциальный Redis round-trip на каждый авторизованный запрос 🔍✅(требует точечной проверки)
- **Файл**: `internal/app/router.go:109`, `sessionstore`.
- **Проблема**: gorilla-sessions middleware открывает store на каждый запрос; server-side Valkey → возможный GET round-trip даже без использования сессии.
- **Фикс**: ленивая загрузка сессии (читать только в `sessions.Default(c)`) или короткий TTL-кэш.

### M8. Осиротевшая очередь комнаты после гонки broadcast/удаление (RoomHub) 🔍✅
- **Файл**: `internal/pkg/websocket/room_hub.go:264-288`.
- **Проблема**: между RLock-проверкой и созданием очереди комната может быть удалена; очередь остаётся в `roomQueues` навсегда, воркер выходит. При пересоздании комнаты broadcast находит `queue != nil`, не создаёт воркер → чат молча дропается.
- **Фикс**: чистить `roomQueues` при обнаружении мёртвого воркера / `queue` при `room==nil` в broadcast.

---

## 🟡 LOW

1. **`globalStore` в sessionstore без синхронизации** (`SetDefault` vs `RenewGinSession`) — де-факто инициализация до трафика; формально data race.
2. **HTML-кэш: host/canonical URL запекаются** — multi-host канонические теги чужие (single-host не проявляется).
3. **`.env.e2e` и `*.exe` попадают в build-контекст** (`.dockerignore:19-21` не исключает `.env.e2e`).
4. **Контейнер от root**, нет `USER`/`securityContext` в Dockerfile/pod.
5. **Trusted-device HMAC использует SESSION_SECRET** — компрометация секрета подделывает и сессии, и trusted cookie; ротация секрета инвалидирует всё.
6. **Смена email: только постфактум-уведомление**, без верификации нового адреса.
7. **`trackPrefix` на каждый Set в in-memory кэше** — аллокации/contention, хотя DeleteByPrefix редко вызывается.
8. **`GetSettings`+`ListPushSubscriptions` на каждое push-задание** — 2 запроса на уведомление.
9. **`time.After(backoff)` в realtimebus reconnect** — лишний таймер на попытку.
10. **`hasAppliedMigrations` на ошибке возвращает false** — диагностика «свежая БД» вводит в заблуждение при транзиентном сбое.

---

## ⚡ Оптимизации (perf, обоснованы pprof)

| # | Оптимизация | Файл | Ожидаемый эффект |
|---|---|---|---|
| P-1 | **Перенести tryServeAnonCache выше content-рендера** | `render/helper.go:254,281` | убирает template walk из анонимных GET на hit |
| P-2 | **bytes.ReplaceAll без string-конверсий** (nonce/CSRF) | `render/htmlcache.go` | −2-3 копии HTML на запрос |
| P-3 | **GOMEMLIMIT в Dockerfile** (`ENV GOMEMLIMIT=384MiB`) | `Dockerfile` | защита от OOM в pod |
| P-4 | **DashboardTeams через LATERAL/window** | `user/repository.go:298-308` | N+1 → 1-2 прохода |
| P-5 | **LOG_FORMAT=json в pod** | `deploy/pod/*.yaml` | −10% CPU |
| P-6 | **Анонимный SQL-кэш листинга на page 2+** | `svc_listing.go:82-88` | меньше нагрузки на PG при пагинации |
| P-7 | **Ленивая загрузка session в middleware** | `router.go:109` | −Redis round-trip на авторизованных |
| P-8 | **Иммутабельные снапшоты мониторинга** (без deep-копии на поллинг) | `svc_monitor.go:159-191` | −аллокации на SSE-поллеры |
| P-9 | **Префикс-индекс кэша лениво** (только при DeleteByPrefix) | `cache/cache.go:201-224` | −аллокации на каждый Set |
| P-10 | **TTL-кэш push-настроек/подписок** | `notification/service.go:337,346` | −2 запроса на уведомление |

---

## 💡 Улучшения UX

1. **Отзыв trusted-устройств**: страница «Устройства, где я остаюсь в системе» — список + «отозвать все» (реестр в БД вместо stateless cookie).
2. **Верификация нового email**: подтвердить новый адрес (код/ссылка) до фиксации — не только уведомить старый.
3. **Блокировка при смене email**: короткое окно (например 5 мин) до применения — владелец старого ящика успевает отменить.
4. **Прогресс пагинации `/games`**: «показать ещё» (infinite scroll) вместо страниц 2+ — убирает N+1-пагинацию и даёт плавнее UX.
5. **Таймзона**: селектор TZ в профиле (не только cookie из JS) — стабильные даты на дашборде/календаре.
6. **Доступность**: добавить `aria-live` для тостов загрузки и кнопок с иконками.
7. **Email-уведомление при отключении 2FA** — владелец знает о снижении защиты.
8. **Показывать версию сборки** в футере (уже есть `build=` в логах — вынести в UI).

---

## 🛡️ Что проверено и НЕ подтвердилось (честность отчёта)

- **Утечек горутин нет** (20 в покое, стабильно).
- **P-1 подтверждён**: heap 6.3MB vs 18.5MB в PASS-13 (swagger убран).
- **N+1 в tournament/email/monitor** — batch-запросы, не найдено в проверенных путях.
- **Гонки в RoomHub/SSEManager/realtimebus/sessionstore** — атомарные Acquire, каналы не закрываются, typed-сериализация, deferred delete. ОК.
- **CSRF-обёртка и HTML-кэш**: fresh nonce/CSRF подставляются корректно; анонимность фильтруется (session cookie + userID).
- **Email batch-путь**: claim `FOR UPDATE SKIP LOCKED` + sending работает (кроме retry-гонки H4).

---

## 📋 Статус

- 3 аудита: ✅ проведены; HIGH/MEDIUM findings — ✅ эмпирически проверены по коду.
- **HIGH: 6** (H1-H6).
- **MEDIUM: 8** (M1-M8).
- **LOW: 10**.
- **Оптимизации: 10** (P-1..P-10), обоснованы pprof.
- **UX: 8 предложений**.
- Проверки: build ✅, golangci-lint ✅ (0 issues), test-short ✅, E2E 14/14 (в pod).

---

## ✅ Выполненные фиксы (PASS-15, коммит)

### HIGH (все 6)
- **H1**: `tryServeAnonCache` перенесён ДО рендера content-шаблона — на кэш-хите content-template walk больше не исполняется (главный выигрыш для анонимных `/` и `/games`).
- **H2**: подстановка nonce/CSRF через `bytes.ReplaceAll` — без string-конверсий и полных копий HTML.
- **H3**: `ENV GOMEMLIMIT=384MiB` в Dockerfile (защита от OOM/GC-спайков в pod).
- **H4**: email `processRetryJob` — атомарный claim (pending/retry→sending) ДО отправки; обновление статуса по `WHERE status='sending'`. Дублкоты писем исключены.
- **H5**: `clearTrustedDeviceCookie` при смене пароля (`ChangePassword`) и отключении 2FA (`Disable`).
- **H6**: Secure-флаг trusted-device cookie из конфига (TLS/reverse-proxy/FORCE_SECURE_COOKIE) — не уходит по HTTP.

### MEDIUM (6 из 8)
- **M1**: TZOffset включён в ключ HTML-кэша (даты не в чужом TZ).
- **M2**: rate-limit на `/profile/update` (UploadRateLimit 20/5мин).
- **M3**: DashboardTeams — `LEFT JOIN LATERAL` вместо 3 коррелированных подзапросов.
- **M4**: `LOG_FORMAT=json` в pod-манифесте (−10% CPU zerolog).
- **M5**: предупреждения о тестовых секретах/pprof в pod-манифесте.
- **M8**: RoomHub — перепроверка комнаты под Lock при создании очереди (нет осиротевших очередей).

### Отложено (оптимизации с ограниченной выгодой / требуют миграций)
- **M6** (CREATE INDEX без CONCURRENTLY блокирует большие таблицы) — trade-off задокументирован.
- **M7** (sessions round-trip) — требуется точечная проверка ленивой загрузки.
- **P-9/P-10/P-6** (префикс-кэш, push-кэш, page 2+ кэш) — следующий проход.

### Проверки
- build ✅, golangci-lint ✅ (0 issues), test-short ✅ (все пакеты), E2E 14/14 (в pod с новым образом).
- Рекомендуемый порядок: H1-H3 (производительность кэша и OOM) → H4 (email дубликаты) → H5-H6 (trusted 2FA) → M1-M3 → остальное.

---

## ✅ Оптимизации (PASS-15, второй проход — коммит)

### Выполнено
- **P-9**: префикс-индекс in-memory кэша теперь ЛЕНИВЫЙ — строится только при
  первом `DeleteByPrefix` (O(n) разово); до этого `Set`/`Delete`/evict НЕ
  аллоцируют на префиксы (trackPrefix гейтится флагом `prefixTracked`).
- **P-10**: TTL-кэш push-подписок в `NotificationService` (10с) + инвалидация
  при удалении устаревшей подписки. Массовые рассылки: 1 запрос на burst
  вместо N.
- **P-6**: SQL-кэш листинга игр расширен на page 2..10 (версионный ключ
  `games:list:vN:...:page` уже есть) — пагинация анонимов не бьёт в PG.
- **M7 — НЕ ПОДТВЕРДИЛСЯ**: gin-contrib `sessions.Sessions` middleware уже
  ленивый (`session` nil до первого `s.Session()`); ServerStore.Get вызывается
  только при обращении хендлера/Page. Для анонимов без cookie — не грузит.

### Документировано
- **M6**: `CREATE INDEX` (без CONCURRENTLY) в миграциях 44-51 блокирует запись
  на больших таблицах при деплое — осознанный trade-off: CONCURRENTLY нельзя
  в multi-statement batch golang-migrate. Для больших БД — фоновый индекс вручную.

---

## 🔒 HTTPS-проверка (PASS-15, полный стек)

Проверено всё, что можно через HTTPS (pod с самоподписанным сертификатом):

| Проверка | Результат |
|---|---|
| `https://…/healthz` | ✅ 200 (db/valkey/ws/disk — ok) |
| TLS-рукопожатие | ✅ ALPN http/1.1, сертификат gengine.local (SAN: IP 127.0.0.1, 172.30.73.28) |
| **Secure-флаги cookie** | ✅ JWT, refresh_token, gengine_session, _csrf_token — **Secure + HttpOnly + SameSite=Strict** |
| **JS-cookie** | ✅ `tz_offset`, `lang` — **Secure** на HTTPS (НАЙДЕНЫ и исправлены: раньше без Secure — уходили по HTTP) |
| **Trusted-2FA cookie** | ✅ Secure (H6) |
| **HSTS** | ✅ `max-age=63072000; includeSubDomains; preload` |
| **CSP** | ✅ nonce, `connect-src … wss:` (WebSocket через HTTPS разрешён) |
| X-Content-Type-Options | ✅ nosniff |
| X-Frame-Options | ✅ DENY |
| Referrer-Policy / Permissions-Policy | ✅ strict-origin / запрещены чувствительные |
| HTTP→HTTPS | HTTP-запросы отклоняются (400) — сервер слушает только TLS |
| **WSS** | ✅ WebSocket работает через WSS (E2E two-users chat 12-14) |
| **SSE** | ✅ маршруты существуют (302 для анонима, авторизация работает) |
| **E2E через HTTPS** | ✅ 15 passed (14 стандартных + cookie-флаг тест) |

### Найденный и исправленный баг
- `tz_offset` и `lang` cookie ставились JS **без Secure** — на HTTPS уходили по
  HTTP. Исправлено в `layout.html` (Secure при `location.protocol === 'https:'`).
- Добавлен тест `e2e/https-cookies.spec.ts`: проверяет Secure для всех cookie
  и HttpOnly для серверных (JS-куки не могут быть HttpOnly).

---

# DEEP_REVIEW — Gengine-0 (PASS 16, повторное ревью)

> Целевое окружение: **pod через podman** (app+PostgreSQL+Valkey). Метод: pprof в pod
> (heap/goroutine/cpu) + 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая
> проверка каждого HIGH/MEDIUM по коду.

---

## 🔬 pprof-результаты (PASS 16, в pod)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 21 в покое | ✅ Утечек нет. Только служебные: runLoop, roomWorker×0, valkeyBus×2, CheckAutoStartGames/CheckTimeouts×2, SSE-менеджер. |
| **heap inuse** | **5.6 MB** | ✅ Стабильно низко (было 6.3 MB в PASS-15). Остальное — рантайм-инициализация (regexp, gorm schema, bluemonday). |
| **heap alloc** | 7.2 MB за жизнь | ✅ Стартовые инициализации, не hot-path. |
| **cpu (лёгкая нагрузка 300 req)** | 6% (600ms samples) | ⚠️ **43% TLS (FIPS bigmod)** — ожидаемо при HTTPS; остальное — рантайм. App-код практически не виден. |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback, не доступен снаружи (порт не проброшен в pod). |

**Вывод**: приложение лёгкое (5.6MB heap, 21 goroutine, CPU ~6% под нагрузкой). Профилирование
не выявило утечек или горячих точек в прикладном коде — доминирует TLS (шифрование).

---

## 🔴 HIGH (новые)

**Не найдено подтверждённых HIGH в PASS-16.** Кандидаты из аудитов проверены по коду и
закрыты (подробности ниже в «Проверено и закрыто»).

---

## 🟠 MEDIUM (новые)

### M1. getUnreadCount кэширует count=0 при ошибке БД 🔍✅ (подтверждено)
- **Файл**: `internal/domain/notification/service.go:500-525`.
- **Проблема**: при ошибке `repo.CountUnread` (отменённый request-context, недоступная БД)
  `count = 0` и **кэшируется на 30с** (`unreadCache[userID]`). В течение полуминуты все
  пользователи видят «0 непрочитанных», хотя уведомления есть.
- **Фикс**: при ошибке не писать в кэш (только логировать), либо использовать
  `context.WithoutCancel` для CountUnread (как в WS-пути).

### M2. ShutdownQueue: таймаут 10с < SMTP-таймаута 30с 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/email/queue.go:80` + `email.go:481` (smtpTimeout=30с).
- **Проблема**: `ShutdownQueue` ждёт воркеров 10с, но воркер, заблокированный в
  `SendEmail` (до 30с), не успевает. Shutdown продолжается, БД закрывается, воркер
  пишет статус письма в закрытую БД → потеря писем/ошибки.
- **Фикс**: ждать до 2× smtpTimeout (60с) или использовать общий shutdown-контекст,
  который проверяется в `SendEmail` между SMTP-операциями.

### M3. Кэш настроек игры хранит указатель на общий объект 🔍✅ (подтверждено)
- **Файл**: `internal/domain/game/svc_play.go:769-789` + `pkg/cache/cache.go:164`.
- **Проблема**: in-memory LRU возвращает **тот же `*GameSetting`** без копии. Сейчас
  потребитель делает копию (`settings = *cached`), но любой будущий код, мутирующий
  закэшированный объект, испортит кэш для всех.
- **Фикс**: документировать иммутабельность кэшируемых объектов / возвращать копии из
  `cache.Get`.

### M4. realtimebus.Subscribe блокирует старт до 5с при недоступном Valkey 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/realtimebus/bus.go:120-145` (waitReady) + `main.go:255,319`.
- **Проблема**: `waitReady` крутится до `subscribeReadyTimeout=5с` даже если `runOnce`
  уже упал (Valkey недоступен). Два вызова `SetPubSub` → старт задерживается до 10с.
  Публикация при этом всё равно fail-open.
- **Фикс**: прерывать `waitReady` при ошибке первого `runOnce` (канал ready/err),
  либо короче таймаут при fail-подключении.

### M5. sessionstore: fail-open при ошибке Valkey → все пользователи «разлогинены» 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/sessionstore/sessionstore.go:305-308` (Get), `:197-234` (нет deadline).
- **Проблема**: при недоступном Valkey `backend.Get` возвращает ошибку, store молча
  считает сессию новой (пустой). Для аутентификационного слоя fail-open опасен:
  пользователи теряют сессию, а после логина сессия может не сохраниться.
- **Фикс**: короткий deadline на Valkey-операции; рассмотреть fail-closed для критичных
  операций (как у rate-limit login/register) или специальный код ошибки.

### M6. Уведомления о предстоящих играх: дубликаты и O(N×M) 🔍✅ (подтверждено)
- **Файл**: `cmd/server/main.go:502-541`.
- **Проблема**: раз в час для days∈{30,14,7,1} вложенный цикл game×user создаёт
  `Notification.Create` без дедупликации. При сдвиге даты игра может попадать в выборку
  несколько часов → дубликаты. При большой базе — burst записей.
- **Фикс**: уникальный ключ/индекс (`type + game_id + user_id` для напоминаний) или
  проверка существования перед Create; батч-лимит.

### M7. Двойной Valkey round-trip на каждый листинг игр 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/game/svc_listing.go:57,65,86`.
- **Проблема**: каждый запрос листинга делает `GET games:list:version` + `GET
  games:list:vN:...` — 2 последовательных RTT к Valkey, даже на HTML-кэш-хите.
- **Фикс**: кэшировать version в памяти (TTL 100-500мс) или pipeline/MGet.

### M8. HTML-кэш анонимов проверяется ПОСЛЕ загрузки данных 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/game/hnd_game.go:161-169` + `render/helper.go:253`.
- **Проблема**: `ListFilteredPaginated` выполняется до `render.Page()` → на HTML-кэш-хите
  данные всё равно грузятся (2 Valkey GET или SQL). Кэш экономит только template-рендер.
- **Фикс**: проверять htmlcache в хендлере до загрузки данных (ключ path+query+lang+tz)
  либо кэшировать данные листинга и HTML одним уровнем.

### M9. Presence-колбэк на каждую мутацию комнаты без троттлинга 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/monitor/handler.go:337-355`.
- **Проблема**: на каждый join/leave синхронно: RoomClientCount + RoomUserIDs + json.Marshal
  + BroadcastToRoom (в т.ч. PUBLISH в Valkey). При штурме подключений — лавина
  presence-сообщений всем участникам комнаты.
- **Фикс**: дебаунс/throttle presence (не чаще 1/сек на комнату), лимит размера payload.

### M10. cleanupInactiveClients держит эксклюзивный Lock на весь sweep 🔍✅ (подтверждено, perf)
- **Файл**: `internal/pkg/websocket/cleanup.go:55-117`.
- **Проблема**: каждые 30с полный проход по комнатам под `h.mu.Lock()`, внутри
  `client.IsClosed()` (client.mu). При тысячах соединений sweep блокирует
  register/unregister/dispatch.
- **Фикс**: собирать кандидатов через RLock, удалять батчем под Lock; не держать
  client.mu внутри h.mu дольше чтения LastActivity.

---

## 🟡 LOW (новые)

### L1. dispatchToRoom удаляет закрытого клиента без decrement счётчиков 🔍✅
- **Файл**: `internal/pkg/websocket/room_hub.go:393-423`.
- **Проблема**: удаление закрытого клиента из комнаты не вызывает `decConnectionNoLock`
  и не сбрасывает `registered`. Если writePump не вызовет UnregisterClient (паника) —
  утечка лимитов до перезапуска.
- **Фикс**: при удалении закрытого клиента сразу декрементить счётчики и помечать
  registered=false.

### L2. room_hub.isStopped() использует полный Lock на hot path 🔍✅
- **Файл**: `internal/pkg/websocket/room_hub.go:442-446`.
- **Проблема**: вызывается на каждый register и broadcast; достаточно RLock/atomic.
- **Фикс**: `atomic.Bool` для `stopped`.

### L3. Параллельная генерация сертификатов в entrypoint (не подтверждено полностью)
- Статус: не подтверждено (не проверялся entrypoint.sh). Низкий приоритет.

### L4. Пароль `change-me` в deploy/systemd/gengine-db.env 🔍✅
- **Файл**: `deploy/systemd/gengine-db.env:5`.
- **Проблема**: шаблон с `change-me`; при копипасте в прод без замены — слабый пароль БД.
- **Фикс**: добавить gitleaks в CI для блокировки паттернов секретов.

### L5. Email попадает в логи при ошибке генерации reset-кода 🔍✅
- **Файл**: `internal/domain/user/auth_handler.go:609`.
- **Проблема**: `log.Error().Str("email", input.Email)` — PII в логах, частично
  противоречит анти-enumeration.
- **Фикс**: логировать userID или факт ошибки без email.

### L6. Параллельный sweep кэша сессий: cap 512, ок; не подтверждено
- Статус: ok, не требует исправления.

### L7. Leaderboard-инвалидация через SCAN 🔍✅ (perf)
- **Файл**: `game/svc_rating.go:74,165` + `cache/valkey.go:196-225`.
- **Проблема**: `DeleteByPrefix("leaderboard")` делает SCAN+DEL при каждом изменении
  рейтинга. При частых финишах — лишняя нагрузка на Valkey.
- **Фикс**: версионный ключ лидерборда (как `games:list:version`) вместо SCAN.

### L8. Search: OR с ILIKE '%…%' может игнорировать индексы 🔍✅ (perf)
- **Файл**: `game/repository.go:163-172` (Autocomplete), `svc_listing.go:131-139` (search).
- **Проблема**: `search_vector @@ … OR name ILIKE '%..%' OR users.name ILIKE '%..%'` —
  OR между GIN и ведущими wildcard ILIKE → seq scan при росте games. Анонимный поиск
  не кэшируется.
- **Фикс**: trgm-индекс по users.name или UNION; кэшировать анонимный поиск.

### L9. Game-структуры в листинг-кэше содержат лишние поля 🔍✅ (perf)
- **Файл**: `game/svc_listing.go:37-40,194-227`.
- **Проблема**: полные `[]Game` (Description, RegistrationDeadline, CoAuthors…) сериализуются
  в JSON для Valkey, хотя в карточках ~10 полей.
- **Фикс**: DTO для карточек листинга.

---

## 🔍 Проверено и закрыто (кандидаты HIGH/MEDIUM из аудитов, НЕ подтвердились)

| Пункт | Статус | Детали |
|---|---|---|
| **Приватный ключ в git** (security H1) | ✅ Закрыто | `deploy/certs/` в `.gitignore:71`, ключ НЕ в git. |
| **WebSocket CheckOrigin** (security M3) | ✅ Закрыто | `monitor/handler.go:35-57` — exact-host Origin-проверка + `strings.EqualFold` + split host/port; X-Forwarded-Host не доверяется. |
| **Verify-code длина** (security M5) | ✅ Закрыто | Код = 6 байт → `hex.EncodeToString` = **12 символов**, биндинг `len=12` корректен. |
| **ContentHTML XSS** (security) | ✅ Закрыто | `render/helper.go:269` — буфер уже прошёл html/template авто-эскейп; template.HTML только для уже отрендеренного контента. |
| **Глобальный rate-limit +1 RTT** (perf) | ✅ Закрыто | `GlobalRateLimit` использует `NewRateLimiter` (in-memory), не Valkey. |
| **N+1 в листингах games/teams/tournaments** (perf) | ✅ Закрыто | Все JOIN/window-функции/IN-батчи, классических N+1 нет. |
| **Горутин-леки** (perf/reviewer) | ✅ Закрыто | roomWorker idle 30с, monitor-поллеры cancel, valkeyBus ctx/close, SSE wg.Add под RLock — все корректны. |

---

## 💡 Предложения по улучшению UX

### Сейчас (быстрые победы)
1. **Проверка HTML-кэша до загрузки данных** (M8) — ускорит `/games` для анонимов (сейчас двойной Valkey GET даже на кэш-хите).
2. **Дебаунс presence** (M9) — уберёт лавину WS-сообщений при массовых подключениях к комнате.
3. **Кэш version листинга в памяти** (M7) — −1 RTT к Valkey на каждый листинг.

### Пользовательский опыт
4. **Онлайн-индикатор в шапке**: уведомления (колокольчик) уже есть; добавить live-счётчик
   непрочитанных через SSE/WS вместо опроса каждые N секунд (сейчас счётчик обновляется
   при навигации) — снизит задержку уведомлений.
5. **Дашборд**: добавить «последние уведомления» и «активные игры» прямо на дашборд
   (сейчас нужно переходить в разделы).
6. **Оптимистичный UI**: при отправке кода/ответа — сразу показывать результат, не
   блокируя (уже частично есть). Прогресс-бар на длинных операциях (экспорт, бэкап).
7. **Календарь**: при клике на день с играми — показать превью в поповере (сейчас
   отдельная секция ниже); «Сегодня»-кнопка для возврата к текущему месяцу.
8. **Мобильная навигация**: бургер-меню уже есть; добавить swipe/жесты на дашборде
   и игровых экранах.
9. **Доступность (a11y)**: aria-live для уведомлений и ошибок форм уже частично есть
   (role=alert); добавить skip-link, фокус-трап в модалках, контраст для тёмной темы.
10. **Пустые состояния**: единый компонент «ничего не найдено» с CTA (создать/поискать)
    — сейчас местами просто текст.
11. **Валидация форм**: показывать ошибки у поля (inline), а не только вверху страницы;
    уже есть для логина — распространить на регистрацию/профиль.
12. **Пагинация и поиск**: подгрузка «бесконечной» ленты на листингах игр (сейчас
    постраничная пагинация) — меньше кликов, лучше для мобильных.
13. **Тёмная тема**: автоматически переключаться по времени суток уже есть
    (data-theme-auto); добавить «system»-вариант (follow OS) — сейчас только auto/time.

### Кодовая база
14. **DTO для листингов** (L9) — меньше трафика и кэша.
15. **Версионный leaderboard** (L7) — убрать SCAN.
16. **Единый shutdown-контекст** для email/SMTP (M2) — надёжный graceful shutdown.
17. **gitleaks в CI** — блокировать коммиты с секретами.
18. **Документация**: godoc для экспортируемых функций сервисов уже есть; добавить
    `docs/ARCHITECTURE.md` с диаграммой потоков (WS/SSE/pub-sub).
19. **Тесты**: добавить e2e для `/calendar` (уже есть calendar-auth), профиля, 2FA-flow
    (сейчас только unit). Расширить `-race` прогон в CI на весь пакет.
20. **Метрики**: RUM уже собирается (`/api/rum`); добавить Prometheus-метрики времени
    ответа по маршрутам (histogram) — сейчас только счётчики.

---

## 🎯 Итог PASS-16

- **HIGH**: новых не найдено (предыдущие H1-H6 закрыты в PASS-15).
- **MEDIUM**: 10 подтверждённых (M1-M10) — приоритет M1 (unread 0), M2 (email shutdown),
  M5 (sessionstore fail-open), M7/M8 (Valkey round-trips).
- **LOW**: 9 подтверждённых.
- **UX/код**: 20 предложений (3 быстрых победы + 17 улучшений).
- **pprof**: heap 5.6MB, 21 goroutine, CPU ~6% под нагрузкой — приложение лёгкое, утечек нет.

---

# DEEP_REVIEW — Gengine-0 (PASS 17, повторное ревью #2)

> Целевое окружение: **pod через podman**. Метод: pprof в pod (heap/goroutine/cpu) + 3
> параллельных аудита (@reviewer, @security, @perf) с фокусом на НОВЫЕ области (user/2FA,
> admin, tournament, export, payment, level, monitor) + эмпирическая проверка каждого
> HIGH/MEDIUM по коду.

---

## 🔬 pprof-результаты (PASS 17, в pod)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 21 в покое | ✅ Утечек нет (стабильно). |
| **heap inuse** | **9.3 MB** (было 5.6 MB) | ⚠️ Вырос на ~3.7MB — в основном рантайм-инициализация: `bufio.NewWriterSize` (1.6MB), `regexp` (2MB), `pgx type Map` (0.5MB), `i18n.map` (1MB). Это стартовые аллокации, не утечка; рост связан с расширением i18n-словарей и pgx-типов. |
| **cpu (лёгкая нагрузка 300 req)** | 6.8% (690ms samples) | ⚠️ **53.6% TLS (FIPS bigmod)** — HTTPS-шифрование; прикладной код чист. |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback, не проброшен наружу. |

**Вывод**: утечек нет; CPU доминирует TLS. Heap вырос из-за инициализации (буферы, regexp,
pgx-типы, i18n) — не hot-path, не требует вмешательства.

---

## 🔴 HIGH (новые)

### H1. Гонка данных на package-глобалах trustedDeviceSecret/trustedDeviceSecure 🔍✅ (подтверждено)
- **Файл**: `internal/domain/user/two_factor_middleware.go:98-116` + `two_factor_handler.go:179`.
- **Проблема**: глобальные `trustedDeviceSecret`/`trustedDeviceSecure`; `SetTrustedSecure(h.trustedSecure)`
  вызывается из `TwoFactorHandler.Verify` **на каждый запрос**, а `trustedSecureFlag()` читается
  параллельно в `setTrustedDeviceCookie`/`clearTrustedDeviceCookie`/`TwoFactorRequired` —
  data race (поймает `-race`).
- **Фикс**: задавать Secure-флаг один раз при старте (в routes.go, как `SetTrustedSecret`),
  убрать вызов из хендлера; или защитить глобалы мьютексом/atomic.

### H2. Несоответствие lockout-backoff: Go cap 1ч vs SQL cap 24ч 🔍✅ (подтверждено)
- **Файл**: `user/service.go:243-260` (`maxLockDuration = 1h`) vs `user/repository.go:571-577`
  (`LEAST(5 * POWER(2, lock_count), 1440)` минут = до 24ч).
- **Проблема**: реальная блокировка аккаунта может длиться 24ч (потенциальный DoS на
  аккаунт), хотя дизайн и логика разблокировки (backoffDuration) заявляют максимум 1ч.
- **Фикс**: выровнять SQL-кап до 60 минут (`LEAST(..., 60)`) или поднять Go-константу;
  желательно единый источник правды (SQL cap = Go cap).

---

## 🟠 MEDIUM (новые)

### M1. Tournament_points перезаписывается при игре в 2+ турнирах 🔍✅ (подтверждено)
- **Файл**: `tournament/service.go:541` (`UPDATE ... SET tournament_points = CASE id ...` —
  присваивает вместо накопления) + `:261` (`RemoveGame` пересчитывает как СУММУ).
- **Проблема**: для игры в двух турнирах колонка = очки «последнего турнира», а не сумма.
- **Фикс**: `tournament_points = tournament_points + CASE ...` (аккумуляция).

### M2. OAuth-пользователи (без пароля) не могут включить 2FA и легко блокируют аккаунт 🔍✅ (подтверждено)
- **Файл**: `user/two_factor_handler.go:447` — `bcrypt.CompareHashAndPassword(user.Password)`:
  у OAuth-юзера `Password == ""`, bcrypt всегда ошибается → попытки инкрементируются →
  после 5 попыток аккаунт блокируется.
- **Фикс**: для юзеров без пароля пропускать проверку пароля при включении 2FA (или
  требовать установки пароля отдельным шагом).

### M3. Экспорт полного контента доступен любому соавтору (observer/read-only) 🔍✅ (подтверждено)
- **Файл**: `export/handler.go:46-67,110,145,177,283,323` — `checkGameAccess` = `IsUserManager`
  (автор ИЛИ ЛЮБОЙ соавтор без роли), тогда как результаты/статистика требуют `requireModerate`
  (`CanModerateGame`).
- **Проблема**: полный дамп игры с правильными ответами (ExportGameCSV/PDF/Excel) доступен
  соавтору с ролью observer/read-only.
- **Фикс**: для экспорта контента (вопросы/ответы) требовать `requireModerate`, как для Import.

### M4. JSON-импорт уровней: нет лимита размера multipart-тела 🔍✅ (подтверждено)
- **Файл**: `level/handler.go:1138-1153` — `c.Request.FormFile` без `http.MaxBytesReader`;
  `io.LimitReader(5MB)` в `import.go:93` ограничивает только decode, multipart уже в памяти.
- **Фикс**: `c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20)` (как в export).

### M5. Шифрование бэкапа читает весь дамп в память 🔍✅ (подтверждено)
- **Файл**: `admin/service.go:370-398` — `os.ReadFile(srcPath)` для много-гигабайтного дампа.
- **Фикс**: потоковое шифрование (io.Copy с шифрующим writer), не читать файл целиком.

### M6. Payment MarkSucceededIfPending позволяет canceled→succeeded 🔍✅ (подтверждено)
- **Файл**: `payment/repository.go:99-107` — `WHERE status <> 'succeeded'`, а не `= 'pending'`;
  поздний вебхук может «воскресить» отменённый платёж.
- **Фикс**: `WHERE status = 'pending'` (или запретить переход из canceled).

### M7. Export CSV: полные строки attempts + вопросы по одному INSERT 🔍✅ (подтверждено, perf)
- **Файл**: `export/service.go:223` (`GetAttemptsByProgressIDs` тянет все колонки для подсчёта),
  `export/service.go:419-421` (`tx.Create(&question)` на строку CSV).
- **Фикс**: `Select("id")`/`COUNT GROUP BY`; `CreateInBatches` для вопросов.

### M8. Export CSV/Excel буферизуют файл целиком в bytes.Buffer 🔍✅ (подтверждено, perf)
- **Файл**: `export/handler.go:114,291,327,367,407,449,545`.
- **Проблема**: для CSV (потоковый) двойная копия в память не нужна; Excel строит через
  `SetCellValue` (рефлексия на ячейку) — сотни тысяч вызовов на больших играх.
- **Фикс**: CSV — стримить в `c.Writer`; Excel — `excelize.StreamWriter` + `SetSheetRow`.

### M9. Admin-дашборд: 5 COUNT(*) без кэша, seq-скан audit_logs 🔍✅ (подтверждено, perf)
- **Файл**: `admin/handler.go:122-129` + `pkg/audit/audit.go:101-105`.
- **Проблема**: счётчики на каждый заход в админку; `COUNT(*)` по растущей `audit_logs` —
  полный скан; `ORDER BY created_at` без индекса.
- **Фикс**: кэшировать счётчики 30-60с; индекс `audit_logs(created_at DESC)`.

### M10. Monitor чат: SaveMessage 2 запроса, sweepPermCache O(n²) 🔍✅ (подтверждено, perf)
- **Файл**: `monitor/repository.go:278-288` (INSERT + SELECT), `:123-152` (O(n²) sweep).
- **Фикс**: `RETURNING` + JOIN для сообщения; heap/пакетное удаление для кэша прав.

---

## 🟡 LOW (новые)

### L1. Неверный err в логе успешного Login 🔍✅ — `user/service.go:221-223` логирует `err` (nil) вместо `resetErr`.
### L2. ForgotPassword логирует email при выключенном SMTP 🔍✅ — `auth_handler.go:616` (противоречит anti-enumeration).
### L3. Admin CreateUser не валидирует email 🔍✅ — `admin/handler.go:257-298`.
### L4. Admin CreateTeam не добавляет капитана в team_members 🔍✅ — проверить семантику «мои команды».
### L5. Admin ListTeams без верхней границы page 🔍✅ — `admin/handler.go:626` (в отличие от ListUsers cap 10000).
### L6. Payment webhook игнорирует ошибку ReadAll 🔍✅ — `payment/handler.go:128`.
### L7. Tournament AddGame не проверяет существование игры 🔍✅ — FK-защита зависит от миграций.
### L8. Неверные статус-коды tournament (403 вместо 409/400) 🔍✅ — `tournament/handler.go:486,514`.
### L9. User hard-delete не чистит games/passings/progress/attempts/logs 🔍✅ — `user/repository.go:481-537` (сироты или FK-ошибка).
### L10. WebAuthn FinishLogin не перевыпускает session ID 🔍✅ — `webauthn_handler.go:361-508`.
### L11. Enable 2FA не проверяет TwoFactorEnabled 🔍✅ — повторная отправка перегенерирует секрет.
### L12. EnsureAdmin пересчитывает bcrypt и перезаписывает пароль на каждом старте 🔍✅ — `db/db.go:74-100`.
### L13. render.bufferPool без ограничения размера 🔍✅ — одна большая страница пиннит большой буфер.
### L14. htmlcache 2 прохода bytes.ReplaceAll на каждый анонимный хит 🔍✅.
### L15. gitleaks allowlist слишком широкий (литералы паролей глобально, *_test.go) 🔍✅.
### L16. CSV-safe (апостроф) виден в ячейках Excel 🔍✅ — UX-грязь, безопасно.
### L17. SearchUsersLight ILIKE email без индекса 🔍✅; audit Log синхронный INSERT 🔍✅; audit OFFSET-пагинация.

---

## 🔍 Проверено и закрыто (кандидаты HIGH/MEDIUM, НЕ подтвердились)

| Пункт | Статус | Детали |
|---|---|---|
| **CSV re-import каскад** | ✅ Закрыто | `answers.question_id REFERENCES questions(id) ON DELETE CASCADE` (000001_init.up.sql:128). |
| **`.env` в git** | ✅ Закрыто | `git ls-files .env .env.e2e` — пусто (в .gitignore). |
| **Calendar popover XSS** | ✅ Закрыто | `escapeHtml(game.name)`/`escapeHtml(game.time)` применены. |
| **ThemeMode валидация** | ✅ Закрыто | whitelist system/time/dark/light + HH:MM валидация. |
| **OAuth state / refresh family / WebAuthn** | ✅ Закрыто | state + ConstantTimeCompare + TTL; семейная ротация + reuse-revoke; userHandle сверка + CloneWarning. |
| **Payment webhook подпись** | ✅ Закрыто | IP-allowlist + API-подтверждение + сверка суммы/валюты (int64 копейки) + атомарный claim. |
| **CSV injection** | ✅ Закрыто | `csvSafe` обрабатывает `= + - @ \t \r` после ведущих пробелов. |
| **Backup path-traversal** | ✅ Закрыто | isWithinBackupDir + Rel; AES-256-GCM, 0600. |
| **N+1 в новых областях** | ✅ Закрыто | export/tournament/monitor — Preload с колонками, батчи. |

---

## 💡 Предложения по улучшению (PASS 17)

### Быстрые победы (1-2 дня)
1. **H1**: убрать SetTrustedSecure из хендлера → один раз в routes.go.
2. **H2**: выровнять SQL-кап блокировки до 60 минут.
3. **M6**: `WHERE status = 'pending'` в MarkSucceededIfPending.
4. **M3**: экспорт контента — requireModerate.

### Оптимизация
5. **M7/M8**: export CSV — стриминг, вопросы CreateInBatches, Excel StreamWriter.
6. **M9**: кэш admin-счётчиков + индекс audit_logs(created_at).
7. **M10**: RETURNING для сообщений чата + фикс O(n²) sweep.

### UX
8. **Онлайн-индикатор в шапке**: live-счётчик непрочитанных через SSE/WS (уже есть
   дебаунс presence, но счётчик обновляется при навигации).
9. **Дашборд**: кэш результата на 15-30с (сейчас 4 запроса на заход).
10. **2FA для OAuth**: отдельный шаг «установите пароль» перед включением 2FA.

---

## 🎯 Итог PASS-17

- **HIGH**: 2 новых (H1 trusted-globals race, H2 backoff 24ч) — оба подтверждены по коду.
- **MEDIUM**: 10 новых (M1-M10).
- **LOW**: 17 новых (L1-L17).
- **Закрыто**: 9 кандидатов (каскады, .env, XSS, валидация, OAuth/WebAuthn, webhook, CSV-injection, backup, N+1).
- **pprof**: heap 9.3MB (стартовые аллокации), 21 goroutine, CPU ~6.8% (TLS) — утечек нет.
- **Приоритет**: H1 (гонка), H2 (DoS блокировки), M6 (платежи), M3 (экспорт-права).

---

# DEEP_REVIEW — Gengine-0 (PASS 18, повторное ревью #3)

> Целевое окружение: **pod через podman**. Метод: pprof в pod (heap/goroutine/cpu) + 3
> параллельных аудита (@reviewer, @security, @perf) с фокусом на game hot-path (SubmitCode),
> monitor (чат/пполлер), snapshot/мониторинг, dashboard + эмпирическая проверка каждого
> HIGH/MEDIUM по коду.

---

## 🔬 pprof-результаты (PASS 18, в pod)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 21 в покое | ✅ Утечек нет (стабильно с PASS-15). |
| **heap inuse** | **6.7 MB** (было 9.3 в PASS-17) | ✅ Снизился: стартовые аллокации (validator, codec, deepcopy, regexp), не hot-path. |
| **cpu (лёгкая нагрузка 300 req)** | 4.6% (460ms samples) | ⚠️ **41% TLS (FIPS bigmod)** + syscall 13% — прикладной код чист. |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback. |

**Вывод**: heap снизился до 6.7MB (убрали лишние инициализации в прошлых раундах), CPU чист.
Профилирование подтверждает: горячих точек в прикладном коде нет, доминирует TLS.

---

## 🔴 HIGH (новые)

### H1. GetOrFetchSnapshotJSON возвращает кэш без копии (нарушение LOW #14) 🔍✅ (подтверждено)
- **Файл**: `internal/domain/game/svc_monitor.go:203-204` vs `:188-190`.
- **Проблема**: на основном кэш-хите (188-190) возвращается **копия** `[]byte`, но на ветке
  «json устарел / nil» (203-204) возвращается `cached.json` **напрямую**. Любой будущий
  потребитель, сделавший append/мутацию, испортит LRU-запись — контракт LOW #14 нарушается.
- **Фикс**: единообразно возвращать копию (`append([]byte(nil), cached.json...)`).

### H2. SubmitCode: полный reload Level.Questions.Answers на каждую попытку 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/game/svc_attempt.go:33-34` + `svc_progress.go:98-99`.
- **Проблема**: комментарий в svc_attempt.go:30 ошибочен — `GetCurrentProgressForUpdate`
  НЕ прелоадит Level (svc_progress.go:98 «без Preload уровня»). SubmitCodeWithTx выполняет
  `Preload("Questions.Answers").First(&lvl, ...)` на КАЖДУЮ отправку кода (2 запроса на
  горячем пути). Граф ответов уровня статичен на время игры.
- **Фикс**: кэшировать ответы уровня (по levelID, инвалидация при редактировании) или
  проверять ответы через SQL `EXISTS` без загрузки графа.

### H3. SaveMessage: повторный SELECT после INSERT (чат) 🔍✅ (подтверждено, perf)
- **Файл**: `monitor/repository.go:282-292`.
- **Проблема**: `Create` уже заполняет ID/CreatedAt, затем `GetMessageByID` — ещё SELECT +
  Preload User на каждое сообщение чата (2 запроса вместо 1). Плюс двойная санитизация в
  ChatWS (handler.go:918 и :971).
- **Фикс**: возвращать созданный msg напрямую (User заполнить одним JOIN/кэшем имени);
  убрать вторую StripHTML.

---

## 🟠 MEDIUM (новые)

### M1. SSE: утечка прогресса других команд 🔍✅ (подтверждено, security)
- **Файл**: `hnd_sse.go:494` — `sseConnect(mgr, c, passing.GameID)` подписывает участника
  команды на ВСЮ игру; `broadcastLocal(gameID,...)` шлёт `hint_available`/`level_completed`
  всех команд. Участник команды A видит использование подсказок командой B (тактическая
  утечка в соревновании).
- **Фикс**: подписывать SSE по passing_id (или фильтровать события по passing_id; менеджерам
  оставить game-wide).

### M2. Захардкоженный admin-пароль в отслеживаемом манифесте 🔍✅ (подтверждено, security)
- **Файл**: `deploy/pod/gengine-pod.yaml:91` (`ADMIN_PASSWORD: AdminPod123456!`).
- **Проблема**: реальная строка пароля в git; gitleaks-allowlist её не покрывает. Копипаста
  в прод = известный админ-пароль.
- **Фикс**: плейсхолдер `__SET_A_STRONG_PASSWORD__` (или gitleaks-правило на `ADMIN_PASSWORD`
  в этом файле).

### M3. Schedule/flush гонка в debounce-диспетчере снапшотов 🔍✅ (подтверждено, reviewer)
- **Файл**: `svc_snapshot.go:37-65`.
- **Проблема**: flush старого таймера может удалить НОВЫЙ таймер из map (после перезаписи
  Schedule) → двойной/потерянный ProcessSnapshot. Смягчено advisory-lock + идемпотентной
  рассылкой.
- **Фикс**: в flush сравнивать `d.timers[gameID] == t` (передавать таймер в замыкание).

### M4. presenceLast растёт без sweep 🔍✅ (подтверждено, reviewer+security)
- **Файл**: `monitor/handler.go:308,362`.
- **Проблема**: запись в `presenceLast` на каждую комнату никогда не удаляется → неограниченный
  рост при активном использовании личных/командных комнат. (Аналог chatRooms решён.)
- **Фикс**: удалять при `RoomClientCount(roomID) == 0` или ленивый sweep.

### M5. storeAnonCache может алиасить буфер из sync.Pool 🔍✅ (подтверждено, perf+reviewer)
- **Файл**: `htmlcache.go:150-177`.
- **Проблема**: `bytes.ReplaceAll` возвращает ту же slice, если плейсхолдер не найден →
  `out` ссылается на backing-array `layoutBuf`, который после putBuffer перезаписывается.
  Сейчас nonce/csrf есть всегда, но контракт хрупкий.
- **Фикс**: `bytes.Clone(out)` перед кэшированием.

### M6. StartTesting переиспользует чужую команду `_test_<userID>` 🔍✅ (подтверждено, reviewer)
- **Файл**: `svc_play.go:492-525`.
- **Проблема**: `WHERE name = ?` находит любую команду с таким именем (включая чужую
  реальную команду, названную `_test_5`), создавая тестовый passing на чужую команду.
- **Фикс**: `AND captain_id = ?` или проверка принадлежности.

### M7. Dashboard: 4 последовательных запроса + тяжёлый ListByUser(5) 🔍✅ (подтверждено, perf)
- **Файл**: `dashboard_service.go:94-157` + `wire_providers.go:238-256`.
- **Проблема**: 4 независимых запроса последовательно; `ListByUser(0,5)` тянет
  `notifications.*` + `COUNT(*) OVER()` по всем уведомлениям ради 5 строк.
- **Фикс**: errgroup для независимых запросов; лёгкий `ListRecentByUser` (SELECT нужных
  колонок LIMIT 5, без window-count) + TTL-кэш.

### M8. DashboardTeams: LATERAL с 3 коррелированными подзапросами на строку 🔍✅ (подтверждено, perf)
- **Файл**: `user/repository.go:290-328`.
- **Проблема**: на каждую команду пользователя — 3 подзапроса по level_progresses/levels.
  Для десятков команд — сотни коррелированных выборок на визит дашборда.
- **Фикс**: один проход по level_progresses для всех passings (GROUP BY game_passing_id).

### M9. UseHint не использует кэш настроек игры 🔍✅ (подтверждено, perf)
- **Файл**: `svc_play.go:298-306`.
- **Проблема**: `GetGameplayData` кэширует GameSetting (60с), но UseHint читает из БД на
  каждый вызов подсказки.
- **Фикс**: использовать `game:settings:%d` кэш с fallback.

### M10. Poller SSE: полная копия JSON + bytes.Equal каждые 5с 🔍✅ (подтверждено, perf)
- **Файл**: `monitor/handler.go:179-205` + `svc_monitor.go:181-192`.
- **Проблема**: каждый тик (5с) на игру — копия `[]byte` кэша + полное сравнение JSON.
  O(N × размер_снапшота).
- **Фикс**: fingerprint (длина/CRC32/версия) вместо полного сравнения.

---

## 🟡 LOW (новые)

- **L1**: `svc_play.go:189` — `map[bool]string{...}` аллоцируется на каждый SubmitCode.
- **L2**: `tx.Save(progress)` пишет все колонки вместо точечного `Updates` (UseHint/CompleteLevel).
- **L3**: `svc_passing.go:222-230` — validTransitions map пересоздаётся на каждый вызов.
- **L4**: `gameplay-show.html:554` — `data.level_name` не отправляется бэкендом → «undefined» в toast.
- **L5**: `monitor/handler.go:914-966` — нет лимита длины Content после StripHTML (до 32KB/сообщение).
- **L6**: `RoomUserIDs` аллоцирует map на каждый presence.
- **L7**: `audit.go:100` — offset без верхней границы (perPage клампить в handler).
- **L8**: `storeAnonCache`/`GetOrFetchSnapshotJSON` — см. H1/M5 (копии).
- **L9**: `GetGameplayData` на ошибке SubmitCode — ~10 запросов на одну неверную попытку (H2 смежный).
- **L10**: `/api/games/{id}/stats` грузит все reviews без лимита.
- **L11**: `CloseVoting` шлёт письма по одному (Enqueue вместо EnqueueBatch).

---

## 🔍 Проверено и закрыто (кандидаты, НЕ подтвердились)

| Пункт | Статус | Детали |
|---|---|---|
| **Приватный ключ TLS в git** | ✅ Закрыто | `git ls-files deploy/certs/ .env` — пусто (в .gitignore). |
| **Uploads magic-bytes** | ✅ Закрыто | `local_storage.go` — `http.DetectContentType`, path-traversal/symlink заблокированы, chmod 0600/0700. |
| **IDOR дашборд-уведомления** | ✅ Закрыто | userID строго из контекста; чужых уведомлений нет. |
| **PII в поиске** | ✅ Закрыто | email маскируется для не-админов; аноним — пустая строка. |
| **XSS в новых попапах/JS** | ✅ Закрыто | CSP nonce, escapeHtml в showToast, ThemeMode allowlist. |
| **RUM валидация** | ✅ Закрыто | IPRateLimit 60/мин, NaN/Inf клампы, page truncation. |
| **SSRF/CSRF новые маршруты** | ✅ Закрыто | новых SSRF-потенциалов нет; формы под gorilla/csrf. |
| **RoomHub runLoop/Acquire, SSE wg.Add под RLock** | ✅ Закрыто | атомарность, идемпотентный unregister, корректный wg.Add. |
| **Миграции 000070/000071** | ✅ Закрыто | закрывают audit_logs(created_at), users(email) trgm. |

---

## 💡 Предложения по улучшению (PASS 18)

### Быстрые победы (1-2 дня)
1. **H1**: копия `cached.json` в GetOrFetchSnapshotJSON (одна строка).
2. **M2**: плейсхолдер для ADMIN_PASSWORD в манифесте.
3. **M5**: `bytes.Clone(out)` в storeAnonCache.
4. **M4**: sweep presenceLast.

### Оптимизация (эффект на hot-path)
5. **H2**: кэш/`EXISTS` для проверки ответов уровня — −2 запроса с каждого SubmitCode.
6. **H3**: убрать GetMessageByID после Create в чате — −1 запрос/сообщение.
7. **M7**: errgroup + лёгкий ListRecentByUser — быстрее дашборд.
8. **M8**: GROUP BY вместо LATERAL в DashboardTeams.

### Безопасность
9. **M1**: скоупинг SSE по passing_id (закрыть тактическую утечку).
10. **M6**: проверка captain_id при переиспользовании test-команды.

### UX
11. **L4**: добавить level_name в SSE-payload (исправить «undefined»).
12. **L5**: лимит длины чат-сообщения (4000 символов).

---

## 🎯 Итог PASS-18

- **HIGH**: 3 новых (H1 snapshot-копия, H2 SubmitCode reload, H3 чат 2 запроса) — подтверждены.
- **MEDIUM**: 10 новых (M1-M10).
- **LOW**: 11 новых (L1-L11).
- **Закрыто**: 9 кандидатов (сертификаты, uploads, IDOR, PII, XSS, RUM, SSRF, RoomHub, миграции).
- **pprof**: heap 6.7MB (снизился), 21 goroutine, CPU ~4.6% (TLS) — утечек нет.
- **Приоритет**: H1 (копия), M1 (SSE-утечка), H2/H3 (hot-path), M2 (пароль в манифесте).

---

# DEEP_REVIEW — Gengine-0 (PASS 19, повторное ревью #4)

> Целевое окружение: **pod через podman**. Метод: pprof в pod (heap/goroutine/cpu) + 3
> параллельных аудита (@reviewer, @security, @perf) с фокусом на ранее не прочитанные
> файлы (svc_crud/cover/admin/rating, fullpreview, simulate, geolocation, audit) +
> эмпирическая проверка каждого HIGH/MEDIUM по коду.

---

## 🔬 pprof-результаты (PASS 19, в pod)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 21 в покое | ✅ Утечек нет (стабильно с PASS-15). |
| **heap inuse** | **8.4 MB** | ⚠️ Стартовые аллокации: bufio (2.6MB), excelize (0.65MB), regexp (1MB), i18n (0.5MB). Не hot-path. |
| **cpu (лёгкая нагрузка 300 req)** | 5.1% (510ms samples) | ⚠️ **37% TLS (FIPS bigmod)** + syscall 12% — прикладной код чист. |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback. |

**Вывод**: утечек нет; heap 8.4MB (инициализация), CPU чист. Профилирование подтверждает
отсутствие горячих точек в прикладном коде — доминирует TLS-шифрование.

---

## 🔴 HIGH (новые)

### H1. Кэш ответов уровня НЕ хитится с Valkey (регресс PASS-18 H2) 🔍✅ (подтверждено)
- **Файл**: `internal/domain/game/svc_attempt.go:153-166`.
- **Проблема**: `loadLevelWithAnswers` читает через `s.cache.GetWithCtx(ctx, key)` + `v.(level.Level)`.
  Для in-memory кэша — работает; для **Valkey** (`VALKEY_HOST/PORT`, рекомендованная конфигурация)
  `GetWithCtx` возвращает `map[string]any` (JSON-unmarshal) → assertion всегда false → кэш
  МЁРТВ → на каждую попытку SubmitCode снова `Preload("Questions.Answers")` = 2 запроса
  (цель PASS-18 не достигнута в production).
- **Фикс**: использовать `cacheGetJSON[level.Level]` (аналог cacheGetGame/cacheGetRating),
  который корректно работает и с Valkey (GetBytesWithCtx + json.Unmarshal), и с in-memory.

### H2. SSE-фильтр passing_id ломается для cross-instance событий (регресс PASS-18 M1) 🔍✅ (подтверждено)
- **Файл**: `hnd_sse.go:372-377` + `sse_pubsub.go:66-90`.
- **Проблема**: `broadcastLocal` извлекает `dm["passing_id"].(uint)`. Локальные вызовы кладут
  `uint` — фильтр работает. Но события с ДРУГОГО инстанса приходят через `handleRemoteBroadcast`
  (`json.Unmarshal` → `float64`) → assertion падает → `eventPassingID == 0` → **фильтр молча
  отключается**: участник команды A получает `hint_available`/`level_completed` команды B
  в multi-instance (тактическая утечка возвращается).
- **Фикс**: извлекать `passing_id` типобезопасно (switch по uint/float64/int64) или прокидывать
  passingID отдельным полем `sseBusMsg`.

---

## 🟠 MEDIUM (новые)

### M1. Публикация игры без уровней через cover-роут 🔍✅ (подтверждено, security)
- **Файл**: `svc_cover.go:53` (`IsDraft: dto.IsDraft`) vs `svc_crud.go:53` (жёстко `IsDraft=true`) +
  `svc_crud.go:135` (Publish проверяет `CountLevelsByGame > 0`).
- **Проблема**: через cover-роут клиент может создать игру сразу `IsDraft=false`, минуя guard
  Publish «нельзя опубликовать игру без уровней». Игра без уровней появляется в публичных списках.
- **Фикс**: в cover-пути при `IsDraft=false` проверять `CountLevelsByGame > 0`, либо всегда
  создавать черновик (публикация — только через Publish).

### M2. UpdateGameWithCover удаляет старую обложку до коммита БД 🔍✅ (подтверждено)
- **Файл**: `svc_cover.go:102-122`.
- **Проблема**: сохранить новый файл → удалить старый → `gameRepo.Update`. Если Update упадёт,
  в БД останется старый путь (файл уже удалён) → битая обложка; новый файл — сирота.
- **Фикс**: сначала Update БД, затем удалять старый файл (после успешного коммита).

### M3. Ошибка storage.Delete глотается, путь в БД затирается 🔍✅ (подтверждено)
- **Файл**: `svc_cover.go:102-108`.
- **Проблема**: `storage.Delete` вернул ошибку → только log.Error, но `CoverPath=""` и БД
  сохраняет пустой путь; файл остаётся на диске (орфан).
- **Фикс**: при ошибке удаления не очищать путь в БД (вернуть ошибку/оставить старый путь).

### M4. ForceFinishGame/DisqualifyTeam не заполняют ResultDuration/FinishedAt 🔍✅ (подтверждено)
- **Файл**: `helpers.go:50-62` (finishPassingProgress) + `svc_admin.go:93-103,152-160`.
- **Проблема**: принудительно завершённые прохождения получают нулевую длительность в
  результатах/лидерборде.
- **Фикс**: в finishPassingProgress выставлять `FinishedAt = now` и `ResultDuration = now - CreatedAt`.

### M5. Уведомления капитанам наследуют отменяемый request-контекст 🔍✅ (подтверждено)
- **Файл**: `svc_admin.go:294,346` (`context.WithTimeout(ctx, ...)`).
- **Проблема**: notifyCaptainsAboutFinish/Disqualification вызываются ПОСЛЕ коммита, но строят
  таймаут от `ctx` запроса — при отключении админа письма молча не отправятся.
- **Фикс**: `context.WithoutCancel(ctx)` (как gameFinishedCallback).

### M6. ChatRoomIDs делает 5-8 последовательных запросов 🔍✅ (подтверждено, perf)
- **Файл**: `monitor/handler.go:1077-1170`.
- **Проблема**: IsUserManager + GetPassingByUser + 4× GetOrCreateRoom на каждую загрузку чата.
- **Фикс**: один SELECT всех комнат игры + создание недостающих батчем; кэш roomID 5-10с.

### M7. Geolocation: 3 запроса на каждый GPS-update 🔍✅ (подтверждено, perf)
- **Файл**: `hnd_geolocation.go:71-90`.
- **Проблема**: каждый POST location (rate-limit 60/мин) делает GetByID + IsTeamMember + upsert.
- **Фикс**: короткий TTL-кэш «passingID→(teamID,status,memberOK)» на 30-60с.

### M8. audit: синхронный INSERT + OFFSET-пагинация 🔍✅ (подтверждено, perf)
- **Файл**: `pkg/audit/audit.go:52-69,99-106`.
- **Проблема**: Log() — синхронный INSERT на 84 callsite (1 RTT на каждое админ-действие);
  List с OFFSET на глубоких страницах O(N).
- **Фикс**: буферизованная асинхронная запись (канал + батч) с метрикой; keyset-пагинация.

### M9. Leaderboard версионируется только в памяти инстанса 🔍✅ (подтверждено, security)
- **Файл**: `svc_rating.go:40-42,208`.
- **Проблема**: в multi-instance инстанс B отдаёт устаревший кэш лидерборда до 5 минут.
- **Фикс**: версия через Valkey (общий счётчик) или TTL-кэш.

### M10. Исключённый из команды участник читает командный чат 🔍✅ (подтверждено, security)
- **Файл**: `monitor/handler.go:872-997` (read-loop проверяет членство только при отправке).
- **Проблема**: после исключения участник остаётся подключённым и читает командный чат до
  дисконнекта/таймаута (пассивный доступ).
- **Фикс**: при изменении членства закрывать сокеты участников (hub.CloseRoomClients) или
  периодический re-check в read-loop.

---

## 🟡 LOW (новые)

- **L1**: Simulate считает пустой код успехом; LevelsPassed++ даже для неуспешных шагов.
- **L2**: SnapshotDispatcher — возможны перекрывающиеся fn при тяжёлом пересчёте.
- **L3**: GetLocationsByGameWithFreshness не фильтрует по окну свежести (название вводит в заблуждение).
- **L4**: audit.List молча отбрасывает ошибочный userIDStr-фильтр; глубокий OFFSET.
- **L5**: кэш лидерборда держит устаревшие имена/аватары до 5 минут.
- **L6**: GetStats глотает ошибки рейтинга/отзывов (страница «нет рейтинга» без признака сбоя).
- **L7**: player_locations растёт бессрочно (нет джоба очистки старых).
- **L8**: 5-секундное окно perm-cache после исключения (если не все пути инвалидируют).
- **L9**: ADMIN_PASSWORD плейсхолдер известен (не fail-fast при запуске с заглушкой).
- **L10**: CI adminpass123 (11 символов, 2 класса) может не пройти requireStrongPassword.

---

## 🔍 Проверено и закрыто (кандидаты HIGH/MEDIUM, НЕ подтвердились)

| Пункт | Статус | Детали |
|---|---|---|
| **Конфликт chat_rooms уникальных индексов** | ✅ Закрыто | 000064/000067 — частичные (WITH WHERE room_type=... AND deleted_at IS NULL), не конфликтуют. |
| **Индекс player_ratings(score)** | ✅ Закрыто | `idx_player_ratings_score` уже есть (000038). |
| **`.env`/certs в git** | ✅ Закрыто | `git ls-files` — пусто. |
| **CRUD/обложки/full-preview права** | ✅ Закрыто | Update/Publish — CanEditContent; Delete — владелец; FullPreview — IsUserManager ДО отдачи ответов. |
| **Geolocation доступ** | ✅ Закрыто | UpdateLocation — членство + status; LocationsByGame — под GameManager. |
| **Monitor data/WS права** | ✅ Закрыто | MonitorData/GameRooms/LogsWS — gameManager; Vote/CloseVoting — членство + FOR UPDATE. |
| **svc_attempt cache утечка ответов** | ✅ Закрыто | ответы кэшируются server-side, клиенту не отдаются. |
| **dashboard errgroup гонки** | ✅ Закрыто | разные поля структуры — data race отсутствует. |
| **chat rooms гонки GetOrCreate** | ✅ Закрыто | уникальные индексы + повторный SELECT при race. |

---

## 💡 Предложения по улучшению (PASS 19)

### Быстрые победы
1. **H1**: cacheGetJSON для level:answers (Valkey-hit восстановить).
2. **H2**: типобезопасное извлечение passing_id (switch uint/float64).
3. **M1**: guard публикации без уровней в cover-пути.

### Оптимизация
4. **M6/M7**: кэш roomID в чате, TTL-кэш геолокации.
5. **M8**: асинхронный audit-INSERT.

### Безопасность
6. **M10**: закрывать сокеты исключённых участников.
7. **L9**: fail-fast на ADMIN_PASSWORD-заглушку в production.

### UX
8. **M4**: ResultDuration для принудительно завершённых игр (корректные результаты).
9. **L5**: обновлять имена в лидерборде.

---

## 🎯 Итог PASS-19

- **HIGH**: 2 новых (H1 Valkey-cache регресс, H2 SSE float64 регресс) — оба «регрессии» фиксов PASS-18, важны.
- **MEDIUM**: 10 новых (M1-M10).
- **LOW**: 10 новых (L1-L10).
- **Закрыто**: 9 кандидатов (индексы chat_rooms/player_ratings, .env, права CRUD/geolocation/monitor, cache, errgroup).
- **pprof**: heap 8.4MB, 21 goroutine, CPU ~5.1% (TLS) — утечек нет.
- **Приоритет**: H1 (Valkey), H2 (SSE multi-instance), M1 (публикация без уровней), M10 (чат после исключения).

---

# DEEP_REVIEW — Gengine-0 (PASS 20, повторное ревью #5)

> Целевое окружение: **pod через podman**. Метод: pprof в pod (heap/goroutine/cpu) + 3
> параллельных аудита (@reviewer — домены notification/calendar/social/team/tournament/payment,
> @security — user/middleware/security/sessionstore/websocket/recaptcha, @perf — cache/websocket/
> realtimebus/level/export/monitor/dashboard) + эмпирическая проверка каждой находки по коду.

---

## 🔬 pprof-результаты (PASS 20, в pod)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 22 в покое | ✅ Утечек нет (+1 — новый audit worker из M8 PASS-19, ожидаемо). |
| **heap inuse** | **7.3 MB** | ⚠️ Стартовые аллокации: excelize (0.65MB), bluemonday (0.5MB), regexp (1MB), yaml (0.5MB), prometheus (0.5MB), gorm schema. Не hot-path. |
| **cpu (лёгкая нагрузка)** | 0.42% (50ms / 12s) | ✅ **100% TLS handshake (RSA SignPSS, FIPS bigmod)** — прикладной код чист. |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback. |

**Вывод**: утечек нет; heap 7.3MB (инициализация), CPU чист (доминирует TLS). Стабильно с PASS-15.

---

## 🔴 HIGH (новые)

### H1. Tournament RemoveGame декрементирует games_played для НЕ начисленных прохождений → потеря результатов 🔍✅ (подтверждено)
- **Файл**: `internal/domain/tournament/service.go:204-241` + `scoredPointsForTournament:629-641`.
- **Проблема**: `RemoveGame` выбирает ВСЕ `finished`-прохождения игры (`game_id AND status=finished`,
  строка 204, БЕЗ фильтра по турниру), затем `points = scoredPointsForTournament(p, tournamentID)`
  (строка 220). Для прохождений, которые ещё НЕ начислены в ЭТОМ турнире (`tournament_scored_ids`
  не содержит tournamentID), `scoredPointsForTournament` возвращает **0**. Но `UPDATE tournament_results
  SET games_played = tr.games_played - 1` (строка 228) применяется БЕЗУСЛОВНО ко всем `teamIDs`, и
  `DELETE ... WHERE games_played <= 0` (строка 237) удаляет валидные строки.
- **Сценарий потери**: команда X в турнире A финишировала игру 1 (начислено, `games_played=1`) и игру 2
  (scoring ещё не отработал → не начислено). Автор удаляет игру 2 (`scoredCount==0` → не-админ может).
  `RemoveGame(игра2)`: для X points=0, но `games_played = 1-1 = 0` → `DELETE WHERE games_played <= 0`
  удаляет строку результата игры 1. Команда теряет очки в лидерборде.
- **Дополнительно**: `RemoveGame` НЕ берёт `pg_advisory_xact_lock(gameID)` (в отличие от
  `UpdateScoresForGame:453`) — сценарий реализуем и как гонка.
- **Фикс**: декрементировать `games_played` только для команд с `points > 0` (фильтровать `teamIDs`
  по ненулевым `points`), либо взять advisory-lock на `gameID` в начале транзакции.

### H2. POST /auth/2fa/verify и /auth/2fa/backup без AuthRequired → 2FA step-up сломан 🔍✅ (подтверждено)
- **Файл**: `internal/domain/user/routes.go:96,98` vs `:95,97`.
- **Проблема**: GET-маршруты `/auth/2fa/verify` и `/auth/2fa/backup` имеют `AuthRequired`, но POST —
  только `LoginRateLimit`. Оба POST-обработчика (`two_factor_handler.go:101` `Verify`,
  `:235` `BackupVerify`) берут `c.GetUint("userID")` (строка 101) → без `AuthRequired` всегда `0` →
  редирект на `/auth/login` (строка 103).
- **Влияние**: `TwoFactorRequired` (middleware) редиректит админа на `/auth/2fa/verify?return_url=...`
  (строка 219), форма (GET) показывается, но POST-сабмит TOTP-кода теряет userID → бесконечный цикл
  login→2fa→login. **Админ с включённой 2FA не может пройти step-up → полностью теряет доступ к
  `/admin/*`, `/metrics`, `/debug/pprof`, `/swagger`** (DoS на защищённые маршруты). Не эскалация, но
  функционально-безопасностный дефект критичного потока.
- **Фикс**: добавить `middleware.AuthRequired(authSvc)` на POST `/auth/2fa/verify` и `/auth/2fa/backup`.

### H3. JSON-импорт уровней: построчный INSERT (~до 100M строк теоретически) + advisory-lock на всю транзакцию 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/level/import.go:160-183` + lock `:108`.
- **Проблема**: каждый уровень (`tx.Create(lvl)`), вопрос (`tx.Create(q)`) и ответ (`tx.Create(&Answer)`)
  вставляется ОТДЕЛЬНЫМ INSERT внутри одной транзакции с `pg_advisory_xact_lock(gameID)`.
  Лимиты: `maxImportLevels=5000`, `maxImportQuestionsPerLevel=200`, `maxImportAnswersPerQuestion=100`
  (строки 48-50) → теоретически 100M сущностей (реально ограничено `io.LimitReader` 5MB, строка 93,
  но всё равно сотни тысяч round-trip).
- **Влияние**: долгая транзакция (секунды-минуты) держит advisory-lock на игру → все операции с игрой
  (submit attempt и др.) блокируются. CSV-импорт УЖЕ батчится (`export/service.go:445-458`,
  `CreateInBatches`), а JSON — нет.
- **Фикс**: собрать уровни/вопросы/ответы в слайсы и вставлять батчами (уровни одним `CreateInBatches`,
  вопросы одним, ответы одним флэт-батчем после присвоения `QuestionID`).

---

## 🟠 MEDIUM (новые)

### M1. Team CreateTeam не атомарен: осиротевшая команда без капитана 🔍✅ (подтверждено)
- **Файл**: `internal/domain/team/service.go:146-157`.
- **Проблема**: `teamRepo.Create` выполняется, затем `AddMember` отдельно. Если `AddMember` вернёт
  ошибку (гонка `ON CONFLICT DO NOTHING` → `ErrAlreadyInOtherTeam`), команда остаётся БЕЗ капитана,
  а хендлер показывает ошибку → пользователь повторяет и создаёт вторую команду.
- **Фикс**: обернуть `Create` + `AddMember` в транзакцию (или удалять команду при неудаче `AddMember`).

### M2. Notification MarkAllAsRead не ставит read_at → retention не чистит 🔍✅ (подтверждено)
- **Файл**: `internal/domain/notification/repository.go:142-146` vs `:135-139,149-152`.
- **Проблема**: `MarkAsRead` ставит `read_at: time.Now()`, а `MarkAllAsRead` — только `Update("read",
  true)`. `DeleteOldRead` чистит по `WHERE read = ? AND read_at < ?` → `read_at IS NULL` не попадает
  в `read_at < cutoff` (NULL-сравнение) → пакетно прочитанные уведомления накапливаются бессрочно.
- **Фикс**: в `MarkAllAsRead` добавить `"read_at": time.Now()`.

### M3. Team AddMember: ErrAlreadyInOtherTeam → 500 вместо 400 🔍✅ (подтверждено)
- **Файл**: `internal/domain/team/handler.go:337-347`.
- **Проблема**: switch обрабатывает только `ErrUserAlreadyInTeam`/`ErrOnlyCaptainCanAdd` (400), а
  `ErrAlreadyInOtherTeam` (пользователь в другой команде — валидный бизнес-кейс) попадает в `default` → 500.
- **Фикс**: добавить `errors.Is(err, ErrAlreadyInOtherTeam)` в первый case.

### M4. Team InvitationHandler.Create: ErrOnlyCaptainCanInvite → 500 вместо 403 🔍✅ (подтверждено)
- **Файл**: `internal/domain/team/handler.go:630-647`.
- **Проблема**: switch обрабатывает только `ErrUserNotFound`/`ErrUserAlreadyInTeam`/`ErrInvitationExists`,
  а `ErrOnlyCaptainCanInvite` → `default` → 500. Также админ не может приглашать (не проверяется `isAdmin`),
  хотя `InvitationHandler.Index` админа пускает — несогласованность.
- **Фикс**: case для `ErrOnlyCaptainCanInvite` (403) + пропускать админа.

### M5. Tournament Update: пустое имя затирает название 🔍✅ (подтверждено)
- **Файл**: `internal/domain/tournament/service.go:87-102` + handler `UpdateTournamentInput.Name`
  (`binding:"omitempty,min=2,max=200"`).
- **Проблема**: пустая строка пропускает валидацию (`omitempty`), а `service.Update` безусловно
  `t.Name = updated.Name` (строка 95) → турнир с пустым именем.
- **Фикс**: не перезаписывать `Name`, если вход пуст (или убрать `omitempty`).

### M6. Chat: нет составного индекса (room_id, created_at) — сортировка всей истории 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/monitor/model.go:63` + `repository.go:345-353`.
- **Проблема**: `idx_chat_messages_room` только на `room_id`, а `GetMessages`/`GetMessagesBefore` делают
  `ORDER BY created_at DESC LIMIT ?` → для комнат с большой историей (общий чат) PostgreSQL сортирует
  все сообщения комнаты на каждую загрузку истории.
- **Фикс**: составной индекс `(room_id, created_at DESC)` (миграция).

### M7. InvalidateTeamPermCache: O(roomIDs × cacheEntries) 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/monitor/repository.go:596-606`.
- **Проблема**: для каждого `roomID` команды проходит по ВСЕЙ `permCache` с `strings.HasPrefix` →
  квадратичный проход под локом при 10000 записей.
- **Фикс**: ключовать кэш по `roomID` (вложенная map `roomID → userID → entry`) или индекс
  `roomID → userIDs` для O(1)-инвалидации.

### M8. CSV-импорт: ответы вставляются CreateInBatches по одному вопросу 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/export/service.go:448-458`.
- **Проблема**: `pendingAnswers[i]` вставляется отдельным `CreateInBatches(ans, 200)` на КАЖДЫЙ вопрос →
  N батч-вызовов (при 5000 вопросов — 5000 INSERT-вызовов).
- **Фикс**: собрать все ответы в один плоский слайс (с `QuestionID` после вставки вопросов) и вставить
  одним `CreateInBatches`.

### M9. ExportTeamResultsCSV: двойная выборка + полная строка игры ради AuthorID 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/export/handler.go:496,510` + `service.go:188`.
- **Проблема**: `GetFinishedPassingForTeam` (результат отброшен `_`) затем повторно
  `GetPassingByGameAndTeam`; `GetGameByIDUnchecked` грузит полную строку игры ради `AuthorID`.
- **Фикс**: использовать результат первой проверки; `AuthorID` брать лёгким `Select`.

### M10. Единый Session.Secret для 4 механизмов безопасности 🔍✅ (подтверждено, security)
- **Файл**: `internal/app/app.go:110-114` + `internal/pkg/sessionstore/sessionstore.go:255-259`.
- **Проблема**: один `Session.Secret` используется для: подпись session-cookie (`authKey`), шифрование
  (`sha256(Secret+":enc")`), fallback CSRF (`config.go:331-335`) и HMAC trusted-device cookie (обход 2FA
  на 30 дней, `two_factor_middleware.go:40-53`). Компрометация одного секрета даёт подделку
  trusted-cookie (обход 2FA) И подделку CSRF-токенов.
- **Фикс**: отдельные `TRUSTED_DEVICE_SECRET` и обязательный `CSRF_SECRET` (не fallback).

---

## 🟡 LOW (новые)

- **L1**: `auth_handler.go:623` — ForgotPassword логирует `input.Email` (PII в логах, противоречит
  анти-энумерационной политике). Фикс: логировать только `user.ID`.
- **L2**: `/auth/refresh` возвращает `access_token` в теле JSON — читается XSS (httpOnly-кука защищает
  куку, но тело доступно JS).
- **L3**: `oauth_service.go:175-177` — имена из OAuth (Yandex/VK) пишутся в `user.Name` без
  `sanitize.StripHTML` (митигировано автоэкранированием `html/template`).
- **L4**: `middleware/security.go:67-68` — CSP `connect-src 'self' ws: wss:` и `img-src 'self' data:
  https:` слишком широкие.
- **L5**: `recaptcha.go:90-97` — проверяется только `Success`, без `hostname`/`action`.
- **L6**: `oauth_service.go:41` — VK `user_id` через `float64` (потеря точности >2^53; сейчас VK ID меньше).
- **L7**: `notification/service.go:411-419` — `getSubs` возвращает кэш-слайс без копирования.
- **L8**: `notification/repository.go:90-120` — `ListByUser` при `OFFSET` > total возвращает `total=0`.
- **L9**: `payment/service.go:387-399` — `resumePendingPayment` с пустым `IdempotencyKey` (legacy).
- **L10**: `calendar/handler.go:127` — `c.Data(200, ...)` вместо `http.StatusOK`.
- **L11**: `social/repository.go:69-94` — `GetSubscriptions`/`GetFollowers` без фильтра
  `profile_visibility` (не подтверждено — фильтрация в UI).
- **L12**: `team/service.go:340-345` — `ChangeCaptain` не инвалидирует perm-кэш чата (некритично).
- **L13**: `websocket/room_hub.go:429-442` — `dispatchToRoom` берёт полный `Lock` даже без `removed`.
- **L14**: `realtimebus/bus.go:268` — `[]byte(msg.Payload)` копирует строку на каждое сообщение.
- **L15**: `export/handler.go:322,362,402,444` — PDF/xlsx буферизуют в `bytes.Buffer` + `c.Data`
  (двойная память; CSV уже стримит).
- **L16**: `monitor/handler.go:388-392` — `presenceLast` stale-записи для комнат, удалённых
  `cleanupInactiveClients` (без колбэка).
- **L17**: `user/model.go:138` — `VerificationCode size:8`, но значение 12 hex-символов (проверить миграцию).

---

## 🔍 Проверено и закрыто (кандидаты HIGH/MEDIUM, НЕ подтвердились)

| Пункт | Статус | Детали |
|---|---|---|
| **SQL-инъекции (notification/calendar/social/team/tournament/payment)** | ✅ Закрыто | Все динамические SQL — плейсхолдеры (`?`/`pq.Array`/`gorm.Expr`); `BuildLikePattern` экранирует `%`/`_`; CASE-конструкции из констант. |
| **XSS** | ✅ Закрыто | `html/template` автоэкранирование; OAuth-имена риск только в неэкранируемом контексте. |
| **CSRF** | ✅ Закрыто | `gorilla/csrf` (без TrustedOrigins — CVE-2025-47909), `APIOriginGuard` (Origin+Sec-Fetch-Site), SameSite=Strict. |
| **OAuth state** | ✅ Закрыто | `crypto/rand` 128 бит, `subtle.ConstantTimeCompare`, привязка к провайдеру, TTL 10 мин. |
| **WebAuthn** | ✅ Закрыто | challenge в server-side сессии, проверка userHandle, CloneWarning→отказ, sign_count. |
| **JWT** | ✅ Закрыто | HS256, `requireStrongSecret(≥32)`, `SigningMethodHMAC`, iss/aud/nbf/iat, jti-blacklist (Valkey+fallback). |
| **Пароли** | ✅ Закрыто | bcrypt cost 12, dummy-hash, HIBP k-anonymity. |
| **Refresh-токены** | ✅ Закрыто | SHA-256 в БД, семейная ротация, детект reuse, device/fingerprint, атомарный claim. |
| **Session fixation** | ✅ Закрыто | server-side store, RenewToken/Clear, session ID 256 бит. |
| **Timing attacks** | ✅ Закрыто | `ConstantTimeCompare` (OAuth), `hmac.Equal` (trusted cookie), dummy bcrypt. |
| **Rate limiting** | ✅ Закрыто | per-IP + per-account lockout (атомарный инкремент + backoff), fail-closed критические лимитеры. |
| **IDOR** | ✅ Закрыто | `WHERE id=? AND user_id=?`, refresh/сброс/верификация по хешам. |
| **cache/LRU утечки** | ✅ Закрыто | ленивый префикс-индекс, sweep по ttlKeys, DeleteByPrefix батчит DEL. |
| **websocket Acquire** | ✅ Закрыто | атомарно под одним локом (без TOCTOU); cleanupInactiveClients под RLock+батч. |
| **monitor permCache/limiter/rooms** | ✅ Закрыто | TTL 5с, cap 10000, O(n) sweep, удаление при нуле. |
| **SSE poller** | ✅ Закрыто | один сборщик на игру, корректный unsubscribe, FNV-хэш. |
| **dashboard errgroup** | ✅ Закрыто | параллельные запросы, разные поля структуры. |
| **Денежная арифметика** | ✅ Закрыто | целочисленные копейки, проверка переполнения, точная сверка. |
| **Гонки NotificationService/Calendar/TeamService** | ✅ Закрыто | unreadMu/subsMu/pushMu/cacheMu/membersMetricMu. |
| **Транзакции (AcceptInvitation/AddGame/RemoveGame/UpdateScores)** | ✅ Закрыто | rollback полный, атомарные claim'ы (ON CONFLICT DO NOTHING). |
| **N+1 (GetByIDWithMembers/UpdateScoresForGame/RemoveGame/scoreTournament)** | ✅ Закрыто | Preload, batch-upsert, unnest-батч. |
| **Утечки HTTP-тел (webpush/yookassa)** | ✅ Закрыто | `resp.Body.Close()`/`defer`, `io.LimitReader`. |

---

## 💡 Предложения по улучшению (PASS 20)

### Критичные (правильность/безопасность)
1. **H1**: RemoveGame декрементировать games_played только для points>0.
2. **H2**: AuthRequired на POST /auth/2fa/verify и /auth/2fa/backup.
3. **H3**: батч-INSERT в JSON-импорте уровней (как CSV).

### Оптимизация
4. **M6**: составной индекс `(room_id, created_at DESC)` для чата.
5. **M7**: O(1)-инвалидация perm-кэша (вложенная map по roomID).
6. **M8/M9**: один батч ответов в CSV-импорте; убрать двойную выборку в экспорте команды.

### Безопасность/надёжность
7. **M10**: раздельные секреты (TRUSTED_DEVICE_SECRET, CSRF_SECRET).
8. **M1**: атомарный CreateTeam + AddMember.
9. **M2**: read_at в MarkAllAsRead (retention).

### UX/качество
10. **M3/M4/M5**: корректные коды ошибок (400/403 вместо 500), не затирать имя турнира.
11. **L1-L2**: убрать PII из логов, не возвращать access_token в теле /auth/refresh.

---

## 🎯 Итог PASS-20

- **HIGH**: 3 новых (H1 турнирная потеря результатов, H2 2FA step-up сломан, H3 построчный импорт).
- **MEDIUM**: 10 новых (M1-M10: 5 code review + 4 perf + 1 security).
- **LOW**: 17 новых (L1-L17).
- **Закрыто**: 21 кандидат (SQLi/XSS/CSRF/OAuth/WebAuthn/JWT/пароли/refresh/сессии/тайминги/rate-limit/
  IDOR/cache/websocket/monitor/SSE/dashboard/деньги/гонки/транзакции/N+1/HTTP-тела) — без проблем.
- **pprof**: heap 7.3MB, 22 goroutine, CPU ~0.42% (TLS) — утечек нет.
- **Приоритет**: H2 (2FA step-up — блокирует админов), H1 (турнирные результаты), H3 (импорт),
  M10 (секреты), M1 (осиротевшая команда).

---

# DEEP_REVIEW — Gengine-0 (PASS 21, повторное ревью #6)

> Целевое окружение: **pod через podman**. Метод: pprof в pod (heap/goroutine/cpu) + 3
> параллельных аудита (@reviewer — admin/game/user домены, @security — crypto/storage/
> sanitize/validation/csrf/sqlutil/render/errors, @perf — i18n/templatefuncs/metrics/
> sessionstore/rolecache/logging/svc_snapshot/svc_listing/svc_facade) + эмпирическая
> проверка каждой находки по коду.

---

## 🔬 pprof-результаты (PASS 21, в pod)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 22 в покое | ✅ Утечек нет (стабильно с PASS-15; +1 audit worker). |
| **heap inuse** | **7.9 MB** | ⚠️ Стартовые аллокации: runtime.allocm (2.5MB), pgx stmtcache LRU (1MB), regexp (1MB), excelize (0.65MB), bufio (0.5MB). Не hot-path. |
| **cpu (лёгкая нагрузка)** | 0.17% (20ms / 12s) | ✅ **100% TLS handshake (RSA SignPSS, FIPS bigmod)** — прикладной код чист. |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback. |

**Вывод**: утечек нет; heap 7.9MB (инициализация), CPU чист. Профилирование подтверждает
отсутствие горячих точек в прикладном коде.

---

## 🔴 HIGH (новые)

### H1. Шифрование бэкапов падает с panic: NewCTR получает IV 12 байт вместо 16 🔍✅ (подтверждено запуском)
- **Файл**: `internal/domain/admin/service.go:406` (`encryptBackupFile`) и `:461` (`decryptBackupFile`).
- **Проблема**: `nonce := make([]byte, gcm.NonceSize())` = **12 байт** (AES-GCM nonce), затем
  `cipher.NewCTR(block, nonce)` (строка 406/461) требует IV длиной == `block.BlockSize()` = **16 байт**.
  `cipher.NewCTR` паникует `IV length must equal block size` (подтверждено запуском Go-сниппета:
  `gcm.NonceSize()=12`, `BlockSize()=16`).
- **Влияние**: при заданном `BACKUP_ENCRYPTION_KEY` (32 байта) **каждый** бэкап падает:
  - async-путь (`CreateNowAsync`) перехватывает panic через recover, но бэкап НЕ создаётся;
  - plaintext-дамп `backup_*.sql` **остаётся на диске** (`os.Remove(srcPath)` на строке 418
    выполняется только после успешного шифрования) — не виден `RotateBackups`, копится бессрочно;
  - `Download` зашифрованного бэкапа → panic → 500.
- **Фикс**: для CTR — IV 16 байт (`make([]byte, block.BlockSize())`). Но заявлен «AES-256-GCM»
  (см. M1) — правильнее перейти на аутентифицированное шифрование (GCM или HMAC-CTR).

---

## 🟠 MEDIUM (новые)

### M1. Бэкап шифруется голым AES-CTR без MAC, заявлен AES-256-GCM 🔍✅ (подтверждено)
- **Файл**: `internal/domain/admin/service.go:369-420`.
- **Проблема**: создаётся `cipher.NewGCM` (строка 387), но `gcm.Seal`/`gcm.Open` НЕ вызываются —
  используется `cipher.NewCTR` без тега аутентичности. Комментарии/логи заявляют «AES-256-GCM».
  Подмена битов шифротекста (хеши паролей, 2FA-секреты, refresh-хеши в дампе) не детектируется.
- **Фикс**: GCM (не потоковый, OOM на гигабайтах — см. M5 PASS-17) или **HMAC-CTR**
  (Encrypt-then-MAC, потоково + аутентифицировано): nonce(16) || ciphertext || mac(32).

### M2. PhotoService.Delete: рассинхрон проверки прав с хендлером 🔍✅ (подтверждено)
- **Файл**: `internal/domain/game/svc_photo.go:57` vs `hnd_photo.go:295`.
- **Проблема**: хендлер использует `CanEditContent` (учитывает jsonb `Permissions`), а
  `PhotoService.Delete` — `hasCoAuthorRole(RoleContentEditor)` (роль, игнорируя `Permissions`).
  Соавтор `observer` + право `edit_content` пройдёт хендлер, но отклонён сервисом (не эскалация,
  сервис строже — но неконсистентная логика).
- **Фикс**: унифицировать через `HasPermission`/`coAuthorHasPermission`.

### M3. profile_handler: админ не может открыть скрытый профиль (вопреки комментарию) 🔍✅ (подтверждено)
- **Файл**: `internal/domain/user/profile_handler.go:176`.
- **Проблема**: комментарий «скрытый профиль виден и админам», но проверка только
  `currentUserID != userID` → 403 без `IsAdmin(c)`. Админ фактически не может просмотреть скрытый профиль.
- **Фикс**: `if currentUserID != userID && !middleware.IsAdmin(c) { 403 }`.

### M4. Admin dashboard: кэш счётчиков не хитится с Valkey (type-assert к анонимному struct) 🔍✅ (подтверждено)
- **Файл**: `internal/domain/admin/handler.go:126-149`.
- **Проблема**: `h.cacheStore.GetWithCtx` + `v.(struct{...5 полей...})`. Для in-memory — работает;
  для Valkey значение приходит `map[string]any` (JSON-unmarshal) → assertion всегда false → кэш
  мёртв → `COUNT(*)` по растущей `audit_logs` на каждый заход (регресс цели PASS-17 M9).
- **Фикс**: кэшировать через `cacheGetJSON`/именованный тип (аналог cacheGetGame/cacheGetRating).

### M5. sqlutil.AddOrder: whitelist пропускает SQL-ключевые слова 🔍✅ (подтверждено, low-impact)
- **Файл**: `internal/pkg/sqlutil/sqlutil.go:45-57`.
- **Проблема**: побуквенный whitelist `[a-zA-Z0-9._,\s]` пропускает все буквы/пробелы/запятые →
  `id UNION SELECT password FROM users` проходит (нет `()`, `;`, `-`, но UNION с литералами возможен).
  Сейчас `PaginatedQueryBuilder` используется только в тестах (мёртвый код) — эксплуатируемость низкая.
- **Фикс**: enum разрешённых полей (map[string]bool) вместо строкового whitelist.

### M6. errors: утечка внутренних деталей в HTTP-ответ 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/errors/errors.go:155-171,320-324`.
- **Проблема**: `Wrap` подставляет сырой `err.Error()` в `Message`; `JSONResponse` отдаёт `Message`
  и `Details` клиенту (для `lang != "ru"` — сырое `Message`). Ошибка БД/ФС (имена таблиц, пути,
  текст запроса) может утечь в ответ.
- **Фикс**: для `ErrInternal` — фиксированное generic-сообщение клиенту, детали только в лог;
  `Details` не сериализовать для internal-ошибок.

### M7. rolecache: cache-aside без singleflight → thundering herd на TTL-границе 🔍✅ (подтверждено, perf)
- **Файл**: `internal/pkg/rolecache/rolecache.go:63`.
- **Проблема**: на промахе (TTL 5с / после `InvalidateAll`) все конкурентные запросы одного
  `userID` параллельно вызывают `provider()` (SELECT role) и дублируют запись.
- **Фикс**: `singleflight.Group` (как в pkg/cache) или GetOrSet вокруг provider.

### M8. sessionstore: sync.Mutex (не RWMutex) + O(n) sweep в hot path Set 🔍✅ (подтверждено, perf)
- **Файл**: `internal/pkg/sessionstore/sessionstore.go:67,84-91`.
- **Проблема**: `memoryBackend` использует `sync.Mutex`: `Get` на КАЖДЫЙ запрос берёт эксклюзивный
  лок (read сериализуется). Плюс sweep при `len(items) > 10000` в `Set` итерирует ВСЕ записи O(n)
  под локом → блокировка всех session-операций.
- **Фикс**: `RWMutex` для `Get`; sweep в фоновую горутину (не в hot path `Set`).

### M9. svc_listing: кэш-ключ из сырого query → неограниченный рост ключей 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/game/svc_listing.go:349,126`.
- **Проблема**: ключ `"games:autocomplete:" + q` и `filter.Search` в `listingCacheKey` — каждый
  уникальный запрос = новый ключ (Valkey/в памяти). `/api/search/games` не rate-limитится для
  анонимов (`APIRateLimit` пропускает `userID==0`). В пределах TTL растёт неограниченно ключей.
- **Фикс**: хешировать/нормализовать query в ключе; лимит длины/числа; префиксная инвалидация.

### M10. svc_listing: COUNT(*) OVER() window на каждый промах кэша 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/game/svc_listing.go:191`.
- **Проблема**: `COUNT(*) OVER()` считает total по ВСЕМ строкам на каждый промах, включая
  per-viewer авторизованные листинги (комбинаций viewer×page много). Низкий hit-rate → полный скан.
- **Фикс**: отдельный `COUNT(*)` только при промахе, либо кэшировать `total` отдельно.

---

## 🟡 LOW (новые)

- **L1**: `push_handler.go:172` — `net.DefaultResolver.LookupHost` без таймаута (медленный DNS блокирует запрос).
- **L2**: `hnd_settings.go:146-152` — `strconv.Atoi` с `_` — нечисловой ввод молча становится `0`.
- **L3**: `svc_facade.go:24-29` — `GetUserGamesView` type-assert `v.(string)` — под Valkey не хитится.
- **L4**: `svc_play.go:318` vs `:813` — один ключ `game:settings:%d`: значение `GameSetting` vs `*GameSetting` (непоследовательно).
- **L5**: `monitor_repository.go:98` — `LIMIT 500` до группировки по командам (эвристика теряет данные).
- **L6**: `storage/local_storage.go:150-162` — boundary-проверка строковая, нет `EvalSymlinks`/`O_NOFOLLOW`.
- **L7**: `storage/local_storage.go:263-266` — при `baseDir==""` граница в `Delete` не проверяется.
- **L8**: `render/htmlcache.go:108-138` — `TryServeAnonPageCache` с пустым `data` → CSRF-плейсхолдер не подставляется (латентный).
- **L9**: `validation.go:129-141` — `ValidateURL` не ограничивает схему http/https (ftp/gopher проходят).
- **L10**: `errors.go:441-452` — `SanitizeMessageForLog` пропускает `session`/`cookie`/`authorization`.
- **L11**: `sanitize.go:28-30` — rich-ссылки без `rel="noopener noreferrer"` (reverse-tabnabbing).
- **L12**: `svc_snapshot.go:58` — `time.AfterFunc` на каждый Schedule (аллокация таймера в hot path).
- **L13**: `templatefuncs/funcs.go:206` — `initials`: `strings.ToUpper(string(r))` = 2 аллокации (→ `unicode.ToUpper`).
- **L14**: `templatefuncs/funcs.go:87` — `richText` bluemonday-санитизация на каждый рендер (без кэша).
- **L15**: `logging/gorm.go:78-82` — `log.Debug()`-событие на каждый SQL даже при выключенном debug.
- **L16**: `logging/logging.go:25` — `GetCorrelationID` генерирует новый uuid при каждом вызове (не «прилипает»).
- **L17**: `svc_listing.go:316-322` — кэш-запись на каждый промах per-viewer (рост ключей).

---

## 🔍 Проверено и закрыто (кандидаты HIGH/MEDIUM, НЕ подтвердились)

| Пункт | Статус | Детали |
|---|---|---|
| **IDOR Phase-3 (cross-game)** | ✅ Закрыто | `GetTeamRoute`/`AttemptsPerUser` под `GameManager` + сервис проверяет `passing.GameID != gameID`. |
| **Членство в геймплее** | ✅ Закрыто | SubmitCode/UseHint/SubmitFile/AcceptAnswer проверяют `isUserInPassing` до сервиса. |
| **FullPreview утечка ответов** | ✅ Закрыто | тексты/ответы/подсказки только менеджеру, ранняя утечка до старта закрыта. |
| **Соавторы (Add/Remove)** | ✅ Закрыто | требуют владельца (`ErrNotOwner`), супер-админ bypass корректен. |
| **Apply (гонки/лимиты)** | ✅ Закрыто | капитан, IsDraft/visibility, дедлайн, лимит команд, ON CONFLICT. |
| **SQL-инъекции** | ✅ Закрыто | плейсхолдеры; ORDER BY whitelist (svc_listing); LIKE через EscapeLike; CASE — параметризовано. |
| **Транзакции (pg_dump/SubmitCode/UseHint/CalculateResults)** | ✅ Закрыто | rollback корректен, колбэки после коммита. |
| **Гонки кэшей (Profile/Listing/CoAuthor/Monitor)** | ✅ Закрыто | мьютексы/LRU/singleflight; SnapshotDispatcher версионирует таймеры. |
| **Пути бэкапов (path traversal)** | ✅ Закрыто | `isWithinBackupDir` + `filepath.Rel` в Download/RotateBackups. |
| **storage (загрузка)** | ✅ Закрыто | sanitizeFilename, `..`-запрет, whitelist расширений, magic-bytes MIME, io.LimitReader. |
| **csrf** | ✅ Закрыто | gorilla/csrf SameSiteStrictMode, без TrustedOrigins (CVE-2025-47909). |
| **i18n** | ✅ Закрыто | O(1) map, TF fast-path без Sprintf, read-only после init. |
| **metrics кардинальность** | ✅ Закрыто | route=FullPath (не фактический путь), vital/status — перечисления. |
| **sessionstore valkeyBackend** | ✅ Закрыто | typedValue, 2s deadline, fail-open. |
| **индексы листинга** | ✅ Закрыто | idx_games_draft_visibility_created/name/starts. |

---

## 💡 Предложения по улучшению (PASS 21)

### Критичные
1. **H1**: исправить IV для CTR (16 байт) или перейти на GCM/HMAC-CTR — иначе бэкапы не создаются.
2. **M1**: аутентифицированное шифрование бэкапов (HMAC-CTR, потоково).

### Оптимизация
3. **M4**: cacheGetJSON для admin-счётчиков (Valkey-hit).
4. **M7/M8**: singleflight в rolecache; RWMutex + фоновый sweep в sessionstore.
5. **M9/M10**: ограничить рост кэш-ключей листинга; отдельный COUNT.

### Безопасность/надёжность
6. **M5/M6**: enum вместо whitelist в AddOrder; не утекать внутренние детали в ошибках.
7. **M2/M3**: унифицировать права фото; пустить админа в скрытый профиль.

### UX/качество
8. **L1-L4, L12-L17**: таймауты DNS, кэш richText, unicode.ToUpper, guard логов, correlationID в ctx.

---

## 🎯 Итог PASS-21

- **HIGH**: 1 новый (H1 — backup-шифрование panic, CRITICAL для бэкапов).
- **MEDIUM**: 10 новых (M1-M10: шифрование, права, кэши, sessionstore, sqlutil, errors).
- **LOW**: 17 новых (L1-L17).
- **Закрыто**: 15 кандидатов (IDOR/членство/FullPreview/соавторы/Apply/SQLi/транзакции/гонки/
  пути/бэкапы/storage/csrf/i18n/metrics/sessionstore-valkey/индексы) — без проблем.
- **pprof**: heap 7.9MB, 22 goroutine, CPU ~0.17% (TLS) — утечек нет.
- **Приоритет**: H1 (бэкапы не создаются при шифровании), M1 (аутентичность), M4 (кэш админки),
  M7/M8 (sessionstore/rolecache hot path), M9 (рост ключей листинга).

---

# DEEP_REVIEW — Gengine-0 (PASS 22, повторное ревью #7)

> Целевое окружение: **pod через podman**. Метод: pprof в pod (heap/goroutine/cpu) + 3
> параллельных аудита (@reviewer — app/db/team/monitor, @security — middleware/db/realtimebus/
> websocket, @perf — cache/middleware/websocket/level/game) + эмпирическая проверка по коду.
> Примечание: субагенты security/perf из конфига всё ещё возвращают пустые ответы (изменения
> opencode.jsonc не вступили в силу в текущей сессии — кэш агентов на старте), поэтому эти два
> аудита выполнены через general.

---

## 🔬 pprof-результаты (PASS 22, в pod)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 22 в покое | ✅ Утечек нет (стабильно с PASS-15). |
| **heap inuse** | **7.4 MB** | ⚠️ Стартовые: runtime.allocm (2.5MB), excelize (0.65MB), i18n map (0.5MB). Не hot-path. |
| **cpu (лёгкая нагрузка)** | 0.42% (50ms / 12s) | ✅ Доминирует TLS + context.parentCancelCtx (мелочь) — прикладной код чист. |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback. |

**Вывод**: утечек нет; heap 7.4MB (инициализация), CPU чист.

---

## 🔴 HIGH (новые)

### H1. Team SearchPaginated: ambiguous ORDER BY id (JOIN users + Order("id DESC")) → 500 🔍✅ (подтверждено)
- **Файл**: `internal/domain/team/repository.go:194-204`.
- **Проблема**: `SearchPaginated` делает `Joins("LEFT JOIN users ON users.id = teams.captain_id")`
  (строка 200) + `Order("id DESC")` (строка 202). В результирующем SQL колонка `id` есть и в
  `teams`, и в `users` (обе с `gorm.Model`) → PostgreSQL `42702 column reference "id" is ambiguous`
  → поиск/пагинация команд возвращает 500. `ListAllPaginated` (строка 191) корректен только
  потому, что там нет `Joins`.
- **Фикс**: `Order("teams.id DESC")`.

### H2. GameManager: observer (read-only) получает права на запись 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/middleware/game_manager.go:23` → `coauthor_repository.go:39-54`.
- **Проблема**: `GameManager` вызывает `IsUserManager`, который (`repo.IsUserManager`) делает
  `SELECT COUNT(*) FROM (games WHERE author_id=? UNION co_authors WHERE game_id=? AND user_id=?)`
  — **без фильтра по роли**. Любой соавтор, включая `RoleObserver` (read-only), считается
  «менеджером». `GameManager` навешен на мутирующие эндпоинты: `SetTeamRoute`, `SetTeamAnswer`,
  `SetTeamStartTime`, `AttemptsPerUser` (game/routes.go:138-145), `LocationsByGame` (GPS),
  `export`, `monitor`.
- **Вектор**: наблюдатель (приглашённый читать игру) может менять маршруты/ответы/время команд
  и читать live-координаты игроков. Обход авторизации.
- **Фикс**: использовать `HasPermissionRole(ctx, gameID, userID, []string{RoleContentEditor, RoleModerator})`
  (уже есть в репозитории) вместо `IsUserManager`; для каждой группы маршрутов задать требуемую роль.

### H3. gzip.NewWriter без sync.Pool — аллокация flate-компрессора на каждый ответ 🔍✅ (подтверждено, perf)
- **Файл**: `internal/pkg/middleware/gzip.go:99`.
- **Проблема**: `gzip.NewWriter(c.Writer)` создаёт flate-компрессор (~64KB истории/hash-таблиц)
  заново на каждый ответ (HTML + статические JS/CSS). При высоком RPS — непрерывный GC-чурн на
  hot path каждого запроса.
- **Фикс**: `sync.Pool` для `*gzip.Writer` + `gz.Reset(w)` (паттерн gin-contrib/gzip).

---

## 🟠 MEDIUM (новые)

### M1. MigrateFromDir: MkdirAll перед ошибкой → молчаливый старт на немигрированной схеме 🔍✅ (подтверждено)
- **Файл**: `internal/db/migrate.go:153-175`.
- **Проблема**: при неверной CWD первый запуск делает `os.MkdirAll("migrations")` (строка 154),
  затем возвращает ошибку (строка 173). Второй запуск: `os.Stat("migrations")` уже НЕ `IsNotExist`
  → проверка проходит → `m.Up()` применяет 0 миграций → `ErrNoChange` → сервер молча стартует на
  пустой схеме. Комментарий заявляет «пустая папка не создаётся» — фактически создаётся и
  подрывает фикс.
- **Фикс**: не вызывать `MkdirAll` перед возвратом ошибки (сразу возвращать ошибку).

### M2. ChatRoom unique index (GameID, TeamID, PassingID) с NULL не запрещает дубликаты 🔍✅ (подтверждено)
- **Файл**: `internal/domain/monitor/model.go:31-34`.
- **Проблема**: составной unique на трёх nullable `*uint` — в PostgreSQL `NULL != NULL`, поэтому
  general/captains/server/personal-комнаты (все `(game_id, NULL, NULL)`) не дедуплицируются индексом.
  Защита от гонки find-or-create держится только на логике сервиса.
- **Фикс**: `UNIQUE ... NULLS NOT DISTINCT` (PG 15+) или уникальный индекс по `COALESCE`, либо
  re-read-fallback в `GetOrCreate*` (проверить service.go).

### M3. middleware.permissions: параметр requiredRole игнорируется (мёртвый) 🔍✅ (подтверждено, security)
- **Файл**: `internal/pkg/middleware/permissions.go:11,23`.
- **Проблема**: проверяет только `IsUserManager` (любая роль), `requiredRole` не сверяется. Сейчас
  вызывается только в тестах (в production не подключён) — эксплуатации нет, но «middleware для
  проверки роли» даёт доступ любому соавтору.
- **Фикс**: передавать `requiredRole` в `HasPermissionRole`, либо удалить параметр.

### M4. OAuth rate-limiter фактически fail-open (расхождение с заявленным fail-closed) 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/middleware/rate_limiter.go:401` + `cmd/server/main.go:271`.
- **Проблема**: `InitOAuthRateLimiterWithValkeyFailClosed` записывает fail-closed-инстанс в
  глобальный `oauthRateLimiter`, но `OAuthRateLimit()` его не использует — создаёт свой
  `newSharedLimiter` (fail-open). Глобальные `oauthRateLimiter`/`InitOAuthRateLimiter*` — мёртвый код.
- **Фикс**: использовать fail-closed-инстанс в `OAuthRateLimit`, либо убрать мёртвый код и задокументировать.

### M5. Обход rate-limit через подделку X-Forwarded-For 🔍✅ (подтверждено, security)
- **Файл**: `internal/pkg/middleware/rate_limiter.go:345,384,556,701` + `router.go:300-316`.
- **Проблема**: критичные лимитеры (login/register/OAuth/reset) ключуются по `c.ClientIP()`. Если
  `TRUSTED_PROXIES` задан слишком широким диапазоном (например `0.0.0.0/0`), атакующий ротирует
  `X-Forwarded-For` и обходит per-IP бюджеты брутфорса. `SetTrustedProxies` принимает произвольные
  CIDR без валидации.
- **Фикс**: валидировать/ограничить доверенные CIDR; дополнить per-account-лимитом (по email) на login.

### M6. Pub/sub канал не аутентифицирован: доступ к Valkey = чтение/инжект всех комнат 🔍✅ (подтверждено, security)
- **Файл**: `internal/pkg/websocket/room_hub_pubsub.go:80-128` + `internal/pkg/realtimebus/bus.go:27-30`.
- **Проблема**: единый канал `gengine:ws`/`gengine:sse` переносит base64-сообщения ВСЕХ комнат;
  подписка без авторизации, сообщения без HMAC. `handleRemoteBroadcast` слепо доверяет `msg.Room`/
  `msg.Data`. Процесс с доступом к Valkey (утечка кредов, SSRF) читает чаты/мониторы и инжектит
  сообщения в существующие комнаты.
- **Фикс**: отдельный Valkey-кред/ACL для шины, HMAC-подпись с instance-ключом, per-room каналы.

### M7. SetTeamRoute: N+1 INSERT (по одному в цикле) 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/game/repository.go:630-635`.
- **Проблема**: маршрут команды вставляется `tx.Create(&row)` на каждый levelID в цикле → N round-trip.
- **Фикс**: собрать `[]GamePassingLevel` и один `tx.Create(&rows)`.

### M8. Level Duplicate: ответы по одному INSERT на вопрос 🔍✅ (подтверждено, perf)
- **Файл**: `internal/domain/level/service.go:226-240`.
- **Проблема**: вопросы вставляются батчем, но ответы — `tx.Create` на каждый вопрос → Q INSERT.
- **Фикс**: накопить все ответы в один слайс и один `tx.Create`.

### M9. BroadcastToRoom: блокирующий Valkey Publish (до 2с) до локальной рассылки 🔍✅ (подтверждено, perf)
- **Файл**: `internal/pkg/websocket/room_hub.go:534` + `room_hub_pubsub.go:107-128`.
- **Проблема**: метод заявлен «неблокирующий», но `publishToBus` делает блокирующий `Publish` с
  таймаутом 2с на каждый чат-месседж — при деградации Valkey каждый обработчик чата зависает до 2с.
- **Фикс**: publish в асинхронной горутине (fire-and-forget), либо `[]byte` полем в wsBusMsg (json сам base64-кодирует, убирая ручной encode/decode).

---

## 🟡 LOW (новые)

- **L1**: `db.go:31-36` — DSN через `fmt.Sprintf(password=%s)` — пароль с пробелом/`'` ломает строку. → `url.QueryEscape`.
- **L2**: `db.go:75-102` — EnsureAdmin Count→Create без транзакции (гонка двух инстансов).
- **L3**: `team/repository.go:206-221` — AddMember повторное добавление в ту же команду возвращает `ErrAlreadyInOtherTeam` (вводит в заблуждение).
- **L4**: `team/user_search_handler.go:38` — `teamID, _ := strconv.Atoi(...)` глотает ошибку → `team_id=0` для админа.
- **L5**: `team/chat_handler.go:48-51` — любая ошибка GetTeamWithMembers → 404 (сбой БД тоже 404, а не 500).
- **L6**: `app/router.go:227-229` — `payload.Page[:128]` байтовое обрезание (рвёт UTF-8) + `Page` нигде не используется (мёртвый код).
- **L7**: `app/app.go:115-120` — CSRF-skip через `HasPrefix` (/api → /api-*, /ws → /ws-*).
- **L8**: `db/migrate.go:62` — cleanupGameSettings `MIN(id)` без учёта `deleted_at`.
- **L9**: `rate_limiter.go:479` — `if userID == 0 { userID = 0 }` no-op.
- **L10**: `rate_limiter.go:294-298` — Retry-After/X-RateLimit-Reset по Go-часам, не по TTL Valkey.
- **L11**: `security.go:27-30` — getLeafletHash возвращает захардкоженный const (при обновлении leaflet CSP сломается).
- **L12**: `security.go:83` — HSTS по X-Forwarded-Proto (хрупко).
- **L13**: `logger.go:24-37` — maskQuery не маскирует path/заголовки.
- **L14**: `cache/cache.go:261-266` — первый DeleteByPrefix строит префикс-индекс O(n) под Lock.
- **L15**: `logger.go:54` — maskQuery вызывается даже для /static (аллокации впустую).
- **L16**: `cache/valkey.go:88-92` — Get делает json.Unmarshal в `any` (теряет типы, аллоцирует).
- **L17**: `game/repository.go:589-600` — ListByGamePaginated: Count + Find (2 запроса) vs COUNT OVER (1).
- **L18**: `room_hub.go:138` — GetHealthStatus берёт Lock вместо RLock.
- **L19**: `logger.go:86-92` — c.FullPath() 4 раза (сохранить в переменную).

---

## 🔍 Проверено и закрыто (кандидаты HIGH/MEDIUM, НЕ подтвердились)

| Пункт | Статус | Детали |
|---|---|---|
| **uploads.go path traversal** | ✅ Закрыто | `..` до Clean, IsAbs, Join внутри uploadsDir; covers/photos/answers по видимости + параметризовано. |
| **migrate advisory lock** | ✅ Закрыто | `pg_advisory_lock` на отдельном Conn, таймаут 10 мин, гарантированный unlock. |
| **monitor poller блокировки** | ✅ Закрыто | monitorPollersMu + subMu — порядок непротиворечив, snapFn вне мьютекса. |
| **ChatWS read-loop** | ✅ Закрыто | горутина + readCh, прерывание через SetReadDeadline, утечек нет. |
| **wsMessageLimiter** | ✅ Закрыто | limiter.mu не под userMsgMu, sweep по lastUsed. |
| **router CORS/metrics/pprof** | ✅ Закрыто | AllowAllOrigins согласован с AllowCredentials; /metrics, /debug/pprof за AuthRequired+2FA+Admin. |
| **wire_providers DI** | ✅ Закрыто | wrapRefreshTokenService безопасен (wire гарантирует порядок). |
| **auth middleware** | ✅ Закрыто | роль перечитывается из БД, ошибки не кэшируются, OptionalAuth fail-closed. |
| **CSRF/Origin** | ✅ Закрыто | gorilla/csrf SameSiteStrict, APIOriginGuard (Sec-Fetch-Site + Origin==Host). |
| **WebSocket origin** | ✅ Закрыто | deny пустой Origin, точное сравнение host, авторизация ДО Upgrade. |
| **bodylimit/gzip** | ✅ Закрыто | MaxBytesReader; gzip только ответов (запросы не декомпрессируются → gzip-bomb неприменим). |
| **EnsureAdmin** | ✅ Закрыто | bcrypt cost 12, параметризовано, без перезаписи админа. |
| **индексы** | ✅ Закрыто | search_vector GIN, name trgm, is_draft+visibility+starts, team_members(user_id), logs(game_id,created_at), game_passings(team_id,status). |
| **cache LRU** | ✅ Закрыто | ttlKeys-sweep, ленивый префикс-индекс, singleflight, cleanup в evictCallback. |
| **Valkey DeleteByPrefix** | ✅ Закрыто | батчит Del по 100 ключей. |
| **WebSocket per-room** | ✅ Закрыто | очереди/воркеры, кэш roomClients, двухфазный cleanup, idle-выход воркера. |
| **Level Move/Duplicate** | ✅ Закрыто | advisory lock + FOR UPDATE, ExistsByPosition через EXISTS. |
| **GetLogsByGameID** | ✅ Закрыто | LIMIT 500 + reverse, COUNT(*) OVER для пагинации. |

---

## 💡 Предложения по улучшению (PASS 22)

### Критичные
1. **H2**: GameManager → HasPermissionRole (observer не должен писать).
2. **H1**: `Order("teams.id DESC")` в SearchPaginated.

### Оптимизация
3. **H3**: sync.Pool для gzip.Writer.
4. **M7/M8**: батч-INSERT в SetTeamRoute и Level.Duplicate.
5. **M9**: асинхронный Valkey Publish в BroadcastToRoom.

### Безопасность/надёжность
6. **M6**: аутентификация pub/sub (HMAC/per-room каналы).
7. **M5**: валидация TRUSTED_PROXIES + per-account лимит.
8. **M1**: убрать MkdirAll перед ошибкой миграций.
9. **M3/M4**: починить/убрать мёртвый requiredRole и OAuth fail-closed.

### UX/качество
10. **M2**: NULLS NOT DISTINCT для chat_rooms (защита от дубликатов).
11. **L1-L5, L13**: корректные DSN/коды ошибок/маскирование логов.

---

## 🎯 Итог PASS-22

- **HIGH**: 3 новых (H1 ambiguous ORDER BY, H2 observer-обход авторизации, H3 gzip без пула).
- **MEDIUM**: 9 новых (M1-M9: миграции, индексы, права, rate-limit, pub/sub, N+1).
- **LOW**: 19 новых (L1-L19).
- **Закрыто**: 17 кандидатов (uploads/миграции-lock/поллер/WS-read-loop/лимитер/CORS/DI/auth/CSRF/
  origin/bodylimit/EnsureAdmin/индексы/cache/Valkey/WebSocket/level/logs) — без проблем.
- **pprof**: heap 7.4MB, 22 goroutine, CPU ~0.42% (TLS) — утечек нет.
- **Приоритет**: H2 (observer-обход — реальная дыра авторизации), H1 (500 при поиске команд),
  H3 (gzip), M6 (pub/sub), M5 (rate-limit).

---

## ✅ Исправления PASS-22 (коммит `fix(PASS-22)`)

| Пункт | Статус | Что сделано |
|---|---|---|
| **H1** SearchPaginated ambiguous ORDER BY | ✅ | `Order("teams.id DESC")` в `team/repository.go` (JOIN с users). |
| **H2** GameManager observer-обход | ✅ | `GameAuthorizer.CanManageContent` (интерфейс + `CoAuthorService.HasPermission(RoleContentEditor)`); `game_manager.go` использует его; stub'ы обновлены. |
| **H3** gzip без sync.Pool | ✅ | `gzipWriterPool` + `gz.Reset` + `Put` в defer; type-assert с ok-проверкой (errcheck). |
| **M1** MigrateFromDir MkdirAll | ✅ | Убран `os.MkdirAll` перед возвратом ошибки «папка миграций не найдена». |
| **M2** ChatRoom unique index | ✅ | Уже закрыто: частичные unique-индексы 000064/000067 (game_general, team_general, personal, captains, team_flood). Server-комната без unique — редкая гонка, оставлено. |
| **M3** permissions requiredRole | ✅ | `GameAuthorizer.HasPermission(ctx, gameID, userID, role)`; `RequirePermission` больше не игнорирует requiredRole. |
| **M4** OAuth rate-limiter fail-open | ✅ | `OAuthRateLimit` использует глобальный `oauthRateLimiter` (fail-closed при Valkey); fallback-лимитер создаётся один раз при регистрации middleware. |
| **M5** TRUSTED_PROXIES | ✅ | `config.go`: `0.0.0.0/0` и `::/0` запрещены (strict — ошибка, non-strict — игнор с Warn). |
| **M6** Pub/sub аутентификация | ✅ | HMAC-SHA256 подпись `wsBusMsg`/`sseBusMsg` (encrypt-then-MAC); ключ — `cfg.Session.Secret`; невалидные сообщения отбрасываются. |
| **M7** SetTeamRoute N+1 | ✅ | Один батч-INSERT `tx.Create(&rows)`. |
| **M8** Level Duplicate N+1 | ✅ | Один батч всех ответов `tx.Create(&allAnswers)`. |
| **M9** BroadcastToRoom блокирующий Publish | ✅ | `publishToBus` асинхронный (горутина + семафор `publishSem` 32); недоступный Valkey не задерживает локальную рассылку. |
| **L1** DSN password | ✅ | `db.go`: DSN через `url.URL` + `url.UserPassword` (процентное кодирование). |
| **L3** AddMember ErrAlreadyInOtherTeam | ✅ | `repository.go`: при `RowsAffected==0` уточняем — `ErrUserAlreadyInTeam` если уже в этой команде. |
| **L6** Page[:128] UTF-8 | ✅ | `router.go`: обрезка до валидной руны (`utf8.ValidString`). |
| **L9** no-op userID=0 | ✅ | Убран мёртвый `if userID == 0 { userID = 0 }`. |
| **L11** leaflet hash | ✅ | `security.go`: SRI-хэши вычисляются из фактических файлов при старте (fallback на хардкод); `SecurityHeadersMiddleware(forceHSTS, staticDir)`. |
| **L13** maskQuery path | ✅ | `logger.go`: `maskPath` маскирует чувствительные сегменты path. |
| **L15** maskQuery для /static | ✅ | `logger.go`: maskQuery не вызывается для /static, /uploads, favicon. |

### PASS-21 (доделаны в этом цикле)

| Пункт | Статус | Что сделано |
|---|---|---|
| **H1** backup-шифрование panic | ✅ | `admin/service.go`: IV = `block.BlockSize()` (16 байт, не gcm.NonceSize 12) — `cipher.NewCTR` больше не паникует; на Windows файл закрывается до Remove. |
| **M1** AES-CTR без MAC | ✅ | Encrypt-then-MAC: формат `IV(16) || ciphertext || HMAC-SHA256(32)`; decrypt проверяет HMAC до записи plaintext (два потоковых прохода, O(1) память). |
| **Тесты** | ✅ | `encrypt_test.go`: round-trip + tampered-HMAC (ловил бы panic H1). |

**Верификация**: `go build ./...`, `go vet ./...`, `golangci-lint run ./...` — 0 issues; `go test -short -race -p 1 ./...` — все пакеты OK.

### E2E-флейк (коммит `test(e2e)`)

| Пункт | Статус | Что сделано |
|---|---|---|
| **two-users.spec.ts:121** флейк | ✅ | `createReconnectingWebSocket.send()` молча теряет сообщение при `readyState !== OPEN`. После принятия личного чата страница перезагружается, и тест кликал send до переподключения WebSocket → первое сообщение дропалось. Добавлен хелпер `waitForChatConnected()` (ждёт `#connection-status` = «Подключено»/«Connected») и вызовы перед send в `two-users.spec.ts` (тесты 63 и 121). E2E: **19 passed** (48s).
