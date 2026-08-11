# DEEP_REVIEW — Gengine-0 (PASS 5)

> Глубокое ревью после закрытия PASS-4.
> Метод: pprof-профилирование (goroutine/heap на :6060, loopback) + 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая проверка кода.
> ✅ = исправлено в этом проходе. 🔍 = подтверждено эмпирически.
> Архивы: `DEEP_REVIEW_2026-08-11_pass{1,2,3,4}.md`.

---

## 🔬 pprof-результаты (PASS 5)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 19 в покое | ✅ Без утечек. |
| **heap** | 22 MB | Норма. |
| **pprof bind** | `127.0.0.1:6060` | ✅ Фикс H2 PASS-4 работает (loopback). |

**Вывод pprof**: профиль чистый; утечек нет.

---

## 🔴 HIGH

### H1. Идемпотентность CreatePayment — мёртвый код → двойная оплата при ретрае 🔍✅
- **Файл**: `internal/domain/payment/service.go:249-258`.
- **Проблема**: `idemKey` генерировался заново на каждый вызов, а `GetByIdempotencyKey(idemKey)` искал по свежему ключу — ветка «вернуть существующую запись» недостижима. При таймауте API и ретрае создавался ВТОРОЙ платёж в ЮKassa.
- **Фикс**: ✅ переиспользуем существующий pending-платёж пользователя по `(user_id, amount_kopecks, status=pending)` (`GetPendingByUserAndAmount`); resume с тем же ключом.

### H2. `UpdateAfterCreate` ошибка → вебхук теряет платёж 🔍✅
- **Файл**: `payment/service.go:299-303`.
- **Проблема**: при сбое апдейта запись оставалась с `payment_id="local-..."`, вебхук искал по реальному id → not found → 500 → вечные ретраи ЮKassa.
- **Фикс**: ✅ повтор апдейта (retry) + лог при повторном сбое.

### H3. Cross-game IDOR в Phase-3 (SetTeamRoute/StartTime/Answer) 🔍✅
- **Файл**: `game/svc_passing.go` + `hnd_passing.go`.
- **Проблема**: `GameManager` проверяет права на игру из URI `:id`, но сервисные методы не проверяли принадлежность `passing_id`/`level_id` игре — модератор игры A мог менять маршруты/ответы команд игры B.
- **Фикс**: ✅ методы принимают `gameID` и проверяют `passing.GameID == gameID` / `level.GameID == gameID`; sentinel `ErrPassingNotInGame`/`ErrLevelNotInGame` → 403; интерфейс+моки обновлены.

---

## 🟠 MEDIUM

### M1. ExportTeamResultsCSV — observer выгружает данные всех команд 🔍✅
- **Файл**: `export/handler.go:528-540`.
- **Проблема**: `IsUserManager` пропускает observer (read-only) → экспорт каждой команды отдельно — утечка, как в ExportResultsCSV до PASS-4 H3.
- **Фикс**: ✅ `CanModerateGame` для экспорта чужих команд (капитан/автор — только свои).

### M2. Calendar singleflight использует ctx первого запроса 🔍✅
- **Файл**: `calendar/handler.go` + `game/service.go` (GetByID).
- **Проблема**: отмена ПЕРВОГО запроса роняла всех ожидающих singleflight (все 500 + повторный stampede).
- **Фикс**: ✅ `context.WithoutCancel(ctx)` внутри замыканий.

### M3. PersonalChat rate-limit per-IP — 429 для всех за прокси, обход ротацией 🔍✅
- **Файл**: `middleware/rate_limiter.go`, `monitor/routes.go`.
- **Проблема**: ключ per-IP — за reverse-proxy без TRUSTED_PROXIES все пользователи делят один бюджет.
- **Фикс**: ✅ per-user ключ (действие аутентифицировано).

### M4. `/payments/create` без rate-limit 🔍✅
- **Файл**: `payment/routes.go`.
- **Проблема**: аутентифицированный пользователь флудил pending-записями + outbound-вызовами к api.yookassa.ru.
- **Фикс**: ✅ `CodeSubmissionRateLimit(1m, 5)` на POST /payments/create.

### M5. CSV round-trip: ответ с `|` разбивается; `'=42` теряет апостроф 🔍
- **Файл**: `export/service.go:112,312`.
- **Проблема**: `strings.Join(answerCodes, "|")` + `Split("|")` — код с `|` портится; `'=42` (реальный апостроф) теряет `'` при unescape.
- **Фикс**: требуется экранирование `|` внутри кодов или JSON-столбец; задокументировано (риск целостности backup/restore).

### M6. JSON-импорт уровней без лимитов (несимметричен CSV) 🔍
- **Файл**: `level/import.go:71-121`.
- **Проблема**: 5MB JSON без cap на уровни/вопросы; Position без валидации; Type без allowlist.
- **Фикс**: требуется (лимиты + валидация, как в CSV PASS-4 M2); задокументировано.

