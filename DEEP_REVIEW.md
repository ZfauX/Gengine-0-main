# Deep Review Gengine-0 — 6 августа 2026 (pass 27 — итоговый статус)

## Резюме

Pass 27 — новое глубокое ревью нерассмотренных областей (`@reviewer` × 2 + `@security`, параллельно):
- **internal/pkg** (websocket hub, cache, middleware, i18n) — 20 находок (1 CRITICAL, 4 HIGH)
- **monitor/export/notification/calendar/social** — 19 находок (1 CRITICAL, 3 HIGH)
- **security** по auth/2FA/webauthn/team/ws/push — 18 находок (4 HIGH)

Все критические и высокие закрыты в коммите `512a7fe`. Ниже — что исправлено.

## Исправлено в pass 27

### 🔴 Критично
- **WS уведомлений не работал вовсе** (N1): `NotificationsWS` привязывал контекст к `c.Request.Context()` с `defer cancel()` — при возврате handler'а соединение мгновенно закрывалось. Переведено на `context.Background()` + cancel в goroutine (как в monitor).
- **Обход лимитов WS** (P1): handler-defer и `writePump` оба вызывали `UnregisterClient` → счётчики декрементировались дважды. Теперь `registered` флаг в `Client` делает unregister идемпотентным.

### 🟠 Высокие
- **Monitor poller race** (M1): окно между `cancel()` и удалением poller из map позволяло прицепить подписчика к отменённому сборщику (тот висел без данных). Удаление из map теперь атомарно с изменением subscribers.
- **SSE без начального снапшота** (M2): новый подписчик ждал следующего изменения состояния. Теперь сразу получает текущий `lastData`.
- **2FA-enable без пароля** (S2): атакующий с украденной сессией мог привязать свой TOTP-секрет. Добавлено подтверждение паролем (как в disable).
- **Push SSRF неполный** (S4): `isPrivateIP` теперь раскрывает IPv4-mapped IPv6 (`::ffff:10.x.x.x`) и блокирует CGNAT `100.64.0.0/10` (RFC 6598).
- **Valkey rate-limit fail-closed** (P3): при сбое Redis каждый запрос получал 429 → полный outage. Переведено на fail-open с логированием.
- **OptionalAuth stale role** (P7): при ошибке загрузки роли из БД JWT-claim доверялся (пониженный админ сохранял привилегии). Теперь fail-closed.
- **`/metrics` и `/swagger` без 2FA** (S5): добавлен 2FA step-up (как на `/admin/*`) — украденный JWT не даёт метрики и документацию.
- **WebAuthn + 2FA сломан** (S11): passkey-логин redirect-ил на `/auth/2fa/verify` (требует JWT, которого ещё нет) → вечный цикл на login. Исправлено на `/auth/2fa/login` (pending-session flow).

### 🟡 Средние
- **Reset-code в логах** (S7): код сброса не логируется вовсе (раньше — первые 4 символа, 16 бит энтропии).
- **OAuth state не привязан к provider** (S8): state для Yandex не принимается в callback VK.
- **`isHTTPS` доверял X-Forwarded-Proto** (S9): только при реально доверенном прокси (`ClientIP() != RemoteIP()`).
- **ChatWS без rate-limit** (M5): per-connection token bucket 10 сообщений/5 сек.
- **Push body leak** (N2): `resp.Body` закрывается даже при ошибке.
- **APIEmailSave затирал настройки** (N3): partial update через `*bool` + merge с текущими.
- **Follow race** (S1): `OnConflict(DoNothing)` вместо check-then-insert (500 на гонке).
- **Email-утечка в подписках** (S2): `users.email` убран из subscriptions/followers.
- **Logs per_page** (M4): clamp до 100 перед расчётом totalPages.
- **ExportTeamResultsCSV — мёртвый endpoint** (E1): маршрут зарегистрирован.
- **CSV-import size guard** (E2): `http.MaxBytesReader` до FormFile (Content-Length больше не обходится).
- **Re-import дублирует вопросы** (E3): существующие вопросы/ответы заменяются в транзакции (`Unscoped` — DB-каскад).
- **`GetHealthStatus` data race** (P2): `conns_per_ip` копируется перед JSON (иначе concurrent map panic в `/admin/ws-health`).

## Осознанные компромиссы (не баги)
| Пункт | Статус |
|---|---|
| **DeleteUser не удаляет игры-авторства** | FK/продуктовое решение: игры автора удаляются отдельно (админ) |
| **SubmitCode level reuse / AdvanceToNextLevel passing reuse** | Частично оптимизировано; дальнейшее — рискованно для стабильности |
| **Admin-пагинация без query-фильтра** | Сбрасывает фильтр при листании — задокументировано |
| **levels-show Leaflet** | Шаблон не рендерится в Go (мёртвый) |
| **2FA-флаг в client-side cookie-сессии** | Standard: encrypted cookie store; bound, short-lived step-up обсуждается |

## При деплое
```
make migrate   # применить 000025–000036
make build && make test-integration
```

## Статус
Все критические/высокие пункты глубоких ревью pass 21–27 закрыты. Остались редкие оптимизации и продуктовые решения — не баги.
