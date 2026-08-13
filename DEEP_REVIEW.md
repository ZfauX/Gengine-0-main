# DEEP_REVIEW — Gengine-0 (PASS 10)

> Глубокое ревью после закрытия PASS-9.
> Метод: pprof-профилирование (PPROF_ENABLED=true, :6060, loopback) + 3 параллельных аудита (@reviewer, @security, @perf) + эмпирическая проверка каждого HIGH/MEDIUM finding.
> Архивы: `DEEP_REVIEW_2026-08-11_pass{1,2,3,4}.md`, `DEEP_REVIEW_2026-08-12_pass5.md`, `DEEP_REVIEW_2026-08-12_pass7.md`, `DEEP_REVIEW_2026-08-13_pass{8,9}.md`.

---

## 🔬 pprof-результаты (PASS 10)

| Профиль | Результат | Вывод |
|---|---|---|
| **goroutine** | 18 в покое | ✅ Без утечек (стабильно с PASS-5..9). |
| **heap inuse** | 19.5 MB | ✅ Норма. 46% — инициализационная память `golang.org/x/net/webdav` (swaggo/files, не код). |
| **heap alloc** | 133 MB cumulative | ⚠️ `text/template evalCall` 31.1%, `bytes.Buffer.String` 9.8%, `reflect.Value.call` 17.6% — рендер HTML. |
| **cpu** | 49.5% cgocall (network), template 20% cum | ✅ Рендер снизился с 40% до 20% (пул PASS-9), zerolog ушёл из топа (F1 PASS-10). |
| **pprof bind** | `127.0.0.1:6060` | ✅ loopback. |

**Вывод**: утечек нет; главные фиксы PASS-9/PASS-10 подтверждены профилем (рендер −50%, логирование −31%). Остаточная статья — HTML-рендер (отражённые вызовы `T` в layout).

---

## 🔴 HIGH (reviewer + security)

### H1. `GormLogger.Trace`: `GetLevel()` не работает → оптимизация мёртвая + SQL-ошибки терялись 🔍✅ (подтверждено)
- **Файл**: `internal/pkg/logging/gorm.go:65`.
- **Проблема**: `log.Logger.GetLevel()` возвращает уровень ИНСТАНСА (всегда Trace, т.к. main.go не вызывает `.Level()`), а не глобальный. Итог: `fc()`/regexp выполнялись на каждый запрос (оптимизация F1 не работала), и при Info/Warn SQL-ошибки (`err != nil`) вообще не логировались.
- **Фикс**: ✅ `zerolog.GlobalLevel()`; при `err != nil` — `log.Warn()` независимо от уровня (наблюдаемость).

### H2. `pg_dump` создаёт файл с 0644 до chmod (TOCTOU): plaintext-секреты читаемы во время дампа 🔍✅
- **Файл**: `internal/domain/admin/service.go:163-187`.
- **Проблема**: `pg_dump -f` создаёт файл с правами по умолчанию; `chmod 0600` — ПОСЛЕ завершения (минуты). Любой локальный пользователь читает дамп с паролями/2FA-секретами.
- **Фикс**: ✅ `os.OpenFile(O_CREATE|O_EXCL, 0600)` ДО pg_dump; BackupDir `0755→0700`.

### H3. Смена email без пароля → захват аккаунта через украденную сессию 🔍✅
- **Файл**: `profile_handler.go:322-364`, `profile-show.html`.
- **Проблема**: email менялся по одному JWT; атакующий с XSS/утечкой куки менял email, сбрасывал пароль на новый адрес → захват аккаунта.
- **Фикс**: ✅ смена email требует `current_password` (bcrypt-проверка); поле добавлено в форму.

---

## 🟠 MEDIUM (reviewer)

### M1. `userMsgLimiters` растёт бесконечно (per-user token bucket) 🔍✅
- **Файл**: `monitor/handler.go:376-388`.
- **Проблема**: map per-user лимитеров без eviction — при миллионах юзеров утечка памяти.
- **Фикс**: ✅ `lastUsed` в `wsMessageLimiter` + lazy sweep при `len > 10000` (удаление неактивных >10 мин).

### M2. GetVotingResults/StartVoting: sentinel маппинг не везде (403 → 500) 🔍✅
- **Файл**: `monitor/service.go:79`, `monitor/handler.go:1591`.
- **Проблема**: `StartVoting` возвращал сырой `errors.New`, `GetVotingResults` маппил `ErrAccessDenied` в 500.
- **Фикс**: ✅ `ErrVotingNotManager` в StartVoting; `errors.Is(ErrAccessDenied)` → 403 в хендлере.

### M3. Временный `.decrypted` файл с детерминированным именем (гонка параллельных Download) 🔍✅
- **Файл**: `admin/service.go:380`.
- **Проблема**: два параллельных Download перезаписывали один файл; cleanup не всегда удалял (Windows).
- **Фикс**: ✅ уникальное имя через `randomHex(8)`.

