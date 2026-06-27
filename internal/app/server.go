// internal/app/server.go
package app

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

	"gengine-0/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// RunServer запускает HTTP(S) сервер с graceful shutdown и опциональным TLS.
// cancel вызывается при получении сигнала к завершению — до shutdown.
func RunServer(r *gin.Engine, db *gorm.DB, cfg *config.Config, cancel context.CancelFunc) {
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось получить sql.DB")
	}

	r.Use(LoggerMiddleware())

	r.GET("/healthz", func(c *gin.Context) {
		if err := sqlDB.Ping(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	if cfg.TLS.CertFile != "" && cfg.TLS.KeyFile != "" {
		if err := ensureTLSCert(cfg.TLS.CertFile, cfg.TLS.KeyFile); err != nil {
			log.Fatal().Err(err).Msg("Не удалось подготовить TLS-сертификат")
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
				if err := startHTTPRedirect(port); err != nil {
					log.Error().Err(err).Msg("HTTP redirect server failed")
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

// ensureTLSCert проверяет существование сертификата, при необходимости генерирует самоподписанный.
// Возвращает ошибку, если не удалось создать директории или сгенерировать сертификат.
func ensureTLSCert(certFile, keyFile string) error {
	certDir := filepath.Dir(certFile)
	keyDir := filepath.Dir(keyFile)
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		log.Info().Msg("Сертификат не найден, генерирую самоподписанный...")
		if err := os.MkdirAll(certDir, 0755); err != nil {
			return fmt.Errorf("не удалось создать директорию для сертификата: %w", err)
		}
		if err := os.MkdirAll(keyDir, 0755); err != nil {
			return fmt.Errorf("не удалось создать директорию для ключа: %w", err)
		}
		if err := generateSelfSignedCert(certFile, keyFile); err != nil {
			return fmt.Errorf("не удалось сгенерировать самоподписанный сертификат: %w", err)
		}
		log.Info().Msg("Самоподписанный сертификат сгенерирован")
	} else {
		log.Info().Msg("Использую существующий сертификат")
	}
	return nil
}

// startHTTPRedirect запускает HTTP-сервер, который перенаправляет все запросы на HTTPS.
// Возвращает ошибку, если не удалось запустить сервер.
func startHTTPRedirect(httpsPort string) error {
	httpPort := "80"
	if httpsPort == "443" {
		httpPort = "80"
	}
	log.Info().Str("port", httpPort).Msg("Запущен HTTP-редирект")
	err := http.ListenAndServe(":"+httpPort, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := "https://" + r.Host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}))
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP redirect server failed: %w", err)
	}
	return nil
}

// generateSelfSignedCert генерирует самоподписанный сертификат и сохраняет его в файлы.
// Возвращает ошибку при сбое генерации или записи.
func generateSelfSignedCert(certFile, keyFile string) error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("не удалось сгенерировать приватный ключ: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Encounter Engine Self-Signed"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("не удалось создать сертификат: %w", err)
	}

	certOut, err := os.Create(certFile)
	if err != nil {
		return fmt.Errorf("не удалось создать файл сертификата: %w", err)
	}
	defer func() {
		_ = certOut.Close()
	}()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return fmt.Errorf("не удалось записать сертификат в PEM: %w", err)
	}

	keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("не удалось создать файл ключа: %w", err)
	}
	defer func() {
		_ = keyOut.Close()
	}()

	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}); err != nil {
		return fmt.Errorf("не удалось записать ключ в PEM: %w", err)
	}
	return nil
}
