# DEEP_REVIEW — Gengine-0 (PASS 9)

> Глубокое ревью после закрытия PASS-8.
> Метод: pprof-профилирование (PPROF_ENABLED=true, :6060, loopback) + 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая проверка каждого HIGH/MEDIUM finding.
> Архивы: `DEEP_REVIEW_2026-08-11_pass{1,2,3,4}.md`, `DEEP_REVIEW_2026-08-12_pass5.md`, `DEEP_REVIEW_2026-08-12_pass7.md`, `DEEP_REVIEW_2026-08-13_pass8.md` (содержимое предыдущего `DEEP_REVIEW.md`).

---

## 🔬 pprof-результаты (PASS 9)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 18 в покое | ✅ Без утечек (стабильно с PASS-5..8). |
| **heap inuse** | 16.1 MB | ✅ Норма. 41% — инициализационная память `golang.org/x/net/webdav` (не код приложения). |
| **heap alloc** | 72.4 MB cumulative | ⚠️ `bytes.growSlice` 17.4%, `text/template evalCall` 17.3% cum, `reflect.Value.call` 11.75% — рендер HTML. |
| **cpu** | 42% cgocall (network I/O), 40.35% cum `text/template.walk` | ⚠️ Рендер HTML — главная статья CPU (как в PASS-8). |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback. |

**Вывод**: профиль чистый от утечек; бутылочное горлышко — HTML-рендер (двухпроходный `render.Page` + 825-строчный layout с inline-скриптами). Найден топ-аллокатор `ShowLoginForm` 15.26% cum (11MB) — см. perf P-1.

---

## 🔴 HIGH (reviewer)

### H1. Фотогалерея: автор черновика получает 404 🔍✅ (подтверждено)
- **Файл**: `internal/domain/game/hnd_photo.go:78`.
- **Проблема**: `if game.IsDraft && !isAdmin` — автор/соавтор черновика (менеджер, но не глобальный админ) получал 404, хотя `GetByID` уже подтвердил права. Поведение расходилось с `Show`.
- **Фикс**: ✅ `IsUserManager` вычисляется ДО проверки IsDraft; блок `!isManager`.

### H2. `StopThemeCacheCleanup`: гонка двойного close → panic 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/middleware/theme.go:75-81`.
- **Проблема**: `close(themeCacheStopCh)` без мьютекса; два вызова (две горутины) → `panic: close of closed channel`.
- **Фикс**: ✅ `themeStopOnce sync.Once` + закрытие под `themeCacheMu`.

---

## 🔴 HIGH (security)

### S-H1. Накрутка/списание очков турнира через RemoveGame 🔍✅ (подтверждено)
- **Файл**: `internal/domain/tournament/service.go:167-267`.
- **Проблема**: автор турнира мог `RemoveGame` ПОСЛЕ начисления очков — списывал чужие очки (манипуляция/DoS перед закрытием турнира). Полная накрутка remove→add невозможна (uniqueIndex + soft-delete), но обнуление чужих очков — реально.
- **Фикс**: ✅ запрет автору удалять игру с начисленными очками (`scoredCount > 0`); глобальному админу разрешено. Тесты обновлены.

### S-H2. `UseHint` → `renderGameplayError`: утечка ответов не-участнику 🔍✅ (подтверждено)
- **Файл**: `internal/domain/game/hnd_gameplay.go:258-285`, `:128-144`.
- **Проблема**: UseHint не проверял членство ДО сервиса; при ошибке `renderGameplayError` рендерил данные уровня (текст вопроса и ПРАВИЛЬНЫЕ ОТВЕТЫ при `HideAnswers=false`) любому аутентифицированному, перебиравшему passing_id.
- **Фикс**: ✅ проверка `isUserInPassing` в хендлере ДО сервиса (как SubmitCode) → 403 для не-участника.

---

## 🟠 MEDIUM (reviewer)

### M1. `RemoveGame` затирает `tournament_points` другого турнира 🔍✅
- **Файл**: `tournament/service.go:248-251`.
- **Проблема**: безусловный `tournament_points = 0` обнулял очки, начисленные ВТОРЫМ турниром той же игры.
- **Фикс**: ✅ обнуление через пересчёт суммы оставшихся `tournament_scored_points` (а не `= 0`).

