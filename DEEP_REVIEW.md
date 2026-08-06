# Deep Review Gengine-0 — 6 августа 2026 (pass 25)

Повторный глубокий аудит после pass 21–24 (5 волн исправлений).

**Методология:** 5 параллельных агентов (security, perf, code, UX, tests) + **ручная верификация** ключевых находок. Ложные срабатывания исключены.

**Легенда:** 🔴 критично · 🟠 высоко · 🟡 средне · ✅ хорошо · ❌ ложное

---

## Итог

Критических проблем безопасности/корректности **не найдено** (после 5 волн исправлений атомарность, advisory-локи, SQL, CSP, Origin-guard на месте). Однако UX-агент нашёл **1 критичный фронтенд-баг** (ReferenceError ломает SSE) и несколько реальных функциональных проблем. Ниже — полный список.

---

## 🔴 Критично (UX)

### H2. `showToast` вызывается во время парсинга → ReferenceError ломает весь геймплей-SSE ✅ подтверждено
`gameplay-show.html:443-452`
- Инлайн-скрипт вызывает `showToast(...)` при наличии `.flash-error`/`.flash-info`, но `showToast` присваивается только внутри `initToast()` на `DOMContentLoaded` (app.js `defer`).
- ReferenceError прерывает IIFE → `flashError.remove()` и **весь SSE-клиент (строки 454-504) не запускаются** — real-time события (уровень, подсказка, таймер) мёртвые ровно после ошибки пользователя.
- **Фикс:** guard `if (window.showToast)`, либо перенос flash→toast в DOMContentLoaded.

---

## 🟠 Высокие

### 1. Фото: загрузка в любую игру без проверки прав ✅ подтверждено
`hnd_photo.go:106-186` (UploadPhoto)
- Только `AuthRequired`; нет проверки, что загрузчик — автор/соавтор/участник команды игры. Любой залогиненный заливает неограниченно фото в галерею любой игры (спам, DoS хранилища). Rate-limit на загрузку нет.
- **Фикс:** `IsUserManager || участник активного прохождения` перед сохранением + upload rate-limiter.

### 2. Автор не может удалить чужие фото (несоответствие прав) ✅ подтверждено
`svc_photo.go:37-53` vs `hnd_photo.go:231-258`
- Хендлер даёт автору `isManager`, но `PhotoService.Delete` проверяет только `photo.UserID == userID` **или строку в `co_authors`** — у автора её нет → «нет прав на удаление фото» (403/500).
- **Фикс:** в `PhotoService.Delete` также разрешать `game.AuthorID == userID`.

### 3. StartTesting — гонка переиспользования тестовой команды ✅ вероятно
`svc_play.go:439-466`
- Два параллельных `StartTesting` для одного юзера+игры: оба видят orphan-команду `_test_<id>` с `passingCount==0`, оба используют её → две `GamePassing` для одной команды.
- **Фикс:** `ON CONFLICT`/уникальный индекс на имя команды, или `FOR UPDATE` на строку команды.

### 4. Tournament AddGame не создаёт прохождения для уже поданных команд ✅ вероятно
`tournament/service.go:79, 222-245`
- `Apply` создаёт passings только для игр, существующих на момент заявки. Если автор добавляет игру позже — зарегистрированные команды не получают passing и не начисляются очки в новой игре.
- **Фикс:** в `AddGame` создавать `StatusPending` passings для всех команд турнира в той же транзакции.

### 5. RemoveGame не сбрасывает `tournament_scored`/`tournament_points` ✅ вероятно
`tournament/service.go:109-174`
- После RemoveGame passing остаётся `tournament_scored=true, finished`. Если игру пере-добавить, команда не сможет пройти её заново (finished→finished невалиден) и не пересчитается.
- **Фикс:** в RemoveGame сбросить флаги на passing (или удалить passing).

### 6. SubmitTestCode вызывает `onCommit()` внутри транзакции ✅ подтверждено
`svc_play.go:520-522`
- Нарушает паттерн onCommit-после-commit (в SubmitCode колбэк вызывается после `db.Transaction`). Сейчас `onGameFinished` nil (тест-режим), но это мина.
- **Фикс:** вынести `onCommit()` за транзакцию.

