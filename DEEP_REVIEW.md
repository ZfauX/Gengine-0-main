# Deep Review Gengine-0 — 7 августа 2026 (ФИНАЛЬНЫЙ статус)

## Резюме

Pass 21–27 + финальная партия закрыли все критические и высокие пункты глубокого ревью.
**Найдено и исправлено 2 критичных бага деплоя в последней партии** (см. ниже).
Проект готов к деплою: `make migrate && make build && make test-integration`.

## Финальная партия (commit `50e253a`)

### 🔴 Критично — миграции
- **Свежая БД через squashed не получала 000024-000036** (games_view, tournament_scored, family_id, rating_value, participant_count, tournament_points, новые индексы, триггеры). Squashed был заморожен на 000023, а `migrate.go` при `version >= 5` пропускал все индивидуальные миграции.
  - Фикс: добавлен `migrations_squashed/000006_schema_tail.{up,down}.sql` (слепок 000024-000036);
  - `migrate.go` переписан: squashed-БД (version ≤ 6) продолжают squashed-набором (догоняют tail), индивидуальные БД (> 6) — поштучно.
- **Миграция 000028 падала на свежей/любой БД**: `conkey = ARRAY[game_id_attnum, position_attnum]` сравнивал `smallint[]` с `integer[]` → SQLSTATE 42883. Исправлено `conkey::int[]`.

### 🟠 Прочее
- **2FA step-up TTL**: флаг верификации теперь хранит timestamp и живёт 15 минут (S14) — украденная кука не даёт бессрочного доступа к `/admin/*`. Legacy `true` игнорируется.
- **Admin-пагинация команд** сохраняет query-фильтр (`&query={{urlquery}}`).
- **Мёртвый шаблон `levels-show.html`** удалён (ни один Go-хэндлер его не рендерил; Leaflet остаётся на game/games-show, где используется).

## Исправлено в pass 27 (commit `512a7fe`)

### 🔴 Критично
- **WS уведомлений не работал** (контекст привязывался к `c.Request.Context()` + `defer cancel()` убивал соединение).
- **Обход лимитов WS**: двойной `UnregisterClient` (handler + writePump) → счётчики уходили в минус. Теперь идемпотентно.

### 🟠 Высокие
- Monitor poller race + SSE без начального снапшота + per_page clamp + ChatWS rate-limit.
- 2FA-enable теперь требует пароль.
- Push SSRF: CGNAT + IPv4-mapped IPv6.
- `/metrics` и `/swagger` получили 2FA step-up.
- Valkey rate-limit fail-open (нет 429-шторма при сбое Redis).
- OptionalAuth fail-closed при ошибке роли.
- WebAuthn + 2FA: redirect на `/auth/2fa/login` (не `/verify`).
- Reset-code не логируется.
- OAuth state привязан к провайдеру; `isHTTPS` доверяет X-Forwarded-Proto только от trusted-прокси.

### 🟡 Средние
Push body leak, APIEmailSave partial update, follow race (OnConflict), email-утечка в подписках, ExportTeamResultsCSV мёртвый endpoint, CSV size guard (MaxBytesReader), re-import заменяет вопросы, GetHealthStatus data race.

## Осознанные компромиссы (не баги)
| Пункт | Статус |
|---|---|
| **DeleteUser не удаляет игры-авторства** | FK/продуктовое решение: админ удаляет игры отдельно |
| **SubmitCode level reuse / AdvanceToNextLevel passing reuse** | Частично оптимизировано; дальнейшее — рискованно для стабильности |
| **Секреты в refs старых PR** | Оставляем (решение пользователя) |

## При деплое
```
make migrate   # применяет 000001-000036 (squashed-БД догонит tail до 6; индивидуальные до 36)
make build && make test-integration
```

## Статус
Все критические/высокие пункты глубоких ревью pass 21–27 и финальной партии закрыты.
Проект готов к деплою.
