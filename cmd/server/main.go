// cmd/server/main.go
package main

import (
	"context"
	"os"

	"gengine-0/internal/app"
	"gengine-0/internal/config"
	"gengine-0/internal/db"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Версия и дата сборки (заполняются при линковке)
var (
	version   = "dev"
	buildDate = "unknown"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Info().Msg("Файл .env не найден, используются только системные переменные окружения")
	}

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	log.Info().Str("version", version).Str("build", buildDate).Msg("Запуск сервера")

	cfg := config.LoadConfig()
	gin.SetMode(cfg.Server.GinMode)

	database, err := db.Connect(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Не удалось подключиться к БД")
	}

	if err := db.Migrate(database); err != nil {
		log.Fatal().Err(err).Msg("Ошибка миграции")
	}

	db.EnsureAdmin(database, cfg)

	localStorage := storage.NewLocalStorage()
	hub := ws.NewRoomHub()
	go hub.Run()

	r := app.SetupRouter(database, localStorage, hub, cfg, ".")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go game.CheckTimeouts(database, ctx)
	go game.CheckAutoStartGames(database, ctx)

	app.RunServer(r, database, cfg, cancel)
}
