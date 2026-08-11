# DEEP_REVIEW — Gengine-0 (PASS 4)

> Глубокое ревью после закрытия PASS-3 (все HIGH/MEDIUM/LOW + IDEA-1..13).
> Метод: pprof-профилирование (goroutine/heap/cpu на :6060) + 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая проверка кода и документации вендора.
> ✅ = исправлено в этом проходе. 🔍 = подтверждено эмпирически.
> Архивы: `DEEP_REVIEW_2026-08-11_pass{1,2,3}.md`.

---

## 🔬 pprof-результаты (PASS 4)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 20 в покое (после фиксов PASS-3) | ✅ Без утечек: стабильно после добавления presence/rolecache/perm-cache. |
| **heap** | 22.6 MB | Норма; топ — swaggo/webdav (swagger), шаблоны. |
| **cpu** (нагрузка 12с) | 66.7% `runtime.cgocall` + 30.8% `bytes.Buffer.WriteTo` (HTTP-ответы) | cgocall = системные вызовы БД/poll в покое — норм. Горячих «чистых» функций нет — кэши работают. |

**Вывод pprof**: профиль чистый; утечек нет; предыдущие оптимизации (кэши, light-граф, фоновая работа) подтверждаются отсутствием пиков.

---

## 🔴 HIGH

### H1. Webhook ЮKassa: обязательный Basic-Auth ломает ВСЕ легитимные вебхуки (регрессия PASS-3 M1) 🔍✅
- **Файл**: `internal/domain/payment/service.go` (`verifyWebhookAuth` вызывается безусловно) + `handler.go:121-155`.
- **Проблема**: фикс M1 (PASS-3) сделал `Authorization: Basic ShopID:WebhookKey` **обязательным**. По официальной документации ЮKassa (yookassa.ru/developers/using-api/webhooks → «Notification authentication») легитимный вебхук аутентифицируется **только по IP-адресам** и проверке статуса объекта через API — **ЮKassa НЕ отправляет Basic-заголовок в вебхуках**.
- **Эффект**: пользователь платит → вебхук приходит с IP ЮKassa, но без Basic → 401 → платежи навсегда остаются `pending`, уведомления не шлются.
- **Фикс**: ✅ сделать Basic-проверку **опциональной** (если заголовок есть — проверить; если нет — доверять IP-allowlist + API-подтверждению). Основная защита: IP-allowlist + `yooKassaGetPayment` + `verifyRemoteAmount`.

### H2. PPROF-сервер слушает 0.0.0.0 без аутентификации 🔍✅
- **Файл**: `cmd/server/main.go:498-512` (`Addr: ":" + PprofPort`), `internal/config/config.go`.
- **Проблема**: при `PPROF_ENABLED=true` pprof биндится на все интерфейсы без auth — на публичном хосте любой может снять heap/cpu профили (секреты в памяти: JWT, cookie-ключи, WebAuthn, ЮKassa).
- **Фикс**: ✅ привязка к `127.0.0.1` по умолчанию (опция `PPROF_BIND` для внутренней сети) + предупреждение в логах.

### H3. `ExportResultsExcel` пропускает requireModerate — observer экспортирует данные всех команд 🔍
- **Файл**: `internal/domain/export/handler.go:432-453`.
- **Проблема**: CSV (287) и PDF (363) требуют `requireModerate`, а Excel — только `checkGameAccess` (IsUserManager, пропускает observer). Тот же класс, что закрыт в PASS-2 #3 — один из 4 «экспорт всех команд» остался открытым.
- **Фикс**: добавить `requireModerate` в `ExportResultsExcel`.

### H4. Капитан не может экспортировать результаты своей команды (мёртвый код) 🔍
- **Файл**: `internal/domain/export/routes.go:38` (`/teams/:team_id/export-results` под `gameManager`) + `handler.go:489-529` (хендлер разрешает капитану, но middleware режет 403).
- **Проблема**: комментарий UX-1/pass 36 явно «доступ должен быть у капитана», но маршрут весит на `gameManager` → логика `isCaptain` в хендлере недостижима.
- **Фикс**: убрать `gameManager` с этого маршрута (оставить AuthRequired), проверку прав оставить в хендлере.

### H5. `CreateBackup` по-прежнему синхронный (фикс PASS-2 #8 неполный) + нет random-суффикса 🔍
- **Файл**: `internal/domain/admin/handler.go:787-794` (вызывает `CreateNow` синхронно), `service.go:56-69` (`filename := "backup_"+timestamp` — комментарий обещает «случайный суффикс», которого нет).
- **Проблема**: HTTP-ответ админу ждёт pg_dump (до 10 мин); два клика в одну ms перезаписывают файл.
- **Фикс**: фоновый запуск + `randomHex(4)` в имени файла.

---

## 🟠 MEDIUM

