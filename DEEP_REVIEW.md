# DEEP_REVIEW — Gengine-0 (PASS 6)

> Глубокое ревью после закрытия PASS-5.
> Метод: pprof-профилирование (goroutine/heap на :6060, loopback) + 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая проверка кода и миграций.
> ✅ = исправлено в этом проходе. 🔍 = подтверждено эмпирически.
> Архивы: `DEEP_REVIEW_2026-08-1{1,2}_.pass{1,2,3,4,5}.md`.

---

## 🔬 pprof-результаты (PASS 6)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 19 в покое | ✅ Без утечек. |
| **heap** | 22 MB | Норма. |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback. |

---

## 🔴 HIGH

### H1. Payment: идемпотентность не работает при КОНКУРЕНТНЫХ retry (TOCTOU) 🔍✅
- **Файл**: `payment/service.go:264-272` + `repository.go:64-74`; миграции — только `idx_payments_user`/`idx_payments_status` (не уникальные).
- **Проблема**: `GetPendingByUserAndAmount` (SELECT) → INSERT → вызов ЮKassa. Два параллельных запроса на одну сумму оба промахиваются → два INSERT → два реальных платежа в ЮKassa.
- **Фикс**: ✅ миграция **000064** — частичный уникальный `(user_id, amount_kopecks) WHERE status='pending'` (INSERT упадёт по unique при гонке; повторный запрос вернёт существующий pending).

### H2. `GetOrCreate*Room`: конфликт-обработка не работает — уникальных индексов нет 🔍✅
- **Файл**: `monitor/repository.go` (все 6 GetOrCreate-методов) + миграции `000001`/`000002`/`000054`/`000059` (только обычные индексы).
- **Проблема**: комментарии «if duplicate (race), re-query» предполагают unique constraint, но его нет — гонка создания (личная комната при одновременном входе, игровая при первом заходе) даёт **дубликаты строк**.
- **Фикс**: ✅ миграция **000064** — уникальные частичные индексы `chat_rooms` (personal/game_general/team_general) + `deleted_at IS NULL`.

---

## 🟠 MEDIUM

### M1. `GetRecentGames` возвращает кэшированный слайс по ссылке + sweep не rate-limited 🔍✅
- **Файл**: `user/profile_service.go:114-141`.
- **Проблема**: (а) возврат внутреннего слайса — мутация consumer → data race; (б) sweep удаляет только истёкшие при len>512 — при свежих записях map растёт.
- **Фикс**: ✅ возврат копии `append([]RecentGame(nil), ...)` + sweep 1/с (`gamesLastSweep`).

### M2. JSON-импорт: автопозиция `MAX+1` — гонка → дубликаты позиций 🔍
- **Файл**: `level/import.go:136-142`.
- **Проблема**: два параллельных импорта в одну игру читают одинаковый MAX → оба уровня с позицией max+1; unique `(game_id, position)` есть в 000001 (стр. 110) — вернёт 500 вместо понятной ошибки.
- **Фикс**: `SELECT ... FOR UPDATE` на игру / advisory lock в транзакции; задокументировано.

### M3. `CloseVoting` маппит ЛЮБУЮ ошибку в 403 🔍✅
- **Файл**: `monitor/handler.go:1501-1510`.
- **Проблема**: сбой БД/сеть отдаёт 403 вместо 500 (тот же анти-паттерн, что закрыт в PASS-4 L9).
- **Фикс**: ✅ только «нет прав» → 403, прочие → 500/Wrap.

### M4. `RemoveGame` списывает очки по ТЕКУЩЕЙ конфигурации турнира 🔍
- **Файл**: `tournament/service.go:183-190`.
- **Проблема**: начисление — по конфигурации на финиш, списание — по текущей; изменение очков между финишем и удалением даёт «фантомные» очки.
- **Фикс**: хранить начисленное per (passing, tournament) и списывать именно его; задокументировано.

