# Gengine-0 ROADMAP — pass 46 и далее

> Статус: **Фаза 46 выполнена полностью**. Предыдущий ROADMAP (Фазы 0–6) выполнен.
> Процесс: реализация → линтер (`golangci-lint run ./...`) → тесты (`go test -short ./...`) → коммит.

---

## Фаза 46 — Полировка и устойчивость (по итогам аудита)

- [x] **S-1** Rate-limiter: fail-closed для критичных лимитеров (логин, регистрация, 2FA, коды) при недоступности Valkey; fail-open оставить только для глобального. — `Init*RateLimiterWithValkeyFailClosed` в `cmd/server/main.go:231-237`, `NewValkeyRateLimiterFailClosed` в `rate_limiter.go`, тест `TestRateLimiter_ValkeyFailClosedVsOpen`.
- [x] **S-2** ChatWS: access-control вынесен в `canJoinRoom` (единая логика для ChatWS/ChatRoomIDs/CreateRoom) с интерфейсными зависимостями — unit-тесты `TestCanJoinRoom_*` (личный/командный/капитанский/общий).
- [x] **S-3** Мониторинг WebHook: 4xx при верификации платежа + классификация ошибок (`ErrWebhookInvalid`→400, `ErrWebhookUntrustedIP`→403) — rejected больше не прячется под always-200.
- [x] **S-4** SQL: конкатенация таблиц в `user/repository.go` заменена на статический whitelist-map (`cleanup []{table,col}` + `HasTable`).
- [x] **S-5** ChatWS hot-path: `CanSendMessage` — единая проверка права на отправку (один запрос комнаты + LEFT JOIN членства + chat_room_members) вместо `IsTeamMemberOrCaptain`+`GetRoomMember` на каждое сообщение.

## Фаза 47 — Тестовая база

- [x] **T-1** Handler-тесты для ChatWS access-control (canJoinRoom: личный/командный/капитанский/общий).
- [x] **T-2** Тест TTL-семантики `GetOrSet` vs `GetOrSetWithCtx` (ttl=0 → дефолт).
- [x] **T-3** Негативный тест `SubmitCode` при сбое загрузки team_id (sqlmock).
- [x] **T-4** E2E: сценарии с двумя пользователями — профиль + личный чат.

## Фаза 48 — Функциональные улучшения

- [x] **F-1** Статистика в профиле: игры/победы/рейтинг + последние игры (блок в ЛК).
- [x] **F-2** Экспорт результатов: PDF/Excel/CSV (уже были реализованы; проверено).
- [x] **F-3** Турниры в UI: список/просмотр/лидерборд/игры/заявки (группы — следующий этап).
- [x] **F-4** Центр уведомлений: фильтр «Все/Непрочитанные» (+ API unread=1).
- [x] **F-5** Мобильная адаптация мониторинга: переключатель «Команды/Карта» (тайлы оффлайн — далее).

## Фаза 49 — Производительность и наблюдаемость

- [x] **P-1** pprof: отдельный сервер PPROF_ENABLED/PPROF_PORT (не на основном порту).
- [x] **P-2** Метрики: /metrics (админ+2FA), добавлен счётчик SSE-подключений.
- [x] **P-3** Кэш: единая семантика TTL-0 (SetDefault) для in-memory и Valkey (GetOrSet/WithCtx).
- [x] **P-4** Бенчмарки кэша: Get (178ns), GetOrSet hit/miss; rate-limiter Allow.

## Правила работы

1. **Порядок**: фазы по нумерации (устойчивость → тесты → фичи → производительность).
2. **После реализации каждой фичи**:
   - `golangci-lint run ./...` — линтер чист;
   - `go test -short ./...` — новые тесты зелёные;
   - если менялись роуты/права — `go test -tags=integration ./...`;
   - если менялся фронт — `make test-e2e`;
   - коммит в git с описанием.
3. **Тесты**: на каждую новую фичу (unit + при необходимости integration/E2E).
4. **Миграции**: новые файлы в `migrations/` + обновление `migrations_squashed/000007` tail'ом при необходимости.
