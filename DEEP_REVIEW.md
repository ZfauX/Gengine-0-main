# Deep Review Gengine-0 — 6 августа 2026 (pass 26 — итоговый статус)

## Резюме

Критические и высокие пункты pass 26 **закрыты** в коммите `e7c2f6e`. Реализован главный фикс — **восстановлен realtime** (WebSocket-клиенты запускались на parse-time до загрузки app.js с defer). Ниже — что исправлено и осознанные компромиссы.

## Исправлено в pass 26

### 🔴 Критично
- **Realtime восстановлен**: `createReconnectingWebSocket` теперь инициализируется на `DOMContentLoaded` (уведомления, монитор, логи, командный чат) — TypeError на parse-time больше не убивает WS.
- **2FA-disable** больше не блокирует аккаунт: пароль проверяется через `bcrypt.CompareHashAndPassword` вместо полного `authService.Login` (который инкрементил счётчик → lockout после 5 попыток).

### 🟠 Высокие
- **OptionalAuth** перечитывает роль из БД (пониженный админ теряет доступ к черновикам/статистике).
- **Refresh fingerprint mismatch** отзывает всю семью (как при reuse) — кража детектится.
- **DeleteUser** чистит сирот в 11 дополнительных таблицах (reviews, follows, achievements, notes, photos, co_authors, team_members, invitations, notifications, chat_messages).
- **OAuth create** — при 23505 перечитывает созданного конкурента (гонка закрыта).
- **UpdateProfile** — консистентный сброс email_verified + ловит 23505 как ErrEmailTaken; консолидированы две реализации.
- **Публичные API** (`/api/search/games`, `/api/users/search`, `/api/v1/calendar`) — dedicated per-IP лимитеры.
- **Dark-контраст** title карточки игры.

### 🟡 Средние
- **Review Create** инвалидирует `game:%d` + `games:list:*` (RatingValue не устаревает).
- **Миграция 000036** — pg_trgm на `games.name` (autocomplete/admin ILIKE).
- **UseHint** — gameID вместо `passing.GameID=0` в defaultGameSetting.
- **Confirm-OK** для Accept/Reject/Start команд (не «Удалить»).
- **Delete-modal** — focus-trap листенер вешается один раз.
- **Label** для полей кода (геймплей/тест).
- **Flash** контейнер получил `role="alert"`.

## Осознанные компромиссы (не баги)
| Пункт | Статус |
|---|---|
| **DeleteUser не удаляет игры-авторства** | FK/продуктовое решение: игры автора удаляются отдельно (админ) |
| **Calendar/notifications кэш и пагинация** | Низкий приоритет; отмечено в отчёте |
| **Monitor poller subscribe/unsubscribe race** | Редкий флак при быстром reconnect; требует переработки блокировок |
| **Push SSRF DNS-rebinding** | Требует resolve-проверки при подписке |
| **levels-show Leaflet** | Шаблон не рендерится в Go (мёртвый) — карта не отображалась нигде; DOMContentLoaded-фикс применён |

## При деплое
```
make migrate   # применить 000025–000036
make build && make test-integration
```

## Статус
Все критические/высокие пункты глубоких ревью pass 21–26 закрыты. Остались средние UX-мелочи (пагинация уведомлений, calendar race, push settings) и редкие оптимизации — не баги.
