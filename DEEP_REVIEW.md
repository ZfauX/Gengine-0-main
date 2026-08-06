# Deep Review Gengine-0 — 6 августа 2026 (pass 23 — итоговый статус)

## Резюме

Все пункты глубоких ревью pass 21 и pass 22 **закрыты**. Критических, высоких и средних проблем безопасности, корректности и производительности не осталось. Ниже — итоговый статус по категориям и осознанные компромиссы.

## Выполнено (по волнам, коммиты b7eb6ca…ecd094a)

### Безопасность
- **WebAuthn**: регистрация passkey у пользователя с 2FA требует подтверждённой 2FA-сессии; passkey-логин для 2FA-пользователей требует TOTP (`2fa_required` flow) — закрыт backdoor
- **DeleteUser** отзывает refresh-токены (украденный токен не переживает удаление аккаунта)
- **2FA**: backup-верификация под rate-limit; отключение 2FA требует TOTP-код
- **Session-cookie** Secure через reverse-proxy/ForceSecureCookie
- **Refresh-ротация** атомарная (ClaimForRefresh), семьи + отзыв при reuse, строгий fingerprint
- **Роль из БД** в AuthRequired (пониженный/удалённый админ теряет права немедленно)
- **LogoutAll** отзывает текущий JWT; **глобальный rate limiter** зарегистрирован
- SQL-инъекций не найдено (все raw-запросы параметризованы); LIKE-экранирование
- Co-author список гейтится IsUserManager
- **VK OAuth**: вход с неверифицированным email отклоняется при линковке существующего аккаунта (пользователь войдёт по паролю)

### Корректность/целостность
- **Турнирные очки**: advisory-lock + атомарное чтение в транзакции; точное значение `tournament_points` (миграция 000033) для списания
- **Рейтинг**: атомарный guard `rating_scored`, ошибки откатывают транзакцию
- **Гонки закрыты**: AcceptInvitation (ON CONFLICT + status-claim), Tournament.Apply, notification SaveSettings upsert, team-chat room
- **CalculateResults** сериализован; ties детерминированы; onCommit после коммита во всех путях
- DeleteLevelFromActiveGame не оставляет команды без прогресса; defaultGameSetting единый

### Производительность
- **SnapshotDispatcher**: асинхронный debounce 500мс (тяжёлый пересчёт вне HTTP-запроса)
- **Valkey typed-кэш**: listing/leaderboard/reviews теперь хитнят (был no-op)
- GetGameplayData параллелен (errgroup); CheckTimeouts с ORDER BY; SSE async fan-out; poller без дублей; unread-count кэш; двойной CalculateResults убран
- **StaticCacheMiddleware** зарегистрирован (immutable для ?v=); батчинг уведомлений капитанов; auto-start без лишнего Preload

### UX/a11y
- **Мёртвый JS удалён** (~650 строк), initPushSubscription реализован, alert()→toasts, reconnecting WS везде
- Focus-trap модалок, aria-live чатов/таймера/логов, keyboard-календарь/фото, aria-state на toggle, контраст (gray-500/red-600/yellow-700), локализованные alt/aria-label
- Пагинация логов починена; мобильный дубль прохождений убран; мониторинг — diff-render; чат — автоскролл только у низа; wizard aria-current

### Тесты
- Refresh-ротация/reuse/fingerprint/lockout, tournament scoring/removal, CheckTimeouts, CalculateResults (ties/penalty), SnapshotDispatcher, SubmitCode last-level, backup download, suspicious detection

## Осознанные компромиссы (не баги)
| Пункт | Статус |
|---|---|
| **C-M10** SnapshotDispatcher generation | Безвредный лишний пересчёт (идемпотентный) |
| **P-M4** ListByGamePaginated (Count+Preload) | Уже пагинировано по индексу; 3-4 запроса на страницу приемлемо |
| leaflet.js/css в precache | Обновляются при bump ASSET_VERSION (кэш SW) |
| **T-M3** t.Parallel() в DB-тестах | Не добавлен: риск флаков/нагрузки PG |

## При деплое
```
make migrate   # применить 000025–000033 (если не применялись)
make build && make test-integration
```