### M4. `BackupService`: fail-open при невалидном ключе шифрования 🔍✅
- **Файл**: `admin/service.go:89`.
- **Проблема**: невалидный `BACKUP_ENCRYPTION_KEY` давал Warn и писал дампы в plaintext.
- **Фикс**: ✅ `NewBackupService` возвращает ошибку (fail-closed); wire/тесты обновлены.

---

## 🟠 MEDIUM (perf)

### P-1. `LoggerMiddleware`: логирует /static/ + maskQuery до проверки уровня 🔍✅
- **Файл**: `middleware/logger.go:50-87`.
- **Проблема**: каждый запрос статики (4+ на страницу) → Info-запись + аллокации maskQuery.
- **Фикс**: ✅ успешные /static/, /uploads/, favicon не логируются на Info (метрики сохранены).

### P-2. `bytes.Buffer.String()` копия — приемлемо 🔍
- **Файл**: `render/helper.go:261`.
- **Вердикт**: копия обязательна (буфер из sync.Pool переиспользуется, строка живёт до рендера layout). Безопасно не убрать; с pool одна копия ~50-100KB на страницу.

### P-3. `text/template evalCall` 31% — ~52 отражённых вызова `T` в layout 🔍
- **Файл**: `templates/layout.html`.
- **Рекомендация**: предвычислить частые ключи (layout.meta_description ×3, nav.notifications ×4 и т.д.) в `data` или через переменные шаблона; HTTP-кэш анонимных страниц.

---

## 🟢 LOW (reviewer/security)

- **L1** `password_reset_service.go:43-46` — мёртвое крипто-чтение (`rand.Read` в `b`, результат не используется).
- **L2** `calendar/handler.go:311-314` — ICS-кэш (`ics:host`) не эвиктится.
- **L3** `monitor/handler.go` — `hasSessionCookie` использует `strings.Contains` (ложное совпадение на `gengine_session_x`).
- **L4** `admin/service.go` — `randomHex` fallback на `time.Now()` (предсказуемые имена при сбое rand).
- **L5** `auth_handler.go:584` — reset/verify-коды в URL (Referer-риск).
- **L6** `logger.go` — `backupWG.Add` маленькое окно гонки с `WaitForBackups` (отложено, low).

---

## ✅ Проверено — проблем НЕ найдено

- **JWT/роли**: HMAC закреплён, jti-blacklist, роль из БД fail-closed.
- **Refresh-токены**: SHA-256, семья + атомарный ClaimAndCreate, revoke при reuse, fingerprint.
- **OAuth**: state 128-бит + ConstantTimeCompare + provider-binding + TTL.
- **2FA**: TOTP/backup с bcrypt, lockout backoff, pending TTL, step-up на /admin.
- **Webhook ЮKassa**: IP-allowlist + Basic + API-подтверждение + сумма в копейках + атомарный succeeded.
- **SSRF (push)**: блокировка приватных IP в DialContext + DNS-rebinding.
- **Uploads**: path-traversal, MIME-магия, случайные имена, 0600, nosniff.
- **Export**: csvSafe (formula), права экспорта, PDF/Excel — модератор/капитан.
- **Cache**: DeleteByPrefix — deadlock устранён, порядок локов согласован (evictCallback сам чистит префиксы).
- **N+1**: batch-CASE, UpsertMany, COUNT OVER, singleflight, advisory lock — образцово.

---

## 💡 Предложения по улучшению кодовой базы (приоритет)

1. **P-3**: дедуп `T()` вызовов в layout (предвычисленные переводы в data) — −5-15% рендера.
2. **L1**: удалить мёртвый `rand.Read`.
3. **L5**: перенести verify-код из URL в POST-форму (Referer-риск).
4. **L2**: eviction ICS-кэша.
5. **P-2**: HTTP-кэш анонимных публичных страниц (Cache-Control) — снимает рендер целиком.

---

## 💡 Предложения по пользовательскому опыту (UX)

1. **Смена email**: теперь требует пароль — жертва украденной сессии защищена; добавить уведомление на старый email.
2. **PDF-экспорт**: длинные вопросы переносятся (MultiCell) — документ читаем.
3. **Производительность**: пул рендера + кэш анонимных страниц → быстрее отклик.

---

## 📋 Статус

- 3 аудита: ✅ проведены; HIGH/MEDIUM findings — ✅ эмпирически проверены.
- **Исправлено в этом проходе: 8 findings** ✅ (H1, H2, H3, M1, M2, M3, M4, P-1) + тесты.
- Проверки: build ✅, test-short ✅, test-integration ✅, golangci-lint ✅ (0 issues), E2E 14/14 ✅.
- Отложено (требует продукта/рефакторинга): P-2, P-3, L1-L6.
