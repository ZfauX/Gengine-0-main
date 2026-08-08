# Deep Review Gengine-0 — 7 августа 2026 (pass 29 — повторное ревью после закрытия pass 28)

## Резюме

Повторное глубокое ревью выполнено **4 параллельными агентами** (security, performance/DB, frontend/UX, tests/architecture) с последующей **личной верификацией всех ключевых находок** (включая опровержение).

**Итог:** 1 критичный баг, 5 высоких, ~15 средних, ~20 низких + оптимизации (4+ индексов, 4 кэш/запросные стратегии) + UX/архитектурные предложения.

> **Статус на 8 авг 2026: все практические находки закрыты** 9 коммитами
> (`69265fc`, `3408b67`, `0c90077`, `25faeae`, `89177c6`, `e02532c` и др.)
> + полный DI-граф, split god-файлов, pprof, RUM.
> Легенда: ✅ исправлено · 🔲 осознанно оставлено.

---

## A. Найденные ошибки (верифицировано лично)

### 🔴 Критично

| ID | Статус | Коммит | Файл | Проблема |
|---|---|---|---|---|
| **CRIT-1** | ✅ | `69265fc` | `user-2fa-enable.html` + `two_factor_service.go` | **Утечка TOTP-секрета третьей стороне.** QR теперь генерируется локально (`GenerateQRCodePNG` + маршрут `/user/2fa/qr`); `api.qrserver.com` больше не получает `otpauth://...?secret=`. |

### 🟠 Высокие

| ID | Статус | Коммит | Файл | Проблема |
|---|---|---|---|---|
| **HIGH-1** | ✅ | `69265fc` | `webauthn_handler.go:128` | 2FA step-up использует `is2FAVerified` (int64 timestamp + TTL) вместо сравнения с bool — регистрация passkey при 2FA снова работает. |
| **HIGH-2** | ✅ | `69265fc` | `profile-show.html`, `profile-public.html` | Инициала аватара через template-func `initials` (rune-safe) — кириллица больше не битая. |
| **HIGH-3** | ✅ | `69265fc` | `output.css` | `.field-error{color:#ef4444}` → `#dc2626` (AA-контраст). |
| **HIGH-4** | ✅ | `69265fc` | `monitor/handler.go` | `/voting/vote` возвращает **403** (`ErrNotTeamMember` sentinel) вместо 400 — убран оракул. |
| **HIGH-5** | ✅ | `3408b67` | `service_test.go` | `OAuthService.Authenticate` покрыт тестами через `httptest` + кастомный RoundTripper (5 подтестов). |

### 🟡 Средние

