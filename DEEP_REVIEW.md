# DEEP_REVIEW — Gengine-0 (PASS 2)

> Повторное глубокое ревью после исправления PASS-1.
> Метод: pprof-профилирование под нагрузкой + 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая проверка критических подозрений HTTP-запросами.
> ✅ = исправлено в этом проходе.

---

## 🔬 pprof-результаты (после фиксов PASS-1)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 21 горутина в покое | ✅ **0 утечек redis/SSE** (в PASS-1 было 4 висящих redis-tryDial). Фикс `client.Close()` подтверждён. |
| **heap** | 8.8 MB | Нормально для загруженного сервера |
| **cpu** (5 сек, 25 регистраций) | 93.5% `blowfish.encryptBlock` | **bcrypt cost 12** — осознанная защита от брутфорса, НЕ баг. Подтверждён корректный scope (только login/register). |
| **gzip** | flate в top | ✅ gzip для статики работает (после фикса, см. ниже) |

---

## 🔴 Критические (подтверждены эмпирически)

### 1. Регрессия PASS-1: gzip ломал отдачу статики JS/CSS ✅ ИСПРАВЛЕНО

- **Файл**: `internal/pkg/middleware/gzip.go:15-28`
- **Проблема**: PASS-1 включил gzip для `/static/`, но `gzipResponseWriter` не переопределял `WriteHeader`. Go's `http.ServeContent` (через `r.Static`) записывал `Content-Length` **несжатого** файла до `WriteHeader`; gzip-запись давала меньше байт → Go-сервер обрывал соединение на недописанный Content-Length.
- **Эмпирически подтверждено**: `GET /static/js/app.js` → `TypeError: terminated` (клиент получал 7938 из ~37000 байт, соединение обрывалось).
- **Фикс**: переопределён `WriteHeader` — ставит `Content-Encoding: gzip`, удаляет `Content-Length` до вызова базового. Повторная проверка: **200, content-encoding: gzip, полный JS (21872 байта распаковано)**.
- **Регрессионный риск**: без фикса браузеры получали бы битый JS/CSS на всех страницах.

### 2. Мультитурнирный RemoveGame списывал неверные очки ✅ ИСПРАВЛЕНО

- **Файл**: `internal/domain/tournament/service.go:177-205`
- **Проблема**: `RemoveGame` списывал `game_passings.tournament_points` — **общую колонку**, которая при 2+ турнирах перезаписывается последним начисленным турниром. Удаляя турнир A (начислено 10), списывалось 20 (значение турнира B, скоринг которого прошёл позже).
- **Фикс**: единый helper `pointsForPlace(t, p)` — пересчёт по правилам удаляемого турнира (место → points_for_X), используется и в начислении, и в списании.
- Тесты `TestTournamentService_RemoveGame` и мультитурнирный зелёные.

---

## 🟠 HIGH (из аудитов)

### 3. Observer-соавтор может менять контент и читать чужие данные (системный пробел прав)

- **Файлы**: `internal/domain/game/routes.go:135-142`, `internal/domain/export/routes.go:49-55`, `internal/domain/export/handler.go:156-158`
- **Проблема**: Phase-3 эндпоинты (route/start-time/answer/attempts) и CSV-импорт защищены только `IsUserManager`, который пропускает роль **observer** (read-only пресет). Observer может:
  - `SetTeamAnswer`/`SetTeamRoute`/`SetTeamStartTime` для любой команды (порча игрового процесса);
  - импортировать CSV, перезаписывающий вопросы/ответы опубликованной игры;
  - экспортировать `ExportResultsCSV`/`ExportStatisticsPDF` со всеми командами.
- **Фикс**: в этих хендлерах проверять гранулярные права (`HasPermission(RoleContentEditor)` для записи, `RoleModerator` для экспорта всех команд), по образцу `UploadPhoto` (проверяет `RoleUploadMedia`).