### M5. Webhook использует request-ctx на весь конвейер 🔍✅
- **Файл**: `payment/handler.go:131` → `service.go`.
- **Проблема**: обрыв соединения ЮKassa посреди обработки → платёж остаётся pending, notify теряется.
- **Фикс**: ✅ `context.WithoutCancel(c.Request.Context())` в handler.

### M6. Webhook не идемпотентен на уровне статусов (дубль уведомления) 🔍✅
- **Файл**: `payment/service.go:452-483`.
- **Проблема**: повторный `payment.succeeded` снова вызывал `UpdateStatus` + `notifyPaymentSucceeded` (дубликат уведомления); `canceled` после `succeeded` перезаписывал статус.
- **Фикс**: ✅ проверка `local.Status == StatusSucceeded` — notify только при первом переходе.

### M7. Личный чат: согласие так и не реализовано (спам смягчён, не закрыт) 🔍
- **Файл**: `monitor/routes.go:125`, `handler.go:1162-1192`.
- **Проблема**: per-user rate-limit (30/мин) + проверка цели; но согласие (accept/block) не реализовано, per-connection лимитер сообщений обходится несколькими сокетами.
- **Фикс**: согласие на личный чат или глобальный per-user WS-лимитер; задокументировано.

### M8. CSV re-import: удаление вопросов без явного удаления ответов ⚠️
- **Файл**: `export/service.go:338` (`Unscoped().Delete(&Question{})`).
- **Проблема**: ответы удаляются только если FK `answers.question_id → questions(id)` ON DELETE CASCADE (000001 не проверен на каскад).
- **Фикс**: проверить миграцию; при отсутствии каскада — явно удалять answers; задокументировано.

### M9. JSON-импорт без верхней границы позиции 🔍
- **Файл**: `level/import.go:109-117`.
- **Проблема**: CSV ограничивает pos ≤10000, JSON — только ≥0; позиция 2^31-1 ломает сортировку.
- **Фикс**: `if il.Position > 10000 { error }`; задокументировано.

---

## 🟡 LOW (из аудитов, задокументировано)

| # | Файл | Проблема |
|---|------|----------|
| L1 | `payment/service.go:150-155` | Граничный overflow `w==MaxInt64/100 && f>=8` (недостижимо через handler, лимит 100k) |
| L2 | `payment/service.go:392` | Сравнение user!=ShopID не constant-time (реальная защита — IP+API) |
| L3 | `user/two_factor_service.go:223-240` | Логирование email (PII) при Enable/Disable2FA |
| L4 | `user/oauth_service.go:185-186` | VK externalID может быть пустым (коллизии unique) |
| L5 | `monitor/handler.go:549-556,1298-1305` | После отклонения RegisterClient handler продолжает слать snapshot (нужно подтвердить идемпотентность Close) |
| L7 | `monitor/handler.go:1128-1149` | CreateRoom без rate-limit |
| L8 | `export/service.go:118-121` | TrimSpace теряет хвостовые пробелы в ответах |
| L9 | `monitor/handler.go:1006-1025` | ChatRoomIDs ~8-10 запросов на вызов (chatty) |
| L10 | `admin/service.go:74-93` | Фоновая горутина CreateNowAsync не в WaitGroup (shutdown убивает pg_dump) |
| L11 | `tournament/service.go:122-130` | AddGame.OrderIndex = COUNT(*) — дырявые индексы после RemoveGame |
| L12 | `user/two_factor_service.go:121-136` | Модуло-биас в GenerateBackupCodes (~2^-64) |
| L13 | `calendar/handler.go` | Sweep кэша только истёкших (известный L5 PASS-4) |

---

## ⚡ Оптимизации (perf-аудит)

### HIGH
| # | Файл | Оптимизация | Статус |
|---|------|-------------|--------|
| P1 | `calendar/handler.go:207-287` | ✅ ICS singleflight (годовой запрос с Preload) + WithoutCancel | ✅ |
| P2 | `user/profile_service.go` | ✅ RecentGames копия + sweep 1/с | ✅ |
| P3 | `export/service.go:364` | Вопросы CSV всё ещё по одному INSERT (до 5000) — батч questions | Задокументировано (P2 закрыл только answers) |
| P4 | `monitor/handler.go:1171` | PersonalChat лишний GetByID на каждую страницу (проверять только при создании) | Задокументировано |
| P5 | `user/profile_service.go` | singleflight для stats/games (stampede на TTL-истечении) | Задокументировано |