### M7. permCache sweep O(n) под локом при переполнении 🔍✅
- **Файл**: `monitor/repository.go`.
- **Проблема**: каждый промах при map>10000 делал полный проход под блокировкой (горячий путь чата).
- **Фикс**: ✅ sweep не чаще 1/с (`lastSweep`).

### M8. ProfileService.InvalidateProfileStats — мёртвый код 🔍
- **Файл**: `user/profile_service.go`.
- **Проблема**: метод не вызывается (нет DI из game-домена); счётчики устаревают до 60с.
- **Решение**: ✅ осознанный TTL 60с; комментарий обновлён (метод — точка интеграции).

---

## 🟡 LOW

| # | Файл | Проблема | Статус |
|---|------|----------|--------|
| L1 | `payment/service.go` | Переполнение `w*100` в kopecks; `MinInt64` мусор | ✅ guard + тест (учтён underflow MinInt64-f) |
| L2 | `export/service.go:73-86` | `'=42` теряет апостроф при unescape | Документировано (вместе с M5) |
| L3 | `payment/handler.go` | `event` не сверяется со статусом API; mismatch → 500 → вечные ретраи | Документировано (классифицировать как 4xx/Conflict) |
| L4 | `monitor/handler.go` | `load_older` без recheck членства | Документировано (до read-deadline) |
| L5 | `admin/service.go` | backupMu per-process; nonce 32 бита; goroutine без recover | ✅ recover добавлен; nonce/распределённый lock — задокументировано |
| L6 | `monitor/handler.go` | PersonalChat GET — мутация без CSRF | Документировано (SameSite=Strict снижает) |
| L7 | `export/service.go:251-253` | Импорт молча пропускает строки <5 полей | Документировано |
| L8 | `profile_service.go` | Кэш RecentGames не подключён | Документировано (P4) |

---

## ⚡ Оптимизации (perf-аудит)

### MEDIUM
| # | Файл | Оптимизация | Статус |
|---|------|-------------|--------|
| P1 | `monitor/repository.go` | permCache sweep 1/с | ✅ |
| P2 | `export/service.go` | Батч-INSERT (30k на 5000 записей → ~10-20с) | Задокументировано (риск изменения ошибко-семантики) |
| P3 | `user/profile_service.go` | Кэшировать RecentGames (60с) | Задокументировано |
| P4 | `game/svc_listing.go` | version-key в памяти (atomic) вместо GET из кэша на каждый запрос | Задокументировано |
| P5 | `monitor/repository.go` | SaveMessage — 2 round-trip (Create+GetMessageByID) | Задокументировано |

### LOW
| # | Файл | Оптимизация |
|---|------|-------------|
| P6 | `websocket/room_hub.go` | sync.Pool для слайса клиентов в dispatchToRoom |
| P7 | `game/hnd_sse.go` | Один bytes.Buffer для SSE-payload (меньше аллокаций) |
| P8 | `notification/repository.go` | DDL-индекс `notifications(read_at) WHERE read` — **только задокументировать** |

---

## 🚀 Улучшения проекта (код + UX)

### Кодовая база
1. **Батч-вставка** в CSV/JSON-импортах (P2) — с тестом на частичную атомарность.
2. **Кэш RecentGames** (P3) — завершить профиль-страницу.
3. **Инвалидация профиль-статистики** при финише игры (через шину/колбэк, не DI-связь).
4. **Replay-кэш вебхуков** `(payment_id, event)` + классификация mismatch как 4xx (L3) — остановить вечные ретраи ЮKassa.
5. **Экранирование `|`** в кодах ответов CSV (M5).
6. **Перевод `/chat/personal` на POST** с CSRF (L6).
7. **Распределённый backup-lock** для multi-instance (L5).

### Пользовательский опыт
| Идея | Эффект |
|------|--------|
| **Статус платежа после retry** (H1/H2) | Пользователь не оплачивает дважды; «pending» не застревает |
| **Капитан видит только свою команду**, модератор — все (M1) | Консистентность прав |
| **Ошибка экспорта** — понятная (4xx/5xx) вместо вечного ретрая | Лучше диагностика |
| **Личный чат** — подтверждение собеседника (accept/block) | Контроль входящих |
| **JSON-импорт** с валидацией (M6) | Не создаёт мусорных уровней |

---

## 📊 Приоритеты исправления

1. ✅ **H1** (идемпотентность платежей), **H2** (fallback payment_id), **H3** (cross-game IDOR).
2. ✅ **M1** (export observer), **M3** (personal chat per-user), **M4** (payments rate-limit), **M2** (singleflight ctx), **M7** (sweep 1/с).
3. ✅ **L1** (kopecks overflow), **L5** (backup recover).
4. ⏳ На будущее: **M5** (`|` в кодах), **M6** (JSON-import лимиты), **L3** (webhook replay/4xx), **P2** (батч), **P3** (RecentGames кэш).

---

*Дата: 2026-08-12. Метод: pprof (goroutine/heap на :6060 loopback) + @reviewer + @security + @perf + эмпирические проверки. Архив PASS-4: `DEEP_REVIEW_2026-08-11_pass4.md`.*