### M1. CSV round-trip ломает ответы с формульными символами 🔍
- **Файл**: `internal/domain/export/service.go:95` (экспорт `csvSafe`) + `:277-291` (импорт без снятия `'`).
- **Проблема**: `=42` → экспорт `'=42` → импорт сохраняет `'=42` → игра не принимает правильный ответ.
- **Фикс**: при импорте снимать ведущий `'` только если это экранирование csvSafe.

### M2. Импорт CSV без лимита записей/валидации позиций 🔍
- **Файл**: `internal/domain/export/service.go:203-296`.
- **Проблема**: нет cap на строки (только 10MB body), `pos, _ := strconv.Atoi(...)` без диапазона, всё в одной транзакции.
- **Фикс**: cap 5000 строк, `pos` 1..MaxLevelsPerGame, батч-INSERT.

### M3. NaN-сумма проходит проверку → отрицательные копейки в БД 🔍
- **Файл**: `internal/domain/payment/handler.go:78-111`.
- **Проблема**: `strconv.ParseFloat("NaN")` без ошибки; сравнения `amount < 50`/`> 100000` с NaN ложны → `rublesToKopecks(NaN)` = `math.MinInt64` → отрицательная запись + сломанный `kopecksToRublesString`.
- **Фикс**: `math.IsNaN/IsInf` → reject; rate-limit на `/payments/create`.

### M4. Имя комнаты чата без санитизации (deferred stored-XSS) 🔍
- **Файл**: `internal/domain/monitor/handler.go:1128-1147` (CreateRoom).
- **Проблема**: `strings.TrimSpace(c.PostForm("name"))` без StripHTML/лимита длины. Сейчас рендерится textContent (безопасно), но JSON `GameRooms` возвращает сырое имя — любой будущий innerHTML-потребитель получит XSS.
- **Фикс**: `sanitize.StripHTML` + `ValidateString(1,100)` + rate-limit.

### M5. Личный чат без согласия (спам-вектор) 🔍
- **Файл**: `internal/domain/monitor/handler.go:1159-1178` + `routes.go:123`.
- **Проблема**: любой аутентифицированный может открыть чат с любым user_id; `GetOrCreatePersonalRoom` без проверки взаимной подписки.
- **Фикс**: требовать обоюдную follow/подтверждение; rate-limit.

### M6. `games-view` без allowlist (произвольное значение) 🔍
- **Файл**: `internal/domain/user/routes.go:170-185` + `profile_service.go:78-80`.
- **Проблема**: `PUT /api/users/preferences/games-view` сохраняет любую строку → self-DoS (500) при >10 символов.
- **Фикс**: allowlist `{"table","cards"}`.

### M7. `sendWebSocketNotification` использует request-ctx для unread-count 🔍
- **Файл**: `internal/domain/notification/service.go:404-424`.
- **Проблема**: при отмене запроса `getUnreadCount(ctx)` вернёт 0 и закэширует его на 30с.
- **Фикс**: `context.WithoutCancel(ctx)` в WS-пути.

### M8. `permCache` чата растёт без sweep (медленная утечка) 🔍
- **Файл**: `internal/domain/monitor/repository.go:72-93, 356-377`.
- **Проблема**: запись вставляется на каждый промах, удаляется только точечно; истёкшие не вычищаются.
- **Фикс**: lazy sweep при `len > N` (как в rolecache/unreadCache).

### M9. `.ics`-кэш не учитывает Host 🔍
- **Файл**: `internal/domain/calendar/handler.go:187, 240-246`.
- **Проблема**: ключ `"ics"` один, но тело содержит `URL: {host}/games/{id}` — на multi-domain первый запрос кэширует чужой host.
- **Фикс**: включить host в ключ кэша.

### M10. `IsTeamMember` (голосование) не включает капитана 🔍
- **Файл**: `internal/domain/monitor/repository.go:528-534`.
- **Проблема**: капитан, не входящий в team_members, не может голосовать (в чате это учтено — `IsTeamMemberOrCaptain`).
- **Фикс**: `OR teams.captain_id = ?` в blackbox-проверке.

### M11. `PublicProfileStats` — некэшируемые коррелированные COUNT 🔍
- **Файл**: `internal/domain/user/profile_repository.go:23-43`.
- **Проблема**: 4 подзапроса (2 тяжёлых JOIN) на каждом просмотре публичного профиля.
- **Фикс**: кэш 30-60с по userID + инвалидация при финише игры.

---

## 🟡 LOW

