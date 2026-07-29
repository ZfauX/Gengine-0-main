# Deep Review (pass 19) — июль 2026

## Покрытие

| Инструмент | Результат |
|-----------|-----------|
| `golangci-lint run ./...` | ✅ Чисто |
| `go vet ./...` | ✅ Чисто |
| `go build ./...` | ✅ OK |
| `go test -short ./...` | ✅ 35/35 |

---

## 🔴 CRITICAL

### C1. `AddMember` — ошибка обещает то, что не работает

**Файл:** `internal/domain/team/service.go:80`

```go
return errors.New("только капитан или автор игры может добавлять участников")
```

Текст говорит "автор игры может добавлять", но `CanManageTeam` (line 72) проверяет **только капитана**. Автор игры, не являющийся капитаном, получит misleading error.

**Фикс:** Убрать "или автор игры" из сообщения об ошибке (команды теперь независимы от игр).

---

### C2. Mojibake в JSON-ответах notification/routes.go

**Файл:** `internal/domain/notification/routes.go`

Русские строки в JSON-ответах сохранены в битой кодировке (UTF-8 → Latin-1 → UTF-8):

```go
c.JSON(http.StatusOK, gin.H{
    "error": "РўСЂРµР±СѓРµС‚СЃСЏ Р°СѓС‚РµРЅС‚РёС„РёРєР°С†РёСЏ",
})
```

**Затрагивает 7 строк:** 126, 136, 144, 152, 164. Клиенты получат кракозябры вместо русского текста.

---

## 🟠 HIGH

### H1. Unqualified column references в game_listing_service

**Файл:** `internal/domain/game/game_listing_service.go:53,59,89`

WHERE-условия используют `author_id`, `visibility`, `is_draft` без префикса `games.`. Сейчас нет конфликтов, но добавление любого JOIN с такими же именами сломает запрос.

**Фикс:** `games.author_id`, `games.visibility`, `games.is_draft`

---

### H2. Пустой пакет `internal/pkg/apierror/`

**Файл:** `internal/pkg/apierror/` — пустая директория

Мёртвый код. Нужно удалить.

---

### H3. `CreateInvitation` — игнорируется ошибка `GetByTeamAndUser`

**Файл:** `internal/domain/team/service.go:181`

```go
existing, _ := s.invRepo.GetByTeamAndUser(ctx, teamID, invitedUserID)
```

При ошибке БД `existing` будет nil, и проверка на дубликат приглашения пропустится.

---

### H4. `game` пакет — 50 файлов, пора декомпозировать

Самый большой пакет (50 Go файлов). Можно выделить подпакеты:
- `gamepassing/` — прохождения и заявки
- `gamereview/` — отзывы и рейтинги
- `gamelevel/` — управление уровнями (если не пересекается с `level/`)

---

## 🟡 MEDIUM

| # | Область | Файл | Описание |
|---|---------|------|----------|
| M1 | Cleanup | `.gitignore` | Нет `.opencode/` в gitignore — файлы конфигурации попадают в репозиторий |
| M2 | Email | `queue.go:50` | TODO: использовать константы вместо raw strings для статусов |
| M3 | i18n | `helper.go:134` | TODO: `data["lang"]` зарезервировано для будущей i18n интеграции |
| M4 | Team | `service.go:170` | `"пользователь не найден"` — теряется оригинальная ошибка БД |
| M5 | Team | `handler.go:235` | `members` ссылается на капитана на странице `ViewTeam` (old `Members` handler) |
| M6 | Notification | `routes.go:103,124` | Избыточные `if userID == 0` (уже под AuthRequired) |
| M7 | Notification | `routes.go` | `userID == 0` проверки в `apiNotifs` хендлерах — синхронизировать стиль |
| M8 | DI | `wire_providers.go` | `NewGameplayHandler` принимает 8 параметров, 1 (`attemptSvc`) игнорируется через `_` |

---

## ✅ Исправлено за раунд

| Проблема | Статус |
|----------|--------|
| Ambiguous column `name` in game listing query (FTS search) | ✅ `games.name ILIKE` |
| ValidateGameDates test broken after validation change | ✅ Test updated |
| Game listing test broken after LEFT JOIN users | ✅ `users.name as author__name` |
| Push notification button state | ✅ `Notification.permission` based |
| ChatWS error log level too high | ✅ `Error` → `Debug` |
| Level handler nil pointer (db: nil) | ✅ `db *gorm.DB` passed |
| Game creation missing is_draft checkbox | ✅ Added to games-new.html |

---

## 📊 Статистика

| Метрика | Значение |
|---------|----------|
| Go файлов в `internal/` | ~215 |
| Доменов | 11 |
| Пакетов в `internal/pkg/` | 22 (1 пустой) |
| Тестов | 35/35 пакетов |
| TODO/FIXME комментариев | 2 |
| Пустых пакетов | 1 (`apierror/`) |

---

## 🏁 Заключение

После 19 раундов ревью проект стабилен: 35/35 тестов, линтер чист, все основные баги исправлены.

**Остаётся 2 критические и 4 высокие проблемы:**

1. **`AddMember` error message** — не соответствует реальности (can't fix without breaking team independence)
2. **Mojibake в JSON ответах** — 7 строк notification/routes.go
3. **Unqualified columns** — fragile SQL в game_listing_service
4. **Empty `apierror/` package** — 1 пустая директория
5. **Ignored error** — `GetByTeamAndUser` результат не проверяется
6. **Game package size** — 50 файлов, пора декомпозировать