### 7. Perf — `GetCurrentProgressForUpdate` прелоадит всю графу ответов на всех горячих путях ✅ подтверждено
`svc_progress.go:99-109`
- `Preload("Level.Questions.Answers")` на каждый SubmitCode/UseHint/SubmitFile/AcceptBlackboxAnswer — полный граф (включая все коды ответов) нужен только SubmitCode.
- **Фикс:** `GetCurrentProgressForUpdateWithAnswers` только для SubmitCode; остальным — без Preload.

### 8. Perf — Valkey `DeleteByPrefixWithCtx` удаляет по одному ключу ✅ подтверждено
`valkey.go:191-214`
- `games:list:` инвалидация (на каждую публикацию/правку) SCANирует и шлёт `Del` по одному ключу → сотни round-trips. `DeleteByPrefix` (без ctx) уже батчит по 100.
- **Фикс:** батчить `Del(keys...)` по 100 и в ctx-варианте.

### 9. Perf — Export грузит ВСЕ попытки (коды) для результатов ✅ подтверждено
`export/repository.go:45-56` (`Preload("Progresses.Attempts")`)
- CSV/Excel/PDF тянут все `attempts` (до 10k символов кода каждый) для finished passings; Excel строит .xlsx в памяти. Большая игра → десятки МБ heap.
- **Фикс:** `COUNT(*) GROUP BY level_progress_id` вместо прелоада; Excel — `StreamWriter`.

---

## 🟡 Средние

### UX
- **H1** — hint-параметр `?hint=` не ставится в redirect (`hnd_gameplay.go:274`), ветка JS `window.location.search.get('hint')` мёртвая (после reload hint всё же виден через flash, но JS-путь не работает). Добавить `?hint=` в redirect.
- **H4** — финальный шаг wizard без защиты от двойного submit (создание дублей игр). `data-no-loading` снят → нужен disable+spinner на финальном шаге.
- **M1** — ошибки UseHint рендерят полный HTML в тост (`renderGameplayError` возвращает 400 с HTML). Для AJAX вернуть plain-text/JSON.
- **M2/M3** — push-кнопки не синхронизированы с реальной подпиской на загрузке; i18n-ключи `push.*` не совпадают с `data-i18n-push-*`.
- **M4** — `JSON.parse` в WS-обработчиках team-chat/layout/logs без try/catch — битый фрейм убивает конвейер.
- **M5** — CSRF-враппер fetch ломается при `Headers`-экземпляре.
- **M6** — notes add: кнопка остаётся со спиннером после network error.
- **M7** — Prev-кнопка пагинации логов захардкожена `disabled`.
- **M8/M9** — delete-модалка без focus-trap; photo-lightbox без dialog-ролей.
- **M10** — результаты поиска в invitations-new — div без клавиатуры (в co_authors исправлено).
- **M11/M12** — отсутствуют label у note-textarea/кода; monitor/logs `connection-status` без `role="status"`.
- **M14** — view-toggle race с preference-fetch; `aria-pressed` хардкод `false`.
- **M15** — контраст `text-yellow-600`/`text-green-600` (2.9-3.3:1) — поднять до 700.
- **M16** — `previewCache` `JSON.parse` без try/catch.
- **L6** — QR-код 2FA через `api.qrserver.com` (внешняя зависимость/приватность).

### Корректность
- **C-1** — `UpdateGameWithCover` и `GameHandler.Delete` маскируют все ошибки как 403 и показывают сырой текст БД.
- **C-2** — `ReviewService.Create` валидирует 1-10, хендлер 1-5 — рассинхрон (5 — фактический максимум).
- **C-3** — `Apply` (game) маскирует ошибку БД как «заявка уже подана».
- **C-4** — `AcceptAnswer` после успешного действия требует `?game_id=` → 400 на успешную мутацию.
- **C-5** — `SettingsPage` дублирует defaultGameSetting вручную.
- **C-6** — `GameService.Delete`/`AdminDelete` дублируют проверку владельца.
- **C-7** — `ListByGamePaginated` переиспользует цепочку GORM для Count и Find.
- **C-8** — `ChangeCaptain` не обновляет team_members (старый капитан остаётся, новый не добавляется).
- **C-9** — `AddMember`/`RemoveMember` не транзакционны, INSERT без ON CONFLICT.
- **C-10** — `Vote` не пере-проверяет `IsOpen` после `FOR UPDATE` внутри tx.
- **C-11** — `PhotoService.List` прелоадит полного User (включая email).
- **C-12** — `CountSearch`/`SearchPaginated` (admin) не экранируют LIKE wildcards.
- **C-13** — `UseHint` при уровне без вопросов всё равно списывает подсказку.

