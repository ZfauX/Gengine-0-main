// cmd/server/main.go
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gengine-0/internal/app"
	"gengine-0/internal/config"
	"gengine-0/internal/domain/admin"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/monitor"
	"gengine-0/internal/domain/social"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/tournament"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"
)

// Версия и дата сборки (заполняются при линковке)
var (
	version   = "dev"
	buildDate = "unknown"
)

// prometheus метрики
var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Общее количество HTTP-запросов",
		},
		[]string{"method", "path", "status"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Длительность HTTP-запросов",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsTotal, httpRequestDuration)
}

// loggerMiddleware логирует запросы с помощью zerolog
func loggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery
		if raw != "" {
			path = path + "?" + raw
		}

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		method := c.Request.Method

		log.Info().
			Int("status", status).
			Str("method", method).
			Str("path", path).
			Dur("latency", latency).
			Str("ip", c.ClientIP()).
			Msg("HTTP запрос")

		httpRequestsTotal.WithLabelValues(method, c.Request.URL.Path, fmt.Sprintf("%d", status)).Inc()
		httpRequestDuration.WithLabelValues(method, c.Request.URL.Path).Observe(latency.Seconds())
	}
}

// gormLogger адаптирует zerolog для GORM v2
type gormLogger struct {
	logLevel logger.LogLevel
}

func (l *gormLogger) LogMode(level logger.LogLevel) logger.Interface {
	newLogger := *l
	newLogger.logLevel = level
	return &newLogger
}

func (l *gormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= logger.Info {
		log.Info().Msgf(msg, data...)
	}
}

func (l *gormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= logger.Warn {
		log.Warn().Msgf(msg, data...)
	}
}

func (l *gormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= logger.Error {
		log.Error().Msgf(msg, data...)
	}
}

func (l *gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.logLevel <= logger.Silent {
		return
	}
	elapsed := time.Since(begin)
	sql, rows := fc()
	log.Debug().
		Dur("elapsed", elapsed).
		Int64("rows", rows).
		Str("sql", sql).
		Err(err).
		Msg("GORM trace")
}

func ensureAdmin(db *gorm.DB, cfg *config.Config) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(cfg.Admin.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Error().Err(err).Msg("ensureAdmin: не удалось захешировать пароль")
		return
	}

	var adminUser user.User
	result := db.Where("role = ?", "admin").First(&adminUser)
	if result.Error == nil {
		adminUser.Password = string(hashed)
		adminUser.Email = cfg.Admin.Email
		if err := db.Save(&adminUser).Error; err != nil {
			log.Error().Err(err).Msg("ensureAdmin: не удалось обновить администратора")
			return
		}
		log.Info().Str("email", adminUser.Email).Msg("Администратор обновлён")
		return
	}

	adminUser = user.User{
		Email:         cfg.Admin.Email,
		Password:      string(hashed),
		Name:          "Администратор",
		Role:          "admin",
		EmailVerified: true,
	}
	if err := db.Create(&adminUser).Error; err != nil {
		log.Error().Err(err).Msg("ensureAdmin: не удалось создать администратора")
		return
	}

	log.Info().Str("email", adminUser.Email).Msg("Создан администратор")
}

