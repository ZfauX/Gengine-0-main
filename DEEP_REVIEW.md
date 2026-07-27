# Deep Review (pass 18) — финальная проверка

## Покрытие

| Инструмент | Результат |
|-----------|-----------|
| `golangci-lint run ./...` | ✅ Чисто |
| `go vet ./...` | ✅ Чисто |
| `go build ./...` | ✅ OK |
| `go test -short ./...` | ✅ 35/35 |

> Проверка регрессий pass 17 + финальное здоровье проекта.

---

## ✅ Регрессии pass 17 — все 6/6 проверены

| Проверка | Статус |
|----------|--------|
| SEO Title во всех handler.go | ✅ Добавлены |
| Canonical URL в layout + helper.go | ✅ Работает |
| ErrorHandler XSS (EscapeString + ToLower) | ✅ Исправлено |
| Squashed migrations 000005_schema | ✅ Создан |
| CHANGELOG.md | ✅ Создан |
| gorm.io/gorm v1.26.0 | ✅ Зафиксирован |

---

## 🔴 CRITICAL

### C1. Sitemap — всего 3 URL из десятков страниц

**Файл:** `internal/app/router.go:133-144`

```go
r.GET("/sitemap.xml", func(c *gin.Context) {
    // / — priority 1.0
    // /games — priority 0.9
    // /calendar — priority 0.7
})
```

В проекте 11 доменов с десятками публичных страниц, но sitemap покрывает только 3. Нет: `/teams`, `/tournaments`, `/games/:id`, `/users/:id`, `/dashboard`, `/profile`, `/settings/notifications`, `/leaderboard` и т.д.

---

### C2. `.env.example` — не хватает 26 переменных

**Файлы:** `.env.example` vs `internal/config/config.go`

В `.env.example` описано 50 переменных. Config загружает 76. Разрыв в **26 переменных**:

**Server/Logging:**
`LOG_FILE_PATH`, `LOG_MAX_SIZE`, `LOG_MAX_AGE`, `LOG_COMPRESS`, `LOG_FORMAT`, `LOG_LEVEL`, `STATIC_DIR`, `UPLOADS_DIR`, `MAX_UPLOAD_SIZE`, `MAX_BODY_SIZE`, `CORS_ORIGINS`, `TRUSTED_PROXIES`, `STRICT_CONFIG`

**DB Pool:**
`DB_CONN_MAX_IDLE_TIME`

**Rate Limiting:**
`RATE_LIMIT_WINDOW`, `RATE_LIMIT_GLOBAL`, `RATE_LIMIT_LOGIN`, `RATE_LIMIT_REGISTRATION`, `RATE_LIMIT_CODE_SUBMISSION`, `RATE_LIMIT_SSE`, `RATE_LIMIT_API`

**Valkey:**
`VALKEY_POOL_SIZE`, `VALKEY_MIN_IDLE_CONNS`, `VALKEY_MAX_RETRIES`

**WebSocket:**
`WS_MAX_TOTAL_CONNS`, `WS_MAX_CONNS_PER_IP`

### C3. `.env.example` — 3 мёртвые Stripe-переменные

`STRIPE_ENABLED`, `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET` описаны в `.env.example`, но **не загружаются** в `config.go` и не используются. `StripeCustomerID` есть в модели User, но код интеграции отсутствует.

---

## 🟠 HIGH

### H1. CI — нет Valkey service

**Файл:** `.github/workflows/go.yml`

Интеграционные тесты могут использовать Valkey (через `Init*WithValkey`), но в CI нет Valkey-сервиса. Тесты, использующие Valkey, пропустятся или упадут.

### H2. CI — coverage только `./cmd/server/...`

Покрытие интеграционных тестов меряет только `cmd/server/`, не включая `internal/` пакеты, которые тесты реально упражняют.

### H3. README — PostgreSQL версия

Говорит "PostgreSQL 16+", но Docker использует `postgres:18-alpine`. Стоит обновить.

---

## 🟡 MEDIUM

| # | Область | Описание |
|---|---------|----------|
| M1 | CI | `govulncheck` без кеширования — каждый раз скачивается |
| M2 | CI | Нет шага `go mod download` для lint job (полагается на кеш из setup-go) |
| M3 | SEO | Sitemap не генерируется динамически из БД |

---

## ✅ Исправлено в раундах 1-18

| Раунд | Найдено | Исправлено |
|-------|---------|------------|
| 1-11 | ~160 | ✅ Все |
| 12 | 15 | ✅ Все |
| 13 | 29 | ✅ Все |
| 14 | 8 | ✅ Все |
| 15 | 18 | ✅ Все |
| 16 | 33 | ✅ Все |
| 17 | 24 | ✅ Все |
| **Всего** | **~287** | **✅ 287/287** |

---

## 📊 Финальный статус проекта

| Метрика | Значение |
|---------|----------|
| Go файлов | ~180 |
| HTML шаблонов | ~77 |
| Тестовых функций | ~200+ |
| Пакетов с тестами | 35/35 (100%) |
| Миграций БД | 18 |
| i18n ключей | 357 ru / 353 en |
| CI/CD | GitHub Actions (5 jobs) |
| Docker | Alpine multi-stage |
| Деплой | Docker Compose |
| Раундов ревью | 18 |
| Найдено/исправлено | ~287 / ~287 |

---

## 🏁 Заключение

Проект в **отличном состоянии**: 100% тестов, 100% линтер, 0 ветированых ошибок, документация в порядке.

**Осталось 3 критические проблемы:**
1. Sitemap — 3 URL вместо десятков (быстрое исправление)
2. 26 env-переменных не документированы в `.env.example`
3. 3 мёртвые Stripe-переменные в `.env.example`

**После их исправления** проект можно считать production-ready.
