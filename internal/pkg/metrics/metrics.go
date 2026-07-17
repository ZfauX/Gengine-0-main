// internal/pkg/metrics/metrics.go
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// --- HTTP-метрики ---
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gengine_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "route", "status"},
	)

	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gengine_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"method", "route"},
	)

	RequestSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gengine_http_request_size_bytes",
			Help:    "HTTP request size in bytes",
			Buckets: []float64{100, 500, 1000, 5000, 10000, 50000, 100000, 500000, 1000000},
		},
		[]string{"method", "route"},
	)

	ResponseSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gengine_http_response_size_bytes",
			Help:    "HTTP response size in bytes",
			Buckets: []float64{100, 500, 1000, 5000, 10000, 50000, 100000, 500000, 1000000},
		},
		[]string{"method", "route"},
	)

	// --- Бизнес-метрики ---

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

	// ActiveUsers - текущее количество активных пользователей (вошедших за последние 30 мин)
	ActiveUsers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gengine_users_active",
		Help: "Number of currently active users (logged in within last 30 min)",
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

	// LevelProgressTotal - общее количество пройденных уровней
	LevelProgressTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gengine_level_progress_total",
		Help: "Total number of level progresses (completed levels)",
	})

	// AttemptsTotal - общее количество попыток ввода кодов
	AttemptsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gengine_attempts_total",
		Help: "Total number of attempts (code submissions)",
	}, []string{"success"}) // success: true/false

	// EmailQueueSize - текущий размер очереди email
	EmailQueueSize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gengine_email_queue_size",
		Help: "Current size of email queue",
	})

	// DatabaseConnections - текущее количество открытых соединений с БД
	DatabaseConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gengine_database_connections",
		Help: "Number of currently open database connections",
	})

	// CacheHitsTotal - общее количество попаданий в кэш
	CacheHitsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gengine_cache_hits_total",
		Help: "Total number of cache hits",
	}, []string{"cache_name"})

	// CacheMissesTotal - общее количество промахов в кэш
	CacheMissesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gengine_cache_misses_total",
		Help: "Total number of cache misses",
	}, []string{"cache_name"})
)

// --- Хелперы для инкремента ---

func IncGamesCreated() {
	GamesTotal.WithLabelValues("draft").Inc()
}

func IncGamesPublished() {
	GamesTotal.WithLabelValues("published").Inc()
}

func IncGamesDeleted() {
	GamesTotal.WithLabelValues("deleted").Inc()
}

func SetActiveGames(count float64) {
	ActiveGames.Set(count)
}

func IncTeamsTotal() {
	TeamsTotal.Inc()
}

func IncUsersTotal() {
	UsersTotal.Inc()
}

func SetActiveUsers(count float64) {
	ActiveUsers.Set(count)
}

func IncGamePassings(status string) {
	GamePassingsTotal.WithLabelValues(status).Inc()
}

func SetTeamMembersTotal(count float64) {
	TeamMembersTotal.Set(count)
}

func ObserveGameDuration(seconds float64) {
	GameDurationSeconds.Observe(seconds)
}

func IncWebSocketConnection() {
	WebSocketConnections.Inc()
}

func DecWebSocketConnection() {
	WebSocketConnections.Dec()
}

func IncLevelProgress() {
	LevelProgressTotal.Inc()
}

func IncAttempt(success bool) {
	status := "false"
	if success {
		status = "true"
	}
	AttemptsTotal.WithLabelValues(status).Inc()
}

func SetEmailQueueSize(size float64) {
	EmailQueueSize.Set(size)
}

func SetDatabaseConnections(conns float64) {
	DatabaseConnections.Set(conns)
}

func IncCacheHit(cacheName string) {
	CacheHitsTotal.WithLabelValues(cacheName).Inc()
}

func IncCacheMiss(cacheName string) {
	CacheMissesTotal.WithLabelValues(cacheName).Inc()
}