### 4. WebAuthn: userVerification=preferred + игнорирование clone-warning

- **Файл**: `internal/domain/user/webauthn_handler.go:81-83,407-409`
- **Проблема**: passkey-login без локальной верификации устройства (PIN/биометрия) — доступ при разблокированном устройстве; `CloneWarning` (признак клонированного аутентификатора) только логируется, JWT всё равно выдаётся.
- **Фикс**: `VerificationRequired` + отказ (403) при CloneWarning, пометка credential как скомпрометированной.

### 5. WebAuthn: нет rate-limit на публичный login/begin-finish

- **Файл**: `internal/domain/user/routes.go:96-97`
- **Проблема**: `/auth/webauthn/login/begin|finish` публичные, CSRF-exempt, без лимитера — флуд пишет сессии и грузит CPU.
- **Фикс**: добавить `LoginRateLimit` (как на парольный логин).

---

## 🟡 MEDIUM (из аудитов)

| # | Файл | Проблема | Фикс |
|---|------|----------|------|
| 6 | `internal/domain/notification/routes.go:143-166` | `PUT /api/notifications/settings` full-replaces (bool без *bool) — частичное обновление выключает остальные каналы | `*bool` как в POST-версии |
| 7 | `internal/domain/admin/handler.go:363-387` | ToggleAdmin last-admin TOCTOU (два демоушена → 0 админов) | `SELECT ... FOR UPDATE` в транзакции |
| 8 | `internal/domain/admin/handler.go:786-793` | CreateBackup блокирует HTTP до 10 мин + ctx от клиента (дисконнект прерывает дамп) | Фоновая задача с независимым ctx |
| 9 | `internal/pkg/storage/local_storage.go:217-229` | `Delete` пропускает boundary-check при `baseDir==""` → риск удаления произвольного файла | Guard для пустого baseDir |
| 10 | `internal/domain/game/hnd_note.go:48,111,142` | Все ошибки → 403 + сырой текст (5xx как 403, утечка деталей) | Только sentinel-права → 403 |
| 11 | `internal/pkg/middleware/auth.go:39-89` | Роль-кэш на `sync.Mutex` — контенция на каждом авторизованном запросе | `sync.RWMutex`/шардирование |
| 12 | `internal/domain/game/svc_play.go:753-770` + `tournament/service.go:533-547` | Кэш через `GetOrSetWithCtx`+type-assert не хитится с Valkey (JSON→`[]any`), game settings и лидерборд каждый раз читают БД | `cacheGetJSON`/typed-bytes helper |
| 13 | `internal/domain/tournament/handler.go:37-44` | `points_for_*` без верхней границы (переполнение int в лидерборде) | clamp до разумного максимума |
| 14 | `internal/domain/user/auth_handler.go:131,143` | Login делает дублирующий SELECT пользователя (для 2FA-проверки) | Вернуть *User из Login |
| 15 | `internal/domain/user/service.go:163-218` | Успешный login пишет UPDATE (сброс попыток) всегда | Условный `WHERE failed<>0 OR locked IS NOT NULL` |

---

## ⚪ LOW / NIT

| # | Файл | Проблема |
|---|------|----------|
| 16 | `internal/pkg/storage/local_storage.go:177` | `Save` возвращает `//abs/...` при абсолютном baseDir (двойной слэш) |
| 17 | `internal/domain/export/service.go:79,178,304` | Ошибки `csvWriter.Flush()` глотаются |
| 18 | `internal/domain/game/hnd_photo.go:239-305` | `DeletePhoto` игнорирует game_id из URL (контракт, не IDOR) |
| 19 | `internal/domain/game/svc_note.go:36` | `Create` не проверяет, что LevelID принадлежит gameID |
| 20 | `internal/domain/user/webauthn_handler.go:380-398` | discoverableHandler не сверяет userHandle с user |
| 21 | `internal/pkg/email/email.go:396-400` | SMTP-заголовки без CRLF-санитизации (латентная инъекция) |
| 22 | `internal/pkg/storage/local_storage.go:122-126` | Файлы 0644/0755 + предсказуемые имена `{userID}_{unixnano}` |
| 23 | `internal/domain/game/hnd_review.go:44,78` | Игнорируемые `strconv.Atoi` → 403 вместо 400 |
| 24 | `internal/domain/calendar/handler.go:274` | `EscapeICalText` не экранирует `\r` |

