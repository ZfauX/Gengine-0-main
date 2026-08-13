//go:build swagger

// internal/app/swagger.go
//
// Регистрация /swagger за build-tag `swagger` (P-1, PASS-13).
//
// Почему: пакет docs (swaggo/files) держит в памяти ~9.5MB (webdav memFile) —
// по pprof это 51% heap inuse. Для production-сервера, где /swagger доступен
// только админам (+2FA), эта память не нужна. Сборка БЕЗ тега swagger не
// импортирует docs и не резервирует память.
//
// Сборка с swagger: go build -tags=swagger ./cmd/server
// (Makefile: make build-swagger).

package app

import (
	"net/url"

	"gengine-0/docs"
	"gengine-0/internal/pkg/middleware"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// registerSwagger регистрирует /swagger под защитой admin+2FA (S-1, S5).
// В обычной сборке (без тега swagger) это no-op — см. swagger_off.go.
func (app *App) registerSwagger(r *gin.Engine, twoFactorMW gin.HandlerFunc) {
	r.GET("/swagger/*any",
		middleware.AuthRequired(app.Deps.Services.Auth),
		twoFactorMW,
		middleware.AdminRequired(),
		func(c *gin.Context) {
			ginSwagger.WrapHandler(swaggerFiles.Handler)(c)
		})
}

// configureSwaggerInfo устанавливает host/basePath из конфига. Вызывается из
// main.go; в обычной сборке (без тега swagger) это no-op (см. swagger_off.go).
func configureSwaggerInfo(baseURL string) {
	host := "localhost:8080"
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	docs.SwaggerInfo.Host = host
	docs.SwaggerInfo.BasePath = "/"
}

// ConfigureSwagger — экспортируемая точка входа из main.go.
func ConfigureSwagger(baseURL string) {
	configureSwaggerInfo(baseURL)
}