### Security
- **S-1** — `/metrics` и `/swagger` на `OptionalAuth` (роль из claims) — пониженный админ видит метрики до expiry. Через `AuthRequired`+`AdminRequired`.
- **S-2** — `GET /games/:id/testing/start` — state-changing GET.
- **S-3** — Host-заголовок в `BaseURL` шаблона (`hnd_game.go:217`).
- **S-4** — логин per-IP только (нет per-account счётчика).

---

## ⚡ Варианты оптимизации (по приоритету)

### Быстрые (дни)
1. **H2** — guard `window.showToast` (чинит сломанный SSE).
2. **#2** — автору разрешить удаление чужих фото (1 условие).
3. **#6** — вынести `onCommit()` из транзакции SubmitTestCode.
4. **H1** — `?hint=` в redirect UseHint.
5. **H4** — защита финального шага wizard.
6. **C-2** — единая константа maxRating (5).
7. **M7** — Prev-кнопка логов.

### Средние
8. **#1** — authz загрузки фото + upload rate limit.
9. **#3/#4/#5** — StartTesting race, AddGame passings, RemoveGame reset.
10. **#7/#8/#9** — per-path preload, Valkey DEL batch, export COUNT.
11. **C-1/C-3/C-4** — классификация ошибок, return gameID.
12. **M2-M6, M8-M16** — push sync, WS try/catch, fetch wrapper, a11y.
13. **C-8/C-9/C-10** — ChangeCaptain tx, AddMember ON CONFLICT, Vote re-check.

### Крупные
14. **#9 (полный)** — экспорт через StreamWriter + SQL-агрегаты.
15. **S-1** — /metrics /swagger через AuthRequired.
16. **C-12** — LIKE-экранирование в admin-поиске.

---

## ✅ Что сделано хорошо (подтверждено)

- **Безопасность:** параметризованный SQL (GameSnapshot/CalculateResults — все через `?`), role из БД в AuthRequired, атомарный refresh, advisory-локи, Origin-guard на /api, SameSite=Strict, sanitization на ввод/вывод, WS/SSE origin exact-match, voting authz (manager/participant), команды/приглашения — капитан-only.
- **Корректность:** onCommit после commit (кроме SubmitTestCode), атомарные guards, идемпотентность турниров/рейтингов, reviews ON CONFLICT, defaultGameSetting.
- **Perf:** debounce-снапшот, singleflight, typed Valkey, CheckTimeouts batch+rollback, AdvanceToNextLevel одним запросом, CheckTeamMembership→gameID, UseHint hint-only, неблокирующий WS/SSE.
- **UX:** diff-рендер монитора, reconnecting WS, focus-trap модалок, контраст, server-side view preference, offline/PWA.
- **Тесты:** 62 файла, ~615 функций; pass 24-фиксы покрыты (APIOriginGuard, CheckTimeouts, ties, ClaimAndCreate).

---

## ❌ Ложные срабатывания (проверено)

| Заявка | Вердикт |
|---|---|
| Monitor disqualify 302 показывается как ошибка | ❌ **Ложь** — fetch следует за redirect, финальный 200 → r.ok true |
| Список игр /metrics — критично | ❌ только S-1 medium (admin data routes защищены) |
| H3 «все ошибки 403» — перепроверено | Частично: UpdateGameWithCover/Delete действительно 403 (C-1), но это не критично |

---

## Приоритеты следующей волны

1. **H2** — сломанный SSE (ReferenceError).
2. **#2** — автор не может удалить фото.
3. **#1** — authz загрузки фото.
4. **#6** — onCommit SubmitTestCode.
5. **#3/#4/#5** — StartTesting/AddGame/RemoveGame.
6. **#7/#8/#9** — perf (preload, Valkey DEL, export).
7. **H1/H4/M1-M16** — UX-партия.