| # | Файл | Проблема |
|---|------|----------|
| L1 | `admin/handler.go:448` | `DeleteUser` вызывает `RevokeAllForUser` без nil-проверки (в ToggleAdmin есть). |
| L2 | `admin/handler.go:402,452` | `auditService.Log` без nil-проверки. |
| L3 | `admin/handler.go:260-271` | Password только `len<8` — нет верхней границы; bcrypt обрезает 72 байта. |
| L4 | `export/service.go:433-435` | `FinishedAt.Sub(StartedAt)` при нулевом StartedAt → гигантская длительность. |
| L5 | `calendar/handler.go:162-170` | Eviction удаляет только истёкшие при >512 — при свежих map растёт. |
| L6 | `export/handler.go:495-498` | Капитан не может выгрузить результаты до финиша игры (status=finished). |
| L7 | `notification/settings_handler.go` | Graceful-degradation: при отключённом JS нельзя выключить чекбокс (нет hidden). |
| L8 | `social/handler.go:69-73` | `Follow` мапит любую ошибку GetByID в 404 (маскирует 5xx). |
| L9 | `export/handler.go:522-524` | `IsUserManager` при ошибке трактуется как «не менеджер» (403 вместо 500). |
| L10 | `export/handler.go` | Хендлеры буферизуют весь файл в bytes.Buffer (пик ×2). |
| L11 | `security.go:67-68` | `connect-src ws: wss:` + `img-src https:` ослабляют CSP (эксфильтрация при XSS). |
| L12 | `monitor/handler.go:317-335` | presence рассылает user_ids всем (раскрытие состава). |

---

## ⚡ Оптимизации (perf-аудит)

### HIGH
| # | Файл | Оптимизация | Риск |
|---|------|-------------|------|
| P1 | `profile_repository.go:23-43` | Кэш статистики профиля (см. M11). | Низкий. |
| P2 | `monitor/repository.go` | Sweep для permCache (см. M8). | Низкий. |

### MEDIUM
| # | Файл | Оптимизация |
|---|------|-------------|
| P3 | `calendar/handler.go:109-171` | singleflight для месяца (как MonitorService.sfGroup). |
| P4 | `export/service.go:218-295` | `CreateInBatches` вместо построчных INSERT. |
| P5 | `export/repository.go:37` | Preload("Author") Select id/name (без password_hash). |
| P6 | `calendar/handler.go:232-246` | `sb.WriteString`+strconv вместо fmt.Fprintf в цикле. |

### LOW
| # | Файл | Оптимизация |
|---|------|-------------|
| P7 | `game/svc_monitor.go:137-144` | `copyTeamProgress` — поверхностная копия (задокументировать). |
| P8 | `monitor/repository.go:219-229` | `SaveMessage` = Create + GetMessageByID (2 запроса). |
| P9 | `templatefuncs/funcs.go:71-73` | `richText` bluemonday на каждый рендер (ок, если не в списках). |
| P10 | `game/export/repository.go` | Индекс `teams(captain_id)` — **DDL, только задокументировать**. |

---

## 🚀 Улучшения проекта (код + UX)

### Кодовая база
1. **Верификация вебхука**: сделать Basic-проверку опциональной (H1) — вернуть работоспособность платежей.
2. **pprof** привязка к loopback (H2) + warn.
3. **Export**: requireModerate в Excel (H3), фикс маршрута капитана (H4), strip `'` при импорте (M1), лимиты импорта (M2).
4. **Backup**: фоновый запуск + random-суффикс (H5).
5. **Платежи**: NaN/Inf reject + rate-limit (M3).
6. **Chat**: санитизация комнат (M4), обоюдное согласие на личный чат (M5).
7. **Allowlist** для games-view (M6), **WithoutCancel** для WS-unread (M7).
8. **Индексы**: `teams(captain_id)` (документировано как DDL).

### Пользовательский опыт
| Идея | Эффект |
|------|--------|
| **Капитан видит результаты команды** (H4) | Закрывает обещание UX-1 |
| **Статус создания бекапа** (прогресс/уведомление) | Админ не ждёт 10 мин на странице |
| **Подтверждение личного чата** (accept/block) | Контроль входящих сообщений |
| **Счётчик непрочитанных не «залипает» на 0** (M7) | Корректный badge после отмены запроса |
| **Ограничение имён комнат** (M4) | Чистота списка комнат |
| **Кэш статистики профиля** (M11) | Быстрее открытие профилей |

---

## 📊 Приоритеты исправления

1. ✅ **H1** — вебхук ЮKassa (регрессия платежей!) — Basic сделан опциональным (IP+API-подтверждение).
2. ✅ **H2** — pprof на loopback (PPROF_BIND).
3. ✅ **H3** — requireModerate в ExportResultsExcel.
4. ✅ **H4** — маршрут капитана (убрать gameManager).
5. ✅ **H5** — фоновый CreateBackup + random-суффикс + backupMu.
6. ✅ **M1** — strip `'` при импорте CSV.
7. ✅ **M3** — NaN/Inf + reject на payments/create.
8. ✅ **M4** — санитизация комнат.
9. ✅ **M7** — WithoutCancel для unread.
10. ✅ **M8** — sweep permCache.
11. ✅ **M10** — капитан в голосовании.
12. ⏳ **M2** (лимиты импорта CSV), **M5** (личный чат — дизайн), **M9** (ics cache host), **M11** (кэш статистики профиля), L-пункты, P3-P10.

---

*Дата: 2026-08-11. Метод: pprof (goroutine/heap/cpu на :6060) + @reviewer + @security + @perf + эмпирические проверки кода и документации ЮKassa. Архив PASS-3: `DEEP_REVIEW_2026-08-11_pass3.md`.*
