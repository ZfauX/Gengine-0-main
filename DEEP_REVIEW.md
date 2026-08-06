# Deep Review Gengine-0 — 6 августа 2026 (pass 24 — итоговый статус)

## Резюме

Высокие и средние пункты pass 24 **закрыты** в двух коммитах (`06e9df2`, `7f66bd6`). Критических проблем не осталось. Ниже — что исправлено и осознанные компромиссы.

## Исправлено в pass 24

### UX
- **Wizard** — `data-no-loading` на `#wizardSubmit`: кнопка «Далее» больше не застревает (initFormLoading не перезаписывает состояние).
- **sw.js** — guard `event.data` в push-обработчике: пустой payload не роняет обработчик.
- **Таймер** — ре-синк по `visibilitychange` при возврате на вкладку.

### Корректность/целостность
- **Дубли отзывов** — миграция 000034 `idx_reviews_game_user` + `ON CONFLICT DO NOTHING` в `ReviewService.Create` (RowsAffected==0 → «уже отзыв»).
- **checkTimeoutsImpl** — сбой `AdvanceToNextLevel` теперь откатывает транзакцию (партия повторится), а не застревает команду с `finished_at` без next-progress.
- **PhotoService.Delete** — различает `ErrRecordNotFound` (403) от реальных ошибок БД (500).
- **LevelService.Duplicate** — `pg_advisory_xact_lock(gameID)`, как в Move (закрыта коллизия позиций).
- **Список игр** — ошибка count-fallback больше не маскируется как «нет игр» (total=0).

### Безопасность
- **Origin-guard** (`middleware.APIOriginGuard`) на всех `/api/*` мутациях: проверка Origin и Sec-Fetch-Site (defense in depth поверх SameSite=Strict). Мёртвый `csrf_json.go` заменён рабочим кодом. Тест покрыт.
- **GET-формы** уровня/вопросов теперь требуют `IsUserManager` (ранее доступны любой игре).

### Производительность
- **AdvanceToNextLevel** — один запрос следующего уровня вместо загрузки всех уровней (O(N)→O(1)).
- **SubmitCode/SubmitFile/UseHint** — `CheckTeamMembership` возвращает gameID, убран повторный load passing (2-3 round-trip на submit).

### Тесты/CI
- **`testutil.SetupPostgresDB`** — единый `-short` guard: `go test -short` больше не требует PostgreSQL (пакеты скипаются вместо `t.Fatalf`). CI без БД теперь валиден.
- **APIOriginGuard** — 4 кейса (same-origin ok, cross-origin 403, Sec-Fetch-Site cross-site 403, GET ok).
- **unreadCache** — lazy sweep при росте >512 записей (устранена медленная утечка).

## Осознанные компромиссы (не баги, задокументировано)
| Пункт | Статус |
|---|---|
| **P-3** GetGameplayData 6-8 запросов | Частично оптимизирован (errgroup, CheckTeamMembership); column-select требует аккуратной проверки шаблонов |
| **P-M4** ListByGamePaginated | Уже пагинировано по индексу, приемлемо |
| **T-2 (расширение)** WebAuthn FinishLogin `2fa_required` e2e | Требует полноценного WebAuthn-мока; базовый 2FA-guard покрыт |

## При деплое
```
make migrate   # применить 000025–000035
make build && make test-integration
```

## Статус
Все критические/высокие пункты глубоких ревью pass 21–24 закрыты. Осталась опциональная оптимизация, не являющаяся багами.
