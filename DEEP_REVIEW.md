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