func main() {
	// Загрузка .env файла (если существует)
	if err := godotenv.Load(); err != nil {
		log.Info().Msg("Файл .env не найден, используются только системные переменные окружения")
	}

	// Настройка zerolog
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	log.Info().Str("version", version).Str("build", buildDate).Msg("Запуск сервера")

	cfg := config.LoadConfig()
	gin.SetMode(cfg.Server.GinMode)

	// Подключение к БД
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.Port,
		cfg.Database.User, cfg.Database.Password,
		cfg.Database.Name, cfg.Database.SSLMode,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: &gormLogger{logLevel: logger.Info},
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось подключиться к БД")
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось получить sql.DB")
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	// Миграции
	models := []interface{}{
		&user.User{}, &user.Achievement{}, &user.ExternalLogin{}, &user.PasswordResetToken{},
		&game.Game{}, &game.GamePassing{}, &game.GameSetting{}, &game.CoAuthor{}, &game.Note{},
		&game.LevelProgress{}, &game.Attempt{}, &game.Photo{},
		&level.Level{}, &level.Question{}, &level.Answer{},
		&team.Team{}, &team.Invitation{},
		&monitor.ChatRoom{}, &monitor.ChatMessage{}, &monitor.BlackboxVotingSession{}, &monitor.BlackboxVote{},
		&social.PlayerRating{}, &social.Follow{},
		&game.Review{}, &game.PlayerRating{},
		&admin.AuditLog{}, &admin.Backup{}, &audit.Entry{},
		&tournament.Tournament{}, &tournament.TournamentGame{}, &tournament.TournamentTeam{}, &tournament.TournamentResult{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		log.Fatal().Err(err).Msg("Ошибка миграции")
	}

	ensureAdmin(db, cfg)

	localStorage := storage.NewLocalStorage()
	hub := ws.NewRoomHub()
	go hub.Run()

	// Настройка роутера (вынесена в internal/app/router.go)
	r := app.SetupRouter(db, localStorage, hub, cfg, ".")

	// Добавляем middleware уровня main (должны быть после сессий/CSRF из router.go)
	r.Use(loggerMiddleware())

	r.GET("/healthz", func(c *gin.Context) {
		if err := sqlDB.Ping(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Опциональная аутентификация для главной страницы
	userAuthSvc := user.NewAuthService(db, cfg)
	optionalAuth := middleware.OptionalAuth(userAuthSvc)
	r.GET("/", optionalAuth, func(c *gin.Context) {
		if c.GetUint("userID") > 0 {
			c.Redirect(http.StatusFound, "/dashboard")
			return
		}
		c.HTML(http.StatusOK, "layout.html", gin.H{"ContentBlock": "home.html"})
	})

	// Фоновые задачи
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go game.CheckTimeouts(db, ctx)
	go game.CheckAutoStartGames(db, ctx)

	// TLS
	if cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != "" {
		certDir := filepath.Dir(cfg.TLS.CertFile)
		keyDir := filepath.Dir(cfg.TLS.KeyFile)
		if _, err := os.Stat(cfg.TLS.CertFile); os.IsNotExist(err) {
			log.Info().Msg("Сертификат не найден, генерирую самоподписанный...")
			if err := os.MkdirAll(certDir, 0755); err != nil {
				log.Fatal().Err(err).Msg("Не удалось создать директорию для сертификата")
			}
			if err := os.MkdirAll(keyDir, 0755); err != nil {
				log.Fatal().Err(err).Msg("Не удалось создать директорию для ключа")
			}
			generateSelfSignedCert(cfg.TLS.CertFile, cfg.TLS.KeyFile)
			log.Info().Msg("Самоподписанный сертификат сгенерирован")
		} else {
			log.Info().Msg("Использую существующий сертификат")
		}
	}

	port := cfg.Server.Port
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		if cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != "" {
			go func() {
				httpPort := "80"
				if port == "443" {
					httpPort = "80"
				}
				log.Info().Str("port", httpPort).Msg("Запущен HTTP-редирект")
				err := http.ListenAndServe(":"+httpPort, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					target := "https://" + r.Host + r.URL.RequestURI()
					http.Redirect(w, r, target, http.StatusMovedPermanently)
				}))
				if err != nil && err != http.ErrServerClosed {
					log.Fatal().Err(err).Msg("HTTP redirect server failed")
				}
			}()

			log.Info().Str("port", port).Msg("Starting HTTPS server")
			if err := srv.ListenAndServeTLS(cfg.TLS.CertFile, cfg.TLS.KeyFile); err != nil && err != http.ErrServerClosed {
				log.Fatal().Err(err).Msg("TLS listen")
			}
		} else {
			log.Info().Str("port", port).Msg("Starting HTTP server")
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatal().Err(err).Msg("listen")
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("Shutting down server...")

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal().Err(err).Msg("Server forced to shutdown")
	}

	if err := sqlDB.Close(); err != nil {
		log.Error().Err(err).Msg("Ошибка при закрытии БД")
	}

	log.Info().Msg("Server exited")
}

func generateSelfSignedCert(certFile, keyFile string) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось сгенерировать приватный ключ")
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Encounter Engine Self-Signed"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось создать сертификат")
	}

	certOut, err := os.Create(certFile)
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось создать файл сертификата")
	}
	defer func() { _ = certOut.Close() }()
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось создать файл ключа")
	}
	defer func() { _ = keyOut.Close() }()
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
}