### MEDIUM
| # | Файл | Оптимизация |
|---|------|-------------|
| P6 | `payment/repository.go` | DDL-индекс `(user_id, status, amount_kopecks, created_at DESC)` — **только задокументировать** |
| P7 | `render/helper.go:191-204` | `GetFlash` делает до 5 session.Save на страницу — идемпотентный flash |
| P8 | `websocket/room_hub.go:318` | sync.Pool для слайса клиентов в dispatchToRoom |
| P9 | `calendar/handler.go:115-130` | singleflight по месяцу без tz (рендер с tz после) |
| P10 | `user/repository.go:276-283` | DashboardAuthoredGames без LIMIT |

### LOW
| # | Файл | Оптимизация |
|---|------|-------------|
| P11 | `notification/service.go:330-353` | GetSettings можно проверять до выборки подписок |
| P12 | `game/hnd_sse.go:313-345` | Один bytes.Buffer для SSE-payload |
| P13 | `export/handler.go:114` | Стримить CSV напрямую (не буферизовать мегабайты) |
| P14 | `tournament/repository.go` | ListGames/ListTeams без LIMIT |

---

## 🚀 Улучшения проекта (код + UX)

### Кодовая база
1. **Уникальные индексы** (000064) — закрывают TOCTOU платежей и гонку комнат.
2. **Идемпотентность succeeded-webhook** — без дублей уведомлений.
3. **Согласие на личный чат** (accept/block) — закрыть спам полностью (M7).
4. **Хранение начисленных очков турнира** per (passing, tournament) (M4).
5. **Advisory lock** для JSON-импорта (M2) + верхняя граница позиции (M9).
6. **Батч вопросов** в CSV-импорте (P3) — сократить 5000 INSERT.

### Пользовательский опыт
| Идея | Эффект |
|------|--------|
| **Уведомление «платёж подтверждён» не дублируется** (M6) | Чистый центр уведомлений |
| **Личный чат по приглашению** (accept) | Контроль входящих, меньше спама |
| **Быстрый импорт уровней** (P3) | Итеративная правка игры не ждёт 20с |
| **Понятные 500/403** в голосовании (M3) | Корректная диагностика |

---

## 📊 Приоритеты исправления

1. ✅ **H1** (payment TOCTOU — unique index 000064), **H2** (chat_rooms race — unique index 000064).
2. ✅ **M3** (CloseVoting 403/500), **M5** (webhook WithoutCancel), **M6** (succeeded idempotent).
3. ✅ **M1** (RecentGames копия), **H1-perf** (ICS singleflight), **H2-perf** (sweep 1/с).
4. ⏳ → ✅ Закрыто далее: **M2** (JSON import advisory lock), **M4** (точное списание очков — миграция 000065), **M7** (согласие на личный чат — миграция 000066 + UI), **M9** (граница позиции), **P4** (PersonalChat создание-only), **P5** (singleflight stats/games), **P7** (flash session once), **L1** (overflow), **L2** (constant-time), **L3** (PII), **L4** (VK user_id), **L6** (permCache cap), **L7** (CreateRoom rate-limit), **L8** (хвостовые пробелы), **L10** (backup WaitGroup), **L11** (OrderIndex), **L13** (calendar cap); **L5/L12** сняты (Close идемпотентен, 32 делит 2^64); **L9** задокументирован.
5. ⏳ Осталось задокументировать: **P3** (батч вопросов CSV — рискованный), **L9** (ChatRoomIDs chatty — рефакторинг репозитория), P6, P8-P14.

---

*Дата: 2026-08-12. Метод: pprof (goroutine/heap на :6060 loopback) + @reviewer + @security + @perf + эмпирические проверки кода и миграций. Архив PASS-5: `DEEP_REVIEW_2026-08-12_pass5.md`.*
