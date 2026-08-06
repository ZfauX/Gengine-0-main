# Deep Review Gengine-0 — 6 августа 2026 (pass 26)

Повторный глубокий аудит после pass 21–25.

**Методология:** 5 параллельных агентов (security, perf, code, UX, tests) + **ручная верификация** критических находок.

**Легенда:** 🔴 критично · 🟠 высоко · 🟡 средне · ✅ хорошо · ❌ ложное

---

## Итог

Найден **1 критичный регресс, незамеченный в pass 21–25**: весь realtime (WebSocket уведомления, монитор, логи, командный чат) **мёртв в браузере** из-за вызова `createReconnectingWebSocket` на parse-time при `defer`-загрузке app.js. Плюс 2FA-disable может блокировать аккаунт при неверном пароле. Это главные фиксы следующей волны.

---

## 🔴 Критично

### 1. Realtime мёртв: WS вызывается на parse-time до `defer` app.js ✅ подтверждено
- `static/js/app.js` подключён с `defer` (`layout.html:148`). Inline-скрипты выполняются **при парсинге** и вызывают `window.createReconnectingWebSocket` (undefined, т.к. app.js ещё не выполнился) → TypeError:
  - `layout.html:640` — уведомления (бэлл молчит)
  - `monitor-page.html:261` — мониторинг полностью мёртв
  - `logs-list.html:73` — live-логи не стримятся
  - `team-chat.html:137` — командный чат мёртв (только chat-page работает — connect внутри fetch)
- **Фикс:** обернуть каждый вызов в `document.addEventListener('DOMContentLoaded', ...)`, либо guard + retry, либо перенести фабрику WS из defer в head-скрипт.

### 2. 2FA-disable блокирует аккаунт при неверном пароле ✅ подтверждено
`two_factor_handler.go:468`
- Для проверки пароля вызывается полный `authService.Login` → при неверном пароле `AtomicIncrementFailedAttempts` → после 5 попыток аккаунт блокируется на 30 мин.
- **Фикс:** `bcrypt.CompareHashAndPassword` напрямую (юзер уже загружен).

---

## 🟠 Высокие

### 3. Leaflet-карта уровня не рендерится ✅ подтверждено
`levels-show.html:30-48`, `layout.html:144`
- `L.map()` на parse-time, а leaflet.js с `defer` → `typeof L === 'undefined'` → пустой блок. Плюс `IncludeLeaflet` не установлен в level-хендлере.
- **Фикс:** инициализация карты на DOMContentLoaded + `IncludeLeaflet: true`.

### 4. `DeleteUser` оставляет сирот во многих таблицах ✅ вероятно
`user/repository.go:365-385`
- Чистятся только 7 таблиц; НЕ чистятся: `games` (автор), `team_members`, `user_achievements`, `follows`, `chat_messages`, `notifications`, `game_passings`, `level_progresses`, `attempts`, `logs`, `reviews`, `notes`, `photos`, `co_authors`, `invitations`.
- **Фикс:** каскады через FK `ON DELETE CASCADE` в миграциях или явная очистка в транзакции.

### 5. Refresh fingerprint mismatch не отзывает семью ✅ вероятно
`user/service.go:269-272`
- Несовпадение fingerprint → ошибка, но токен остаётся активным. Повтор с другого устройства не детектится как кража.
- **Фикс:** `RevokeAllByFamily` + audit при mismatch (как при reuse).

### 6. `UpdateProfile` — гонка на уникальный email ✅ вероятно
`profile_service.go:101-116`
- Count-then-Update; два параллельных сохранения на новый email → raw DB error.
- **Фикс:** ловить `ErrDuplicatedKey`/23505 → `ErrEmailTaken`.

### 7. OAuth создание пользователя — гонка (нет upsert) ✅ вероятно
`user/service.go:732-742`
- Два параллельных OAuth-колбэка для нового email → unique violation.
- **Фикс:** `ON CONFLICT (email) DO NOTHING` + re-read.

### 8. `OptionalAuth` не перечитывает роль — пониженный админ видит черновики ✅ вероятно
`middleware/auth.go:94-107`
- `/games/:id`, `/api/games/:id/stats`, `/users/:id` через OptionalAuth (роль из claims). Пониженный админ видит draft/private игры до expiry.
- **Фикс:** перечитывать роль в OptionalAuth (как AuthRequired) или убрать IsAdmin-обход в хендлерах.

### 9. Публичные `/api/*` без лимитов ✅ вероятно
- `/api/search/games`, `/api/users/search`, `/api/v1/calendar` — только глобальный 100/мин. Enumeration/scraping.
- **Фикс:** dedicated IP-лимитеры (10-20/мин).

