# Deep Review Gengine-0 — 6 августа 2026 (pass 25 — итоговый статус)

## Резюме

Высокие и средние пункты pass 25 **закрыты** в коммите `9efb823`. Критических проблем не осталось. Ниже — что исправлено и осознанные компромиссы.

## Исправлено в pass 25

### 🔴 UX (критично)
- **showToast-ReferenceError** — guard `if (window.showToast)` на flash→toast: больше не ломается SSE-клиент геймплея при наличии flash-ошибки.

### 🟠 Высокие
- **Фото**: загрузка только менеджером игры (IsUserManager); автору разрешено удалять чужие фото.
- **SubmitTestCode** — `onCommit()` вызывается после коммита (паттерн B1).
- **StartTesting** — `FOR UPDATE` на строку команды при переиспользовании orphan-команды (закрыта гонка двойного прохождения).
- **Tournament AddGame** — создаёт `StatusPending` прохождения для всех команд турнира.
- **Tournament RemoveGame** — сбрасывает `tournament_scored`/`tournament_points` (пере-добавленная игра пересчитывается).
- **Perf**: `GetCurrentProgressForUpdate` без прелоада графы ответов (SubmitCodeWithTx сам догружает); Valkey `DeleteByPrefixWithCtx` батчит DEL по 100; экспорт загружает только id попыток.
- **Security**: `/swagger` и `/metrics` — через `AuthRequired`+`AdminRequired` (роль из БД); `GET /games/:id/testing/start` → POST.

### 🟡 Средние
- **Hint** — `?hint=` в redirect UseHint (JS-тост работает).
- **Wizard** — защита финального шага от двойного submit (disable+spinner).
- **WS** — try/catch вокруг JSON.parse в team-chat/layout/logs.
- **Review** — максимальный рейтинг 5 (согласовано с хендлером/UI).
- **Update/Delete игры** — бизнес-ошибки → 403 с локализованным текстом; реальные ошибки БД → 500 generic (без сырого текста).
- **AcceptAnswer** — `game_id` опционален (нет 400 на успешную мутацию).
- **AddMember** — `INSERT ... ON CONFLICT DO NOTHING`.
- **Logs prev-page** — `disabled` по серверному условию.

## Осознанные компромиссы (не баги)
| Пункт | Статус |
|---|---|
| **C-8** ChangeCaptain не обновляет team_members | Новый капитан должен быть участником (IsMember-проверка) — логика валидна; старый остаётся участником |
| **M2/M3** push-кнопки не синхронизированы на загрузке | UX-мелочь; кнопки работают, состояние не перечитывается |
| **L6** QR-код 2FA через api.qrserver.com | Внешняя зависимость; генерация локально — отдельная задача |
| **S-4** логин per-IP | Per-account счётчик — продуктовое решение |

## При деплое
```
make migrate   # применить 000025–000035
make build && make test-integration
```

## Статус
Все критические/высокие пункты глубоких ревью pass 21–25 закрыты. Осталась UX-косметика (push-синхронизация, контраст мелочи, focus-trap delete-модалки) и опциональные оптимизации — не баги.
