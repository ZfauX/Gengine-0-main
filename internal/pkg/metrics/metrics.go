// internal/pkg/metrics/metrics.go
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// GamesTotal - общее количество игр (по статусам)
	GamesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gengine_games_total",
		Help: "Total number of games created",
	}, []string{"status"}) // status: draft, published, deleted

	// ActiveGames - текущее количество активных (опубликованных) игр
	ActiveGames = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gengine_games_active",
		Help: "Number of currently active (published) games",
	})

	// TeamsTotal - общее количество созданных команд
	TeamsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gengine_teams_total",
		Help: "Total number of teams created",
	})

	// UsersTotal - общее количество зарегистрированных пользователей
	UsersTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gengine_users_total",
		Help: "Total number of registered users",
	})

	// GamePassingsTotal - общее количество прохождений по статусам
	GamePassingsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gengine_game_passings_total",
		Help: "Total number of game passings",
	}, []string{"status"}) // status: started, finished, testing, disqualified

	// TeamMembersTotal - текущее количество участников команд
	TeamMembersTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gengine_team_members_total",
		Help: "Total number of team members across all teams",
	})

	// GameDurationSeconds - гистограмма длительности прохождений
	GameDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "gengine_game_duration_seconds",
		Help:    "Duration of game passings in seconds",
		Buckets: []float64{60, 300, 600, 1800, 3600, 7200, 14400, 28800}, // 1m, 5m, 10m, 30m, 1h, 2h, 4h, 8h
	})

	// WebSocketConnections - текущее количество активных WebSocket-соединений
	WebSocketConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gengine_websocket_connections",
		Help: "Number of currently active WebSocket connections",
	})
)

// IncGamesCreated инкрементирует счетчик созданных игр (draft)
func IncGamesCreated() {
	GamesTotal.WithLabelValues("draft").Inc()
}

// IncGamesPublished инкрементирует счетчик опубликованных игр
func IncGamesPublished() {
	GamesTotal.WithLabelValues("published").Inc()
}

// IncGamesDeleted инкрементирует счетчик удаленных игр
func IncGamesDeleted() {
	GamesTotal.WithLabelValues("deleted").Inc()
}

// SetActiveGames устанавливает текущее количество активных игр
func SetActiveGames(count float64) {
	ActiveGames.Set(count)
}

// IncTeamsTotal инкрементирует счетчик команд
func IncTeamsTotal() {
	TeamsTotal.Inc()
}

// IncUsersTotal инкрементирует счетчик пользователей
func IncUsersTotal() {
	UsersTotal.Inc()
}

// IncGamePassings инкрементирует счетчик прохождений по статусу
func IncGamePassings(status string) {
	GamePassingsTotal.WithLabelValues(status).Inc()
}

// SetTeamMembersTotal устанавливает текущее количество участников команд
func SetTeamMembersTotal(count float64) {
	TeamMembersTotal.Set(count)
}

// ObserveGameDuration записывает длительность прохождения в гистограмму
func ObserveGameDuration(seconds float64) {
	GameDurationSeconds.Observe(seconds)
}

// IncWebSocketConnection увеличивает счетчик активных WebSocket-соединений
func IncWebSocketConnection() {
	WebSocketConnections.Inc()
}

// DecWebSocketConnection уменьшает счетчик активных WebSocket-соединений
func DecWebSocketConnection() {
	WebSocketConnections.Dec()
}
