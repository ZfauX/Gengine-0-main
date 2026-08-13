// cmd/server/main.go
package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "net/http/pprof" // P-1 (pass 48): pprof-эндпоинты /debug/pprof/*

	"gengine-0/internal/app"
	"gengine-0/internal/config"
	"gengine-0/internal/db"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/monitor"
	"gengine-0/internal/domain/notification"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/i18n"
	"gengine-0/internal/pkg/logging"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/realtimebus"
	"gengine-0/internal/pkg/sessionstore"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/natefinch/lumberjack.v2"
	"gorm.io/gorm"
)

// @title Gengine API
// @version 1.0
// @description API для платформы квестов Gengine
// @termsOfService http://swagger.io/terms/
// @contact.name API Support
// @contact.email support@gengine.io
// @license.name MIT
// @license.url https://opensource.org/licenses/MIT
// @host localhost:8080
// @BasePath /
// @securityDefinitions.apikey JWT
// @in cookie
// @name jwt

var (
	version   = "dev"
	buildDate = "unknown"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	migrateFlag := flag.Bool("migrate", false, "Применить миграции и выйти")
	flag.Parse()

	// Загрузка env-файла: по умолчанию `.env`, но можно переопределить через
	// APP_ENV_FILE (например `APP_ENV_FILE=.env.e2e`). Без этого E2E-сервер
	// подхватывал из `.env` отсутствующие в .env.e2e ключи (TLS_*, TRUSTED_PROXIES),
	// что форсировало Secure-куку CSRF и ломало формы по HTTP (403).
	envFile := os.Getenv("APP_ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if err := godotenv.Load(envFile); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to load %s: %w", envFile, err)
		}
		log.Info().Str("file", envFile).Msg("env file not found, using only system environment variables")
	}

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// P-1 (PASS-13): конфигурация SwaggerInfo вынесена за build-tag `swagger`.
	// В обычной сборке configureSwaggerInfo — no-op (docs не грузится).
	app.ConfigureSwagger(cfg.Server.BaseURL)

	// ============================================================
	// ИНИЦИАЛИЗАЦИЯ SENTRY
	// ============================================================
	var sentryWriter *logging.SentryWriter
	sentryFlushTimeout := config.SentryFlushTimeout
	if cfg.Sentry.Enabled && cfg.Sentry.DSN != "" {
		sentryErr := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.Sentry.DSN,
			TracesSampleRate: cfg.Sentry.TracingRate,
			Release:          version,
			Environment:      cfg.Server.GinMode,
		})
		if sentryErr != nil {
			log.Warn().Err(sentryErr).Msg("Sentry: initialization failed, continuing without Sentry")
		} else {
			log.Info().Msg("Sentry: initialized successfully")
			defer sentry.Flush(sentryFlushTimeout)
			sentryWriter = logging.NewSentryWriter(sentryFlushTimeout)
		}
	} else {
		log.Info().Msg("Sentry: disabled")
	}

	// ============================================================
	// НАСТРОЙКА ЛОГГЕРА
	// ============================================================
	logFilePath := cfg.Server.LogFilePath
	if logFilePath == "" {
		logFilePath = "logs/app.log"
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(logFilePath), 0755); mkdirErr != nil {
		log.Error().Err(mkdirErr).Msg("failed to create log directory")
		return fmt.Errorf("failed to create log directory: %w", mkdirErr)
	}

	logFile := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    cfg.Server.LogMaxSize,
		MaxBackups: cfg.Server.MaxBackups,
		MaxAge:     cfg.Server.LogMaxAge,
		Compress:   cfg.Server.LogCompress,
	}

	writers := []io.Writer{os.Stderr, logFile}
	if sentryWriter != nil {
		writers = append(writers, sentryWriter)
	}

	var consoleWriter zerolog.ConsoleWriter
	if cfg.Server.LogFormat == "json" {
		log.Logger = zerolog.New(zerolog.MultiLevelWriter(writers...)).With().Timestamp().Logger()
	} else {
		consoleWriter = zerolog.ConsoleWriter{Out: os.Stderr}
		writers[0] = consoleWriter
		log.Logger = log.Output(zerolog.MultiLevelWriter(writers...))
	}

	log.Info().
		Str("version", version).
		Str("build", buildDate).
		Str("log_format", cfg.Server.LogFormat).
		Msg("Запуск сервера")
	log.Info().
		Str("log_file", logFilePath).
		Int("max_size_mb", cfg.Server.LogMaxSize).
		Int("max_backups", cfg.Server.MaxBackups).
		Int("max_age_days", cfg.Server.LogMaxAge).
		Bool("compress", cfg.Server.LogCompress).
		Msg("Ротация логов включена")

	// H1: Устанавливаем уровень логирования из конфига
	switch strings.ToLower(cfg.Server.LogLevel) {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	log.Debug().Str("level", cfg.Server.LogLevel).Msg("Уровень логирования установлен")

	gin.SetMode(cfg.Server.GinMode)

	// --- Подключение к БД ---
	database, err := connectDBWithRetry(cfg, config.DBMaxRetryAttempts, config.DBRetryInitialDelay)
	if err != nil {
		log.Error().Err(err).Msg("failed to connect to DB after several attempts")
		return fmt.Errorf("failed to connect to DB after several attempts: %w", err)
	}
	log.Info().Msg("DB connection established")

	if *migrateFlag {
		log.Info().Msg("running migrations...")
		if migrateErr := db.RunMigrations(database); migrateErr != nil {
			log.Error().Err(migrateErr).Msg("migration error")
			return fmt.Errorf("migration error: %w", migrateErr)
		}
		log.Info().Msg("migrations applied successfully")
		return nil
	}

	if ensureErr := db.EnsureAdmin(database, cfg); ensureErr != nil {
		log.Error().Err(ensureErr).Msg("failed to create/update admin")
		return fmt.Errorf("failed to create/update admin: %w", ensureErr)
	}

	localStorage := storage.NewLocalStorage().WithBaseDir(filepath.Join(".", cfg.Server.UploadsDir))
	hub := ws.NewRoomHub()
	hub.SetLimits(cfg.WebSocket.MaxTotalConns, cfg.WebSocket.MaxConnsPerIP)
	go hub.Run()

	// --- Инициализация rate limiters (singleton, создаются один раз) ---
	// Если Valkey доступен, используем его как shared backend для rate limiters между инстансами.
	// Если Valkey задан в конфиге, но недоступен (valkeyClient == nil), делаем fallback
	// на in-memory лимитеры — иначе маршруты используют захардкоженные лимиты из routes.go
	// (например, регистрация 3/10мин), игнорируя RATE_LIMIT_* из конфига.
	rateLimitWindow := cfg.Server.RateLimitWindow
	useValkey := false
	// MULTI-INSTANCE (PASS-12): уникальный идентификатор инстанса для
	// anti-эхо в cross-instance pub/sub (WebSocket/SSE).
	instanceID := uuid.NewString()
	// realtimeBus (MULTI-INSTANCE PASS-12): cross-instance real-time шина.
	// nil, если Valkey недоступен — WebSocket/SSE работают локально.
	var realtimeBus realtimebus.Bus
	// valkeyClient (LOW, PASS-13): общий клиент для сессий/лимитеров/шины —
	// сохраняем ссылку, чтобы корректно закрыть при shutdown.
	var valkeyClient *redis.Client
	// SessionStore (PASS-11, session fixation): server-side store — Valkey если
	// доступен, иначе in-memory. В cookie только session ID; данные на сервере.
	sessionSecret := []byte(cfg.Session.Secret)
	sessionEncKey := sha256.Sum256([]byte(cfg.Session.Secret + ":enc"))
	var sessionStore *sessionstore.ServerStore
	if cfg.Valkey.Host != "" {
		valkeyClient = cache.NewValkeyClient(cfg.Valkey.Host, cfg.Valkey.Port, cfg.Valkey.Password, cfg.Valkey.PoolSize, cfg.Valkey.MinIdleConns, cfg.Valkey.MaxRetries)
		if valkeyClient != nil {
			useValkey = true
			// PASS-11: server-side session store в Valkey (multi-instance).
			sessionStore = sessionstore.NewValkeyStore(valkeyClient, "gengine:session", sessionSecret, sessionEncKey[:])
			// S-M2 (PASS-8): общий клиент для per-user лимитеров (личный чат,
			// комнаты, поиск, WebAuthn, платежи) — меж-инстансная координация.
			middleware.SetSharedValkeyClient(valkeyClient)
			// MULTI-INSTANCE (PASS-12): cross-instance real-time шина (WebSocket +
			// SSE) через Valkey pub/sub. instanceID — уникальный идентификатор
			// ЭТОГО инстанса (anti-эхо при рассылке).
			realtimeBus = realtimebus.NewValkeyBus(valkeyClient)
			hub.SetPubSub(realtimeBus, instanceID)
			// Глобальный/SSE/API — in-memory (M7, PASS-13): эти лимитеры защищают
			// от флуда ОДНОГО инстанса; распределённый флуд за балансировщиком —
			// задача nginx/LB. Раньше каждый запрос делал round-trip INCR в Valkey
			// (главный вкладчик в 55% cgocall по pprof). Критичные лимитеры
			// (логин/2FA/коды/сброс) остаются в Valkey fail-closed.
			middleware.InitGlobalRateLimiter(rateLimitWindow, cfg.Server.RateLimitGlobalRequests)
			middleware.InitSSERateLimiter(rateLimitWindow, cfg.Server.RateLimitSSE)
			middleware.InitAPIRateLimiter(rateLimitWindow, cfg.Server.RateLimitAPI)
			// Критичные лимитеры (логин, регистрация, 2FA/коды, сброс пароля, OAuth) —
			// fail-closed (S-46-1, pass 46): при outage Valkey запросы отклоняются,
			// иначе брутфорс-защита отключается вместе с кэшем.
			middleware.InitLoginRateLimiterWithValkeyFailClosed(valkeyClient, rateLimitWindow, cfg.Server.RateLimitLoginRequests)
			middleware.InitRegistrationRateLimiterWithValkeyFailClosed(valkeyClient, rateLimitWindow, cfg.Server.RateLimitRegistration)
			middleware.InitCodeSubmissionRateLimiterWithValkeyFailClosed(valkeyClient, rateLimitWindow, cfg.Server.RateLimitCodeSubmission)
			middleware.InitPasswordResetRateLimiterWithValkeyFailClosed(valkeyClient, rateLimitWindow, cfg.Server.RateLimitPasswordReset)
			middleware.InitOAuthRateLimiterWithValkeyFailClosed(valkeyClient, rateLimitWindow, cfg.Server.RateLimitLoginRequests)
			// M6 (PASS-13): per-user лимит загрузок (аватары, фото) — общий в Valkey.
			middleware.InitUploadRateLimiter(rateLimitWindow, cfg.Server.RateLimitUploadRequests)
		} else {
			log.Warn().Msg("Valkey configured but unavailable, falling back to in-memory rate limiters")
		}
	}
	if !useValkey {
		middleware.InitGlobalRateLimiter(rateLimitWindow, cfg.Server.RateLimitGlobalRequests)
		middleware.InitLoginRateLimiter(rateLimitWindow, cfg.Server.RateLimitLoginRequests)
		middleware.InitRegistrationRateLimiter(rateLimitWindow, cfg.Server.RateLimitRegistration)
		middleware.InitCodeSubmissionRateLimiter(rateLimitWindow, cfg.Server.RateLimitCodeSubmission)
		middleware.InitSSERateLimiter(rateLimitWindow, cfg.Server.RateLimitSSE)
		middleware.InitAPIRateLimiter(rateLimitWindow, cfg.Server.RateLimitAPI)
		middleware.InitPasswordResetRateLimiter(rateLimitWindow, cfg.Server.RateLimitPasswordReset)
		middleware.InitOAuthRateLimiter(rateLimitWindow, cfg.Server.RateLimitLoginRequests)
		// M6 (PASS-13): per-user лимит загрузок (in-memory fallback).
		middleware.InitUploadRateLimiter(rateLimitWindow, cfg.Server.RateLimitUploadRequests)
		// PASS-11: без Valkey — in-memory server-side сессии (single-instance).
		// Сессии теряются при рестарте — пользователь входит заново (приемлемо).
		sessionStore = sessionstore.NewInMemoryStore(sessionSecret, sessionEncKey[:])
	}

	// --- Инициализация persistent-очереди email (только если SMTP включён) ---
	if cfg.SMTP.Enabled {
		email.InitQueue(cfg, database, config.EmailQueueWorkers, config.EmailQueueInterval, config.EmailQueueBatchSize)
	} else {
		log.Info().Msg("SMTP disabled, email queue not started")
	}

	// --- Инициализация кэша (Valkey с fallback на in-memory, NoopCache как последний fallback) ---
	var appCache cache.CacheStore
	if cfg.Valkey.Host != "" {
		appCache = cache.NewValkeyCache(cfg.Valkey.Host, cfg.Valkey.Port, cfg.Valkey.Password, cfg.Valkey.PoolSize, cfg.Valkey.MinIdleConns, cfg.Valkey.MaxRetries)
		if appCache == nil {
			log.Warn().Msg("Valkey unavailable, using in-memory cache")
			appCache = cache.NewCacheWithLRU(config.CacheDefaultTTL, config.CacheCleanupInterval, config.CacheMaxItems)
		}
	} else {
		log.Info().Msg("Valkey not configured, using in-memory cache")
		appCache = cache.NewCacheWithLRU(config.CacheDefaultTTL, config.CacheCleanupInterval, config.CacheMaxItems)
	}

	deps := app.NewDependencies(database, cfg, hub, localStorage, appCache)
	appInstance := app.NewApp(database, localStorage, hub, cfg, ".", deps)
	// MULTI-INSTANCE (PASS-12): cross-instance SSE через Valkey pub/sub.
	// SSEManager создаётся внутри wire — настраиваем через deps.Services.
	if realtimeBus != nil {
		deps.Services.SSEMgr.SetPubSub(realtimeBus, instanceID)
	}
	// PASS-11 (session fixation): server-side session store (Valkey/in-memory).
	if sessionStore != nil {
		appInstance.SetSessionStore(sessionStore)
		// Глобальный регистр для RenewGinSession (перевыпуск session ID при
		// логине/2FA/OAuth из хендлеров, где нет доступа к store).
		sessionstore.SetDefault(sessionStore)
	}
	r, err := appInstance.SetupRouter()
	if err != nil {
		log.Error().Err(err).Msg("failed to setup routes")
		return fmt.Errorf("failed to setup routes: %w", err)
	}

	// Контекст для фоновых задач
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// bgWg отслеживает фоновые горутины для корректного завершения.
	// Должен быть объявлен ДО onGameFinished (тот добавляет в него горутины).
	var bgWg sync.WaitGroup

	// Callback для расчёта результатов при завершении игры.
	// DEEP-REVIEW PASS-3 H4: выполняется в ФОНОВОЙ горутине с таймаутом —
	// раньше CalculateResults + UpdateScoresForGame (advisory lock) + UpdateRatings
	// блокировали HTTP-запрос игрока, отправившего код на последнем уровне.
	// M9 (PASS-3): WithoutCancel + WithTimeout — фоновая работа не виснет на lock.
	// H1 (PASS-13): горутина добавляется в bgWg — shutdown ждёт её до закрытия БД
	// (раньше WithoutCancel переживал shutdown и мог выполнять SQL после sqlDB.Close()).
	onGameFinished := func(reqCtx context.Context, gameID uint) {
		bgCtx, bgCancel := context.WithTimeout(context.WithoutCancel(reqCtx), 30*time.Second)
		defer bgCancel()
		bgWg.Add(1)
		go func() {
			defer bgWg.Done()
			if deps.Services.Monitor != nil {
				if err := deps.Services.Monitor.CalculateResults(bgCtx, gameID); err != nil {
					log.Error().Err(err).Uint("game_id", gameID).Msg("onGameFinished: CalculateResults failed")
				}
			}
			if deps.Services.Tournament != nil {
				deps.Services.Tournament.UpdateScoresForGame(bgCtx, gameID)
			}
			// Начисление очков рейтинга игрокам (B3): раньше UpdateRatingsForGame
			// не вызывался в проде — игроки не получали очки за игры.
			if deps.Services.Rating != nil {
				if err := deps.Services.Rating.UpdateRatingsForGame(bgCtx, gameID); err != nil {
					log.Error().Err(err).Uint("game_id", gameID).Msg("onGameFinished: UpdateRatingsForGame failed")
				}
			}
		}()
	}

	// Прокидываем колбэк в обычный игровой путь (SubmitCode/AcceptBlackboxAnswer)
	// и в принудительное завершение — турнирные очки начисляются и при штатном финише.
	if deps.Services.GamePlay != nil {
		deps.Services.GamePlay.WithGameFinishedCallback(onGameFinished)
	}
	if deps.Services.GameAdmin != nil {
		deps.Services.GameAdmin.WithGameFinishedCallback(onGameFinished)
	}

	// Асинхронный дебаунс-диспетчер снапшотов мониторинга (S3): тяжёлый
	// пересчёт (GetOrFetchSnapshot + CalculateResults + broadcast) уходит из
	// HTTP-запроса игрока в фоновый воркер с дебаунсом ~500 мс на игру.
	var snapshotDispatcher *game.SnapshotDispatcher
	if deps.Services.GamePlay != nil {
		snapshotDispatcher = game.NewSnapshotDispatcher(500*time.Millisecond, func(gameID uint) {
			deps.Services.GamePlay.ProcessSnapshot(context.Background(), gameID)
		})
		deps.Services.GamePlay.WithSnapshotDispatcher(snapshotDispatcher)
	}

	// goSafe запускает горутину с recover.
	goSafe := func(fn func()) {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error().Interface("panic", r).Str("stack", string(debug.Stack())).Msg("goroutine panicked")
				}
			}()
			fn()
		}()
	}

	// Запуск фоновых задач
	bgWg.Add(1)
	goSafe(func() {
		defer bgWg.Done()
		game.CheckTimeouts(database, ctx, onGameFinished)
	})
	bgWg.Add(1)
	goSafe(func() {
		defer bgWg.Done()
		game.CheckAutoStartGames(database, ctx)
	})

	// Мониторинг connection pool (раз в минуту)
	bgWg.Add(1)
	goSafe(func() {
		defer bgWg.Done()
		ticker := time.NewTicker(config.PoolMonitorInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("connection pool monitoring: stopping")
				return
			case <-ticker.C:
				sqlDB, err := database.DB()
				if err != nil {
					log.Warn().Err(err).Msg("connection pool monitoring: failed to get sql.DB")
					continue
				}
				stats := sqlDB.Stats()
				log.Debug().
					Int("open_connections", stats.OpenConnections).
					Int("in_use", stats.InUse).
					Int("idle", stats.Idle).
					Int64("wait_count", stats.WaitCount).
					Int64("wait_duration_ms", stats.WaitDuration.Milliseconds()).
					Msg("Connection pool stats")
			}
		}
	})

	// WebSocket cleanup — периодическая очистка неактивных соединений
	bgWg.Add(1)
	goSafe(func() {
		defer bgWg.Done()
		appInstance.Hub.StartCleanupPeriodic()
	})

	// Фоновая очистка просроченных refresh-токенов (раз в час)
	bgWg.Add(1)
	goSafe(func() {
		defer bgWg.Done()
		ticker := time.NewTicker(config.RefreshTokenCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("refresh token cleanup: context canceled, stopping")
				return
			case <-ticker.C:
				if err := deps.Services.Auth.CleanExpiredRefreshTokens(ctx); err != nil {
					log.Error().Err(err).Msg("Очистка refresh-токенов: ошибка")
				} else {
					log.Debug().Msg("Очистка refresh-токенов: успешно завершена")
				}
			}
		}
	})

	// Фоновая retention прочитанных уведомлений (P-2, pass 33): раз в сутки
	// удаляем прочитанные старше 90 дней — таблица не растёт безгранично.
	const notificationRetentionDays = 90
	bgWg.Add(1)
	goSafe(func() {
		defer bgWg.Done()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("notification retention: context canceled, stopping")
				return
			case <-ticker.C:
				cutoff := time.Now().AddDate(0, 0, -notificationRetentionDays)
				deleted, err := deps.Repos.Notification.DeleteOldRead(ctx, cutoff)
				if err != nil {
					log.Error().Err(err).Msg("Очистка уведомлений: ошибка")
				} else if deleted > 0 {
					log.Debug().Int64("deleted", deleted).Msg("Очистка уведомлений: удалено прочитанных старше 90 дней")
				}
			}
		}
	})

	// D-1 (pass 45): уведомления о предстоящих играх. Раз в час проверяем,
	// есть ли опубликованные игры, стартующие ровно через 30/14/7/1 день, и
	// создаём уведомление пользователям с соответствующим notify_game_days.
	bgWg.Add(1)
	goSafe(func() {
		defer bgWg.Done()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("upcoming game reminders: context canceled, stopping")
				return
			case <-ticker.C:
				remindDays := []int{30, 14, 7, 1}
				for _, days := range remindDays {
					games, err := deps.Repos.Game.ListUpcomingByDays(ctx, days)
					if err != nil {
						log.Error().Err(err).Int("days", days).Msg("Upcoming game reminders: list failed")
						continue
					}
					if len(games) == 0 {
						continue
					}
					userIDs, err := deps.Repos.User.GetUsersByNotifyDays(ctx, days)
					if err != nil {
						log.Error().Err(err).Int("days", days).Msg("Upcoming game reminders: users failed")
						continue
					}
					for _, game := range games {
						link := fmt.Sprintf("/games/%d", game.ID)
						for _, uid := range userIDs {
							title := i18n.T("notif.upcoming_game_title")
							body := fmt.Sprintf("%s: %s", i18n.T("notif.upcoming_game_body"), game.Name)
							if err := deps.Services.Notification.Create(ctx, uid, notification.NotificationTypeInfo, title, body, link); err != nil {
								log.Debug().Err(err).Uint("user_id", uid).Uint("game_id", game.ID).Msg("Upcoming game reminder: create failed")
							}
						}
					}
				}
			}
		}
	})

	srv := &http.Server{
		Addr:        ":" + cfg.Server.Port,
		Handler:     r,
		ReadTimeout: config.ServerReadTimeout,
		// WriteTimeout = 0 (бесконечность): HTTP/1.1 WriteTimeout обрывает SSE/WebSocket-потоки
		// через 30 сек с ERR_INCOMPLETE_CHUNKED_ENCODING. Защиту от зависших запросов
		// обеспечивает middleware.ContextTimeout (30 сек) на уровне Gin.
		WriteTimeout: 0,
		IdleTimeout:  config.ServerIdleTimeout,
	}

	// P-1 (pass 48): отдельный pprof-сервер (PPROF_ENABLED=true, порт PPROF_PORT).
	// Не экспонируем pprof на основном порту — включается только для профилирования.
	// DEEP-REVIEW PASS-4 H2: по умолчанию привязан к 127.0.0.1 (PPROF_BIND) —
	// без auth pprof не должен быть доступен извне (дамп памяти = секреты).
	var pprofSrv *http.Server
	if cfg.Server.PprofEnabled {
		pprofMux := http.NewServeMux()
		pprofMux.Handle("/debug/pprof/", http.DefaultServeMux)
		addr := net.JoinHostPort(cfg.Server.PprofBind, cfg.Server.PprofPort)
		pprofSrv = &http.Server{
			Addr:        addr,
			Handler:     pprofMux,
			ReadTimeout: config.ServerReadTimeout,
		}
		goSafe(func() {
			log.Info().Str("addr", addr).Msg("pprof server started (loopback by default)")
			if err := pprofSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Msg("Ошибка работы pprof-сервера")
			}
		})
	}

	goSafe(func() {
		log.Info().Str("port", cfg.Server.Port).Msg("Сервер запущен")
		var err error
		if cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != "" {
			err = srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("Ошибка работы сервера")
		}
	})

	// Ожидание сигналов завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Получен сигнал завершения, инициируем graceful shutdown...")

	// ============================================================
	// GRACEFUL SHUTDOWN — правильный порядок:
	// 1. Остановить rate limiters (перестать принимать новые запросы)
	// 2. Остановить email-очередь (чтобы рабочие завершились до остановки HTTP)
	// 3. Остановить HTTP-сервер (дождаться завершения текущих запросов, включая WS upgrade)
	// 4. Остановить WebSocket-хаб (после HTTP — больше нет активных хендлеров)
	// 5. Отменить контекст фоновых задач
	// 6. Закрыть кэш (Valkey)
	// ============================================================

	// 1. Останавливаем rate limiters — запрещаем новые запросы
	middleware.StopGlobalRateLimiter()
	middleware.StopLoginRateLimiter()
	middleware.StopRegistrationRateLimiter()
	middleware.StopCodeSubmissionRateLimiter()
	middleware.StopPasswordResetRateLimiter()
	middleware.StopSSERateLimiter()
	middleware.StopAPIRateLimiter()
	middleware.StopOAuthRateLimiter()

	// 2. Останавливаем очередь email (если была запущена) — до HTTP, чтобы
	//    рабочие завершили отправку, прежде чем сервер перестанет принимать запросы.
	if cfg.SMTP.Enabled {
		email.ShutdownQueue()
		log.Info().Msg("Email-очередь остановлена")
	}

	// 3. Останавливаем SSE-менеджер ДО HTTP-сервера — закрывает все SSE-сессии
	//    (иначе srv.Shutdown() ждёт бесконечные SSE-потоки до таймаута).
	if deps.Services.SSEMgr != nil {
		deps.Services.SSEMgr.Stop()
		log.Info().Msg("SSE-менеджер остановлен")
	}

	// Останавливаем общие сборщики монитора SSE
	monitor.StopAllMonitorPollers()
	log.Info().Msg("SSE-сборщики монитора остановлены")

	// 3.1 Останавливаем пул push-воркеров уведомлений (H7, pass 30) — ждём
	//     доставки уже поставленных задач, новые запросы не принимаются.
	if deps.Services.Notification != nil {
		deps.Services.Notification.Shutdown()
		log.Info().Msg("Пул push-воркеров уведомлений остановлен")
	}

	// 4. Останавливаем HTTP-сервер (ожидаем завершения текущих запросов)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("Ошибка при завершении HTTP-сервера")
	}
	log.Info().Msg("HTTP-сервер остановлен")

	// 4.1 Останавливаем pprof-сервер (LOW, PASS-13): раньше не включался в
	// graceful shutdown — процесс просто завершался с активным сервером.
	if pprofSrv != nil {
		if err := pprofSrv.Shutdown(shutdownCtx); err != nil {
			log.Warn().Err(err).Msg("Ошибка при завершении pprof-сервера")
		} else {
			log.Info().Msg("pprof-сервер остановлен")
		}
	}

	// 5. Останавливаем WebSocket-хаб (после HTTP — ни один хендлер не использует хаб)
	hub.Stop()
	log.Info().Msg("WebSocket-хаб остановлен")

	// 5.0 Останавливаем фоновую очистку кэша темы (L5, PASS-3).
	middleware.StopThemeCacheCleanup()

	// 5.1 Останавливаем дебаунс-диспетчер снапшотов (S3) — до закрытия БД.
	if snapshotDispatcher != nil {
		snapshotDispatcher.Close()
		log.Info().Msg("Снапшот-диспетчер остановлен")
	}

	// 5.2 Дожидаемся активного фонового pg_dump (L10, PASS-6) — иначе процесс
	// убивает дамп посреди записи. L6 (PASS-10): BeginShutdown блокирует новые
	// дампы — устраняет гонку Add(1) с Wait (неотслеживаемая горутина).
	if deps.Services.Backup != nil {
		deps.Services.Backup.BeginShutdown()
		deps.Services.Backup.WaitForBackups()
		log.Info().Msg("Фоновые бекапы завершены")
	}

	// 6. Отменяем контекст фоновых задач
	cancel()

	// Дожидаемся завершения фоновых горутин
	bgWg.Wait()
	log.Info().Msg("Фоновые задачи остановлены")

	// 7. Закрываем кэш (Valkey connection)
	if closer, ok := appCache.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			log.Warn().Err(err).Msg("Ошибка закрытия кэша")
		} else {
			log.Info().Msg("Кэш закрыт")
		}
	}

	// 7.1. Останавливаем cross-instance pub/sub шину и закрываем общий Valkey-клиент
	// (LOW, PASS-13): раньше realtimeBus и valkeyClient не закрывались —
	// pubsub-горутины и пул Redis-соединений жили до выхода процесса.
	if realtimeBus != nil {
		realtimeBus.Close()
		log.Info().Msg("Realtime bus closed")
	}
	if valkeyClient != nil {
		if closeErr := valkeyClient.Close(); closeErr != nil {
			log.Warn().Err(closeErr).Msg("Ошибка закрытия Valkey-клиента")
		} else {
			log.Info().Msg("Valkey client closed")
		}
	}

	// 8. Закрываем соединение с БД
	if sqlDB, err := database.DB(); err == nil {
		if closeErr := sqlDB.Close(); closeErr != nil {
			log.Warn().Err(closeErr).Msg("Ошибка закрытия БД")
		} else {
			log.Info().Msg("Соединение с БД закрыто")
		}
	}

	log.Info().Msg("Сервер полностью остановлен")
	return nil
}

func connectDBWithRetry(cfg *config.Config, maxAttempts int, initialDelay time.Duration) (*gorm.DB, error) {
	var dbConn *gorm.DB
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		log.Info().Int("attempt", attempt).Msg("Попытка подключения к БД")
		dbConn, lastErr = db.Connect(cfg)
		if lastErr == nil {
			return dbConn, nil
		}

		if attempt == maxAttempts {
			break
		}

		delay := initialDelay * time.Duration(1<<(attempt-1))
		log.Warn().
			Err(lastErr).
			Dur("delay", delay).
			Int("remaining", maxAttempts-attempt).
			Msg("Ошибка подключения к БД, повтор через задержку")
		time.Sleep(delay)
	}

	return nil, fmt.Errorf("не удалось подключиться к БД после %d попыток: %w", maxAttempts, lastErr)
}
