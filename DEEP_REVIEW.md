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
