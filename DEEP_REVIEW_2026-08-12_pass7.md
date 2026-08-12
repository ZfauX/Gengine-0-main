# DEEP_REVIEW — Gengine-0 (PASS 7)

> Глубокое ревью после закрытия PASS-6.
> Метод: 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая проверка кода + интеграционные тесты.
> ✅ = исправлено в этом проходе. 🔍 = подтверждено эмпирически.
> Архивы: `DEEP_REVIEW_2026-08-11_pass{1,2,3,4}.md`, `DEEP_REVIEW_2026-08-12_pass5.md`.

---

## 🔴 HIGH

### H1. Согласие на личный чат: получатель не мог принять, инициатор мог принять сам себя 🔍✅
- **Файл**: `internal/domain/monitor/repository.go` (`AcceptPersonalRoom`).
- **Проблема**: условие было `user2_id = ?`. `GetOrCreatePersonalRoom` нормализует пару (`user1=min(id)`, `user2=max(id)`), а `OwnerID` — инициатор. Если инициатор имел БОЛЬШИЙ id: получатель попадал в `user1_id` и не мог принять чат вовсе, а инициатор (`user2_id`) мог принять сам себя — **обход консента** (прямое нарушение M7 PASS-6).
- **Фикс**: ✅ принимает любой участник (`user1_id = ? OR user2_id = ?`), НЕ являющийся владельцем (`owner_id IS DISTINCT FROM ?`). Инициатор принятие сам себя — `RowsAffected=0`.
- **Тест**: ✅ `TestChatService_PersonalRoomAcceptConsent` — оба порядка ID, инициатор-сам, посторонний.

### H2. `RemoveGame`: рассинхрон параллельных массивов `scored_ids`/`scored_points` 🔍✅
- **Файл**: `internal/domain/tournament/service.go` (блок списания очков).
- **Проблема**: удалялся только `tournament_scored_ids`, а `tournament_scored_points` оставался. После remove→add→finish индексы разъезжались, `scoredPointsForTournament` списывал устаревшее значение очков.
- **Фикс**: ✅ `scored_points` удаляется ПО ПОЗИЦИИ (unnest + WITH ORDINALITY), а не `array_remove` по значению (два турнира с одинаковыми очками удалили бы не тот элемент).
- **Тест**: ✅ `TestTournamentService_RemoveGame_MultiTournamentParallelArrays` — разные очки (10/20), после удаления из A остаётся B со значением 20.

---

## 🟠 MEDIUM

### M1. `TournamentHandler.Show`: лишний `GetMyTeams` для анонимов 🔍✅
- **Файл**: `internal/domain/tournament/handler.go`.
- **Проблема**: SQL-запрос команд выполнялся для каждого просмотра турнира, включая анонимных (userID=0), хотя шаблон использует `UserTeams` только внутри блока `CanApply` (заявка).
- **Фикс**: ✅ `GetMyTeams` только при `userID != 0`.

### M2. WS-чат: устаревший `Accepted` при подключении 🔍✅
- **Файл**: `internal/domain/monitor/handler.go` (ChatWS) + `repository.go`/`service.go`.
- **Проблема**: `chatRoom` загружался один раз при апгрейде сокета. Если получатель принял личный чат через HTTP ПОСЛЕ открытия WS, проверка `!chatRoom.Accepted` блокировала отправку до переподключения.
- **Фикс**: ✅ лёгкий `GetAcceptedStatus` (SELECT accepted) для личных комнат при каждой отправке (hot-path уже делает `CanSendMessage`).

### M3. Backfill `tournament_scored_points` для legacy-строк 🔍✅
- **Файл**: `migrations/000065_tournament_scored_points.up.sql`.
- **Проблема**: существующие строки с заполненным `scored_ids` имели пустой `scored_points` → RemoveGame списал бы 0 вместо начисленных очков.
- **Фикс**: ✅ UPDATE-backfill: раскладывает суммарный `tournament_points` по элементам `scored_ids` (base + остаток в последний; сумма сохраняется).

### M4. 2FA Verify: старый JWT не отзывался 🔍✅
- **Файл**: `internal/domain/user/two_factor_handler.go`.
- **Проблема**: комментарий обещал «jti blacklist», но `RevokeJWT` не вызывался — перехваченный до 2FA access-токен оставался валидным до истечения на всех авторизованных маршрутах.
- **Фикс**: ✅ читаем cookie `jwt` и отзываем его перед выдачей нового токена. `RevokeJWT` безопасен для уже невалидных/истёкших токенов.

---

## 🟡 LOW

### L4. Геолокация: NaN/Inf координаты проходили валидацию 🔍✅
- **Файл**: `internal/domain/game/hnd_geolocation.go`.
- **Проблема**: `NaN` проходит сравнения `lat < -90 || lat > 90` → попадал в БД.
- **Фикс**: ✅ `math.IsNaN/IsInf` для lat/lng/accuracy.

### L5. Импорт уровней: автопозиция превышала `maxImportPosition` 🔍✅
- **Файл**: `internal/domain/level/import.go`.
- **Проблема**: при `Position==0` автопозиция `maxPos+1` могла быть 10001+ — обход лимита M9.
- **Фикс**: ✅ ошибка, если `maxPos+1 > maxImportPosition`.
- **Тест**: ✅ `TestImportService_Import_AutoPositionCap` — 10000 уровней + импорт без позиции → ошибка, транзакция откатывается.

### L6. Webhook: гонка двух параллельных уведомлений 🔍✅
- **Файл**: `internal/domain/payment/repository.go` + `service.go`.
- **Проблема**: проверка `alreadySucceeded` читалась из локальной копии — два параллельных webhook'а оба видели pending и слали дубликат уведомления.
- **Фикс**: ✅ атомарный `MarkSucceededIfPending` (`UPDATE WHERE status <> 'succeeded'`, RowsAffected>0 = переход совершён этим вызовом). Только победивший уведомляет.
- **Тест**: ✅ `TestMarkSucceededIfPending_AtomicTransition` — 8 параллельных вызовов, ровно 1 переход.

### L7. `PersonalChat`: откат комнаты при транзиентной ошибке загрузки собеседника 🔍✅
- **Файл**: `internal/domain/monitor/handler.go`.
- **Проблема**: `DeleteRoom` выполнялся при ЛЮБОЙ ошибке `GetByID`, включая транзиентные (network/500) — комната удалялась без нужды.
- **Фикс**: ✅ удаление только при `errors.Is(uErr, gorm.ErrRecordNotFound)`.

---

## 📋 Проверки

- `go build ./...` ✅
- `go test -short ./...` ✅
- `go test -tags=integration ./internal/domain/...` ✅
- `golangci-lint run ./...` ✅ (0 issues)
- Новые тесты: monitor H1, tournament H2, level L5, payment L6 — ✅

## 🔬 pprof-результаты (PASS 7)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 19 в покое | ✅ Без утечек (без изменений с PASS-5/6). |
| **heap** | 22 MB | Норма. |