### 10. Контраст: title карточки игры в dark ✅ подтверждено
`games-list.html:82` — `text-gray-800` без `dark:text-*` на тёмном фоне ≈ 1.2:1 (нечитаемо). Автор/дата без dark-вариантов.

---

## 🟡 Средние

### Code
- **M-1** UseHint: `defaultGameSetting(passing.GameID)` где GameID=0 (`Select("status")`) — заменить на `gameID`.
- **M-2** Дубли `UpdateProfile` (UserService сброс email_verified vs ProfileService проверка unique) — консолидировать.
- **M-3** `Scan` вместо `First` — мёртвая ветка `ErrRecordNotFound` в GetThemeSettings/GetGamesView.
- **M-6** `FinishRegistration` не re-check 2FA-флаг.
- **M-5** Register возвращает 200 «успех» при инфраструктурной ошибке.
- **M-4** Push SSRF: DNS rebinding не закрыт.

### Perf
- **Stale `RatingValue`** в `game:%d`/`games:list:*` после отзыва — инвалидировать.
- **Calendar** не кэшируется (месяц/год на каждый запрос).
- **SubmitCode** пере-гружает level graph (GetCurrentProgressForUpdate light → SubmitCodeWithTx reload).
- **AdvanceToNextLevel** повторный load passing (caller уже знает gameID).
- **CheckTimeouts** двойной load passing.
- **Monitor poller** subscribe/unsubscribe race (orphan SSE).
- **AutocompleteSearch** без кэша/лимита, `games.name` без trgm-индекса.

### UX
- **M1** Confirm-OK «Удалить» для non-деструктивных действий (Accept/Reject/Start команд).
- **M2** Пагинация админки теряет query-фильтр; search не urlencoded.
- **M3** delete-modal keydown-листенер копится при каждом открытии.
- **M4** Уведомления без пагинации (>50 недоступны).
- **M5** Push-настройки без disable/status на странице настроек.
- **M6** Flash-дубли + нет role="alert".
- **M7** Calendar prev/next race (устаревший ответ).
- **M8** Поля кода без label.

---

## ⚡ Варианты оптимизации (по приоритету)

### Быстрые (дни)
1. **Крит #1** — DOMContentLoaded для WS-инициализаций (4 файла).
2. **Крит #2** — bcrypt-проверка в 2FA disable.
3. **#3** — Leaflet DOMContentLoaded + IncludeLeaflet.
4. **#10** — dark: title/автор/дата.
5. **M-1** — gameID в defaultGameSetting.
6. **#8** — роль в OptionalAuth.

### Средние
7. **#5/#6/#7** — refresh family revoke, UpdateProfile/OAuth upsert.
8. **M-2/M-6** — консолидация UpdateProfile, FinishRegistration 2FA.
9. **Perf** — инвалидация RatingValue, кэш календаря, trgm games.name.
10. **UX M1-M8** — confirm-OK, пагинация, listener leak, flash.

### Крупные
11. **#4** — каскады DeleteUser (миграция 000036).
12. **#9** — публичные лимитеры.
13. **Perf** — SubmitCode level reuse, AdvanceToNextLevel passing reuse.

---

## ✅ Что сделано хорошо (подтверждено)

- **Безопасность**: AuthRequired роль из БД, атомарный refresh+семьи, advisory-локи, параметризованный SQL, CSP nonce на всех 43 inline, Origin-guard, SameSite=Strict, sanitization, LIKE-escape, upload manager-gated + sniffing.
- **Корректность**: onCommit после commit (все пути), идемпотентность турниров/рейтингов, reviews ON CONFLICT, Vote IsOpen re-check, UseHint no-questions.
- **Perf**: debounce-снапшот, singleflight, typed Valkey, batched DEL, listing cache conditional, CheckTimeouts ORDER BY, неблокирующий WS/SSE.
- **UX**: diff-рендер монитора, reconnecting WS (статус-модель), focus-trap, контраст, server-side preference, offline/PWA.
- **Тесты**: 62 файла; pass-25 фиксы частично покрыты.

---

## Приоритеты следующей волны

1. **Крит #1** (realtime мёртв) и **Крит #2** (2FA lockout).
2. **#3** (Leaflet), **#10** (dark title), **M-1** (gameID).
3. **#4-#9** (удаление сирот, refresh revoke, upsert, OptionalAuth, лимитеры).
4. **Perf** (RatingValue, calendar, trgm).
5. **UX M1-M8**.
6. **Тесты**: OAuth unskip, 2FA handler, pass-25 фиксы (photo authz, Apply, AddGame/RemoveGame).