| ID | Статус | Коммит | Файл | Проблема |
|---|---|---|---|---|
| MED-1 | ✅ | `69265fc` | `sqlutil.go` | `EscapeLike` экранирует backslash `\` (+тест). |
| MED-2 | ✅ | `69265fc` | `admin-*.html` | `\| urlquery` в пагинации/фильтрах админки. |
| MED-3 | ✅ | `69265fc` | `notification/service_test.go` | `GetByUser` покрыт через mockRepo (двойной skip убран). |
| MED-4 | ✅ | `0c90077` | `tournament/service.go`, `monitor/service.go` | Sentinel-ошибки (Err*Forbidden, ErrCaptainOnly, ErrVotingClosed и др.) вместо строк. |
| MED-5 | ✅ | `3408b67` | `layout.html` | Удалены ~28 мёртвых `data-i18n-*` атрибутов. |
| MED-6 | ✅ | `3408b67` | `games-photos.html` | Lightbox: `role="dialog" aria-modal` + focus restore на thumb. |
| MED-7 | ✅ | `3408b67` | `monitor-page.html` | Дисквалификация: блокировка кнопки + toast успеха. |
| MED-8 | ✅ | `0c90077` | `profile-show.html` | Удалён мёртвый JS-блок настроек уведомлений. |
| MED-9 | ✅ | `3408b67` | `games-list.html` | Fetch предпочтений вида только для авторизованных. |
| MED-10 | ✅ | `3408b67` | `monitor-page.html` | `escapeHtml(String(team_id))` в data-атрибуте. |
| MED-11 | ✅ | `3408b67` | `gameplay-show.html`, `webauthn-login-button.html` | Same-origin guard для `location.href` редиректов. |
| MED-12 | ✅ | `0c90077` | `export/service_test.go` | PDF/Excel тесты (валидные сигнатуры). |
| MED-13 | ✅ | `0c90077` | `calendar/handler_test.go` | iCal/sanitize тесты + фикс экранирования backslash. |
| MED-14 | ✅ | `69265fc` | `calendar/handler_test.go` | `require.NotEmpty` вместо тихого skip. |
| MED-15 | ✅ | `3408b67` | `social/service.go` | `Unfollow` возвращает `ErrNotFollowing` при отсутствии подписки. |

### 🔲 Осознанно оставлено (низкий риск / не воспроизводится)
- **PF9 (push async)** — закрыто в `0c90077` (async goroutine).
- **F4** (регекс-классификация ошибок кода) — косметика, не блокирует.
- **T2/T4** (time.Sleep в тестах, русские строки) — стабильно на CI.

---

## B. Оптимизации

| ID | Статус | Коммит | Файл | Оптимизация |
|---|---|---|---|---|
| Индексы | ✅ | `69265fc` | `migrations/000038` | `external_logins(provider,external_id)`, `external_logins(user_id)`, `player_ratings(score)`, `teams.name trgm`. |
| PF-1 | ✅ | `25faeae` | `svc_monitor.go` | GameSnapshot attempts: трёхуровневый IN → прямой JOIN. |
| PF-2 | ✅ | `0c90077` | `notification/service.go` | Web Push асинхронно (`context.WithoutCancel` goroutine). |
| PF-3 | ✅ | `25faeae` | `profile_service.go` | `GetPublicProfileStats`: 4 round-trip → 1 SQL. |
| PF-4 | ✅ | `25faeae` | `game/service.go` | `GetByID` пропускает permission-проверку для public non-draft. |
| PF-5 | ✅ | `89177c6` | `svc_progress.go` | `checkAutoStartGamesImpl`: N+1 COUNT → батч `GROUP BY game_id`. |
| PF-6 | ✅ | `25faeae` | `admin/handler.go` | Dashboard: 5 COUNT → 1 SQL со скалярными подзапросами. |
| PF-8a | ✅ | `25faeae` | `level/service.go` | Duplicate: батч-вставка вопросов/ответов. |

---

## C. Улучшения пользовательского опыта

| # | Статус | Коммит | Примечание |
|---|---|---|---|
| 1 | ✅ | `3408b67` | Skeleton loaders, optimistic chat, keyboard shortcuts. |
| 2 | ✅ | `3408b67` | Photo lightbox dialog + focus. |
| 3 | ✅ | `0c90077` | Print styles, 404 search, localized dates (ранее в pass 28). |
| 4 | ✅ | `3408b67` | `monitor.disqualify_success` toast. |

---

## D. Архитектурные улучшения

| # | Статус | Коммит | Примечание |
|---|---|---|---|
| D1 (repository-интерфейсы) | ✅ | `2aba00d`, `0c90077` | NotificationRepository полный; monitor/tournament sentinel-ошибки; тесты высокорисковых путей. |
| D2 (split god-классов) | ✅ | `2aba00d`, `89177c6`, `e02532c` | `RefreshTokenService`; `user/service.go` разбит на 4 файла; `game/service.go` → `service.go` + `svc_facade.go`. |
| D3 (error-контракт) | ✅ | `0478223`, `0c90077` | Sentinel-ошибки + `errKeyMap`. |
| D4 (DI manifest) | ✅ | `abaf4fa`, `89177c6` | Полный DI-граф через wire (все репозитории и сервисы). |
| D5 (i18n автотест) | ✅ | `abaf4fa` | `TestAllUsedKeysExistInBothDictionaries`. |
| D6 (тесты) | ✅ | `abaf4fa`, `0c90077` | `t.Cleanup`, разблокированные тесты, PDF/Excel/iCal/OAuth. |

---

## E. Идеи (реализованы в e02532c)

| Идея | Статус | Описание |
|---|---|---|
| **Профилирование на реальных данных** | ✅ | `/debug/pprof/*` под админ+2FA защитой — CPU/heap/goroutine/trace профили с продакшена. |
| **RUM (Real User Monitoring)** | ✅ | `POST /api/rum` + `PerformanceObserver`-коллектор Web Vitals (LCP/INP/CLS/FCP/TTFB) в layout.html; метрики `gengine_rum_*` в Prometheus. |
| **Split GameService** | ✅ | `service.go` (CRUD-ядро, 398 строк) + `svc_facade.go` (делегирующие методы, 199 строк). |

---

## Приоритет фиксов (исторически)

1. **CRIT-1** — утечка TOTP-секрета → ✅ `69265fc`.
2. **HIGH-1..5** → ✅ `69265fc`, `3408b67`.
3. **MED-1..15** → ✅ `69265fc`, `3408b67`, `0c90077`.
4. Индексы + перф-оптимизации → ✅ `25faeae`, `89177c6`.
5. Архитектура + идеи → ✅ `89177c6`, `e02532c`.

---

## Статус (актуально)

**Все практические находки pass 29 закрыты.** Этапы:
1. `69265fc` — CRIT-1, HIGH-1..4, MED-1/2/3/14, индексы
2. `3408b67` — HIGH-5, MED-5..11/15
3. `0c90077` — MED-4/8/12/13, PF-2
4. `25faeae` — PF-1/3/4/6/8a
5. `89177c6` — полный DI-граф (H2), split user god-file (M9), PF-5
6. `e02532c` — pprof, RUM, split GameService facade

Полный CI-контур зелёный: `go build`, `go vet`, `gofmt`, `go generate`, `go test -race -short ./internal/...`, golangci-lint — все чистые.