---

## 📊 Оптимизации

### pprof-подтверждённые
- **bcrypt cost 12** на регистрации/логине — 93.5% CPU под нагрузкой регистраций. Осознанный trade-off (анти-брутфорс). *Не менять без причины; при масштабировании — вынести регистрацию в worker.*

### Быстрые победы (топ-5)
1. **Гранулярные права на Phase-3/import/export** (#3) — закрывает самый опасный класс (observer пишет контент).
2. **Valkey-совместимый кэш** для game settings и лидерборда (#12) — восстанавливает P5/P1 на реальном проде с Valkey.
3. **`sync.RWMutex` для role-cache** (#11) — дешёвое снижение контенции на всех авторизованных запросах.
4. **Убрать дублирующий SELECT в Login** (#14).
5. **WebAuthn `VerificationRequired`** (#4) — усиление passwordless-границы.

---

## 🚀 Улучшения (код + UX)

### Кодовая база
1. **Единый слой гранулярных прав** — централизовать проверки `HasPermission` для менеджерских операций (сейчас разбросано: где IsUserManager, где RoleUploadMedia).
2. **Кэш настроек темы** — `GetThemeSettings` на каждый авторизованный запрос (проверить и кэшировать).
3. **Фоновая обработка backup** (#8) — pg_dump в worker с независимым ctx.
4. **Санитизация SMTP-заголовков** (#21) — запрет CRLF в subject/to.
5. **Строгие права на загрузки** (#22) — 0700/0600 + random nonce.

### Пользовательский опыт
| Идея | Эффект |
|------|--------|
| **Онлайн-индикатор** в чате (кто в сети по WS) | Живость real-time |
| **Уведомление о подтверждении платежа** (теперь вебхук работает!) | Закрывает петлю после оплаты |
| **PWA-установка** (beforeinstallprompt) | Удержание, оффлайн |
| **Тёмная карта** в мониторинге (Leaflet dark tiles) | Согласованность с темой |
| **Фильтры в списке игр** (дата/статус/мои) | Быстрая навигация |
| **Пагинация/виртуализация чата** при >500 сообщений | Плавность на длинных играх |
| **Прогресс-бар прохождения** в мониторинге | Лучшая читаемость |
| **Онбординг для авторов** (первая игра → подсказки) | Снижение порога |

---

## ✅ Подтверждено исправленным в PASS-1 (pprof + эмпирика)
- Утечка redis-goroutine при недоступном Valkey — **0 goroutine** в pprof.
- Платёжный вебхук — теперь доходит до handler (не 403 CSRF).
- GameRooms IDOR — 403 для не-менеджера.
- gzip статики — работает (после фикса PASS-2, см. #1).

---

## 🎯 Рекомендации (что чинить первым)

1. **Гранулярные права на Phase-3/import/export** (#3) — самый опасный пробел (observer пишет контент).
2. **Valkey-совместимый кэш settings/leaderboard** (#12) — реальный прод с Valkey.
3. **WebAuthn `VerificationRequired` + clone-reject** (#4).
4. **RWMutex role-cache** (#11) + **убрать дублирующий SELECT в Login** (#14).
5. **ToggleAdmin TOCTOU + CreateBackup фоновая** (#7, #8).

---

*Дата: 2026-08-11. Метод: pprof (goroutine/heap/cpu) + @reviewer + @security + @perf + эмпирические HTTP-проверки.*