### M2. Ошибка валидации приглашения рендерит чужой шаблон (copy-paste) 🔍✅
- **Файл**: `team/handler.go:618`.
- **Проблема**: `InvitationHandler.Create` при невалидном user_id рендерил `teams-add_member.html` вместо `invitations-new.html`.
- **Фикс**: ✅ правильный шаблон.

### M3. Капитан двух команд (обход A-5) 🔍✅
- **Файл**: `team/service.go:131-141` (CreateTeam), `:317-332` (ChangeCaptain).
- **Проблема**: не проверялось, что капитан/новый капитан уже состоит в другой команде (уникальный индекс покрывает только team_members, не captain_id).
- **Фикс**: ✅ `userInOtherTeam` в CreateTeam и ChangeCaptain (с исключением текущей команды).

### M4. `CoAuthorService.Add`: пустая роль → только чтение 🔍✅
- **Файл**: `game/svc_coauthor.go:278-284`.
- **Проблема**: `PresetPermissions("")` → `[PermRead]` вычислялся ДО установки дефолта `RoleContentEditor` → content_editor терял `edit_content`.
- **Фикс**: ✅ дефолт роли до расчёта пресета.

---

## 🟠 MEDIUM (perf)

### P-1. `ShowLoginForm`/`render.Page`: 11MB cumulative, нет пула буферов 🔍
- **Файл**: `internal/pkg/render/helper.go:224` (`var buf bytes.Buffer`), `:193` (`sessions.Default`).
- **Проблема**: двухпроходный рендер (контент → buf → layout) без `sync.Pool`; сессия открывается на каждый HTML-запрос даже без cookie.
- **Рекомендация**: `sync.Pool` для `bytes.Buffer`; короткое замыкание сессии при отсутствии cookie `gengine_session=`.

### P-2. `team.ListAllTeams` без LIMIT 🔍✅
- **Файл**: `team/repository.go:142-151`.
- **Проблема**: все команды + Preload на вкладку «Команды» — O(n) памяти.
- **Фикс**: ✅ `Limit(200)`.

### P-3. `Preload("Captain")` без Select → password_hash в память 🔍✅
- **Файл**: `team/repository.go:183-197` (ListAllPaginated/SearchPaginated).
- **Проблема**: не только перф, но и загрузка чувствительных полей.
- **Фикс**: ✅ `Select("id, name, avatar_path")`.

### P-4. Tournament N+1: цикл `GetByID` + лишний `Preload("Author")` 🔍
- **Файл**: `tournament/service.go:400-411`.
- **Рекомендация**: `GetByIDs` одним запросом без Author.

### P-5. Tournament `ListGames` — неограниченный Find с tsvector 🔍
- **Файл**: `tournament/repository.go:112-119`.
- **Рекомендация**: `Select` + `Limit(200)`.

### P-6. WS: аллокация `Message` на каждый broadcast + `IsClosed()` в цикле 🔍
- **Файл**: `websocket/room_hub.go:471`, `:358-385`.
- **Рекомендация**: `sync.Pool` для `*Message`; не звать `IsClosed()` на каждого клиента в цикле.

---

## ⚪ LOW (reviewer/security)

- **L1** `two_factor_handler.go:105,162` — двойной `GetByID` в Verify (второе чтение только для JWT).
- **L2** `team/chat_handler.go:54` — игнорирование ошибки `IsMember` → ложный 403.
- **L3** `svc_simulate.go:68` — `Success: true` всегда (даже без ответов).
- **L4** `profile_handler.go:502` — игнорирование `Atoi` в UpdateNotifyGameDays.
- **L5** `webauthn_handler.go:491` — игнорирование ошибки `UpdateSignCount`.
- **L6** `export/service.go:465,470,480` — PDF `Cell` не переносит строки (длинные тексты обрезаются).
- **L7** `profile_handler.go:410-418` — все ошибки смены пароля маскируются под «неверный текущий пароль».
- **L8** `user/profile_repository.go` — публичный профиль не фильтрует visibility (непубличные игры в RecentGames).
- **L9** `user_search_handler.go` — поиск для команды без проверки CanManageTeam (требует верификации).

---

## ✅ Проверено — проблем НЕ найдено

