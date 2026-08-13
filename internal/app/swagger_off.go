//go:build !swagger

// internal/app/swagger_off.go
//
// Заглушка для сборки БЕЗ тега swagger (P-1, PASS-13): не импортирует docs
// (swaggo/files — 9.5MB heap), /swagger не регистрируется.
// Включить swagger: go build -tags=swagger ./cmd/server.

package app

import "github.com/gin-gonic/gin"

// registerSwagger — no-op без тега swagger.
func (app *App) registerSwagger(_ *gin.Engine, _ gin.HandlerFunc) {}

// ConfigureSwagger — no-op без тега swagger.
func ConfigureSwagger(string) {}