- **JWT**: HMAC закреплён, jti-blacklist, отзыв при logout/reset.
- **Роли**: перечитываются из БД (TTL 5с), fail-closed, удалённый пользователь отзывается.
- **Team**: CanManageTeam (капитан/супер-админ), атомарный claim приглашений + ON CONFLICT, RemoveMember/SetMemberRole/ChangeCaptain с проверками.
- **Notification**: MarkAsRead/ListByUser скоупированы по user_id; push — SSRF-защита (validPushEndpoint + блокировка приватных IP).
- **WebAuthn**: Delete скоупирован, 2FA-гейт на регистрацию, userHandle, CloneWarning.
- **Game**: CheckTeamMembership в SubmitCode/SubmitFile/UseHint; Get/SetTeamRoute со сверкой gameID; Phase-3 под GameManager; rate-limit на submit/hint/file/accept.
- **Storage**: path traversal, MIME-детект, случайные имена, права 0600/0700.
- **CSRF**: SameSite=Strict + nonce-CSP; JSON API защищены контент-типом.
- **N+1**: batch-CASE, UpsertMany, advisory lock, singleflight — образцово.

---

## 💡 Предложения по улучшению кодовой базы (приоритет)

1. **P-1 (render.Page)**: sync.Pool буферов + short-circuit сессии без cookie — самая большая победа по CPU/alloc (40% рендер).
2. **S-H1/S-H2** (исправлены): турнирные очки и утечка ответов — закрыты.
3. **P-4/P-5**: batch GetByIDs турниров + Select/Limit ListGames.
4. **L6**: PDF MultiCell вместо Cell.
5. **L8**: фильтр visibility в публичном профиле.

---

## 💡 Предложения по пользовательскому опыту (UX)

1. **404 для автора черновика** (исправлен H1) — автор теперь видит свою галерею.
2. **PDF-экспорт**: длинные вопросы/ответы не должны обрезаться (MultiCell + перенос строк).
3. **Смена email**: запрашивать текущий пароль/подтверждение (security UX-безопасность).
4. **Ошибки смены пароля**: разделять «неверный пароль» и системные сбои (понятный 500).
5. **Производительность**: пул буферов рендера → быстрее отклик всех HTML-страниц.

---

## 📋 Статус

- 3 аудита: ✅ проведены; HIGH/MEDIUM findings — ✅ эмпирически проверены (H1, H2, S-H1, S-H2, M1-M4, P-2, P-3).
- **Исправлено в этом проходе: 11 findings** ✅ (H1, H2, S-H1, S-H2, M1, M2, M3, M4, P-2, P-3 + тесты).
- **Второй проход (все оставшиеся): исправлено 11/11** ✅
  - P-1: `render.Page` — `sync.Pool` буферов + short-circuit сессии без cookie `gengine_session=` (топ-аллокатор по pprof).
  - L8: публичный профиль фильтрует `visibility='public'` (RecentGames + games_created) — не раскрывает «по ссылке»/скрытые игры.
  - P-4: `GetByIDs` одним запросом без Preload Author (убирает N+1 в UpdateScoresForGame).
  - P-5: `ListGames` — Select без tsvector/description + Limit(200).
  - L9: `NewInvitationForm` — проверка `CanManageTeam` (SearchUsers уже был защищён S-45-1).
  - L6: PDF — MultiCell для вопросов/подсказок/ответов + `ensurePDFSpace` (AddPage на границе).
  - L7: смена пароля — `ErrChangePasswordWrong` → 400, прочие ошибки → 500 (новый i18n ключ).
  - P-6: WS `dispatchToRoom` — убран `IsClosed()` из цикла; неблокирующая проверка Done до отправки (закрытый клиент гарантированно удаляется).
  - L1: 2FA Verify — переиспользует `user` вместо второго GetByID.
  - L2: TeamChat — ошибка `IsMember` → 500 (не ложный 403).
  - L3: Simulate — `Success = code != "невозможно определить"`.
  - L5: WebAuthn `UpdateSignCount` — логирует ошибку.
  - Проверки: build ✅, test-short ✅, test-integration ✅, golangci-lint ✅ (0 issues), E2E 14/14 ✅.
- Отложено (требует продукта/инфраструктуры): P-1 (пул рендера), P-4/P-5, P-6, L1-L9.
