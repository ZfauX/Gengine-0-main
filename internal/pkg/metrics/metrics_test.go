package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetMetrics() {
	GamesTotal.Reset()
	GamePassingsTotal.Reset()
	AttemptsTotal.Reset()
	CacheHitsTotal.Reset()
	CacheMissesTotal.Reset()
}

func TestIncGamesCreated(t *testing.T) {
	resetMetrics()
	before := testutil.ToFloat64(GamesTotal.WithLabelValues("draft"))
	IncGamesCreated()
	after := testutil.ToFloat64(GamesTotal.WithLabelValues("draft"))
	assert.Equal(t, before+1, after)
}

func TestIncGamesPublished(t *testing.T) {
	resetMetrics()
	before := testutil.ToFloat64(GamesTotal.WithLabelValues("published"))
	IncGamesPublished()
	after := testutil.ToFloat64(GamesTotal.WithLabelValues("published"))
	assert.Equal(t, before+1, after)
}

func TestIncGamesDeleted(t *testing.T) {
	resetMetrics()
	before := testutil.ToFloat64(GamesTotal.WithLabelValues("deleted"))
	IncGamesDeleted()
	after := testutil.ToFloat64(GamesTotal.WithLabelValues("deleted"))
	assert.Equal(t, before+1, after)
}

func TestIncGames(t *testing.T) {
	resetMetrics()
	IncGames("archived")
	assert.Equal(t, 1.0, testutil.ToFloat64(GamesTotal.WithLabelValues("archived")))
}

func TestSetActiveGames(t *testing.T) {
	resetMetrics()
	SetActiveGames(42)
	assert.Equal(t, 42.0, testutil.ToFloat64(ActiveGames))
	SetActiveGames(10)
	assert.Equal(t, 10.0, testutil.ToFloat64(ActiveGames))
}

func TestIncTeamsTotal(t *testing.T) {
	resetMetrics()
	before := testutil.ToFloat64(TeamsTotal)
	IncTeamsTotal()
	after := testutil.ToFloat64(TeamsTotal)
	assert.Equal(t, before+1, after)
}

func TestIncUsersTotal(t *testing.T) {
	resetMetrics()
	before := testutil.ToFloat64(UsersTotal)
	IncUsersTotal()
	after := testutil.ToFloat64(UsersTotal)
	assert.Equal(t, before+1, after)
}

func TestSetActiveUsers(t *testing.T) {
	resetMetrics()
	SetActiveUsers(100)
	assert.Equal(t, 100.0, testutil.ToFloat64(ActiveUsers))
}

func TestIncGamePassings(t *testing.T) {
	resetMetrics()
	IncGamePassings("started")
	assert.Equal(t, 1.0, testutil.ToFloat64(GamePassingsTotal.WithLabelValues("started")))
	IncGamePassings("finished")
	assert.Equal(t, 1.0, testutil.ToFloat64(GamePassingsTotal.WithLabelValues("finished")))
	IncGamePassings("started")
	assert.Equal(t, 2.0, testutil.ToFloat64(GamePassingsTotal.WithLabelValues("started")))
}

func TestSetTeamMembersTotal(t *testing.T) {
	resetMetrics()
	SetTeamMembersTotal(5)
	assert.Equal(t, 5.0, testutil.ToFloat64(TeamMembersTotal))
}

func TestObserveGameDuration(t *testing.T) {
	resetMetrics()
	ObserveGameDuration(100)
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() == "gengine_game_duration_seconds" {
			require.Len(t, f.GetMetric(), 1)
			h := f.GetMetric()[0].GetHistogram()
			assert.Equal(t, uint64(1), h.GetSampleCount())
			assert.InDelta(t, 100, h.GetSampleSum(), 0.1)
			return
		}
	}
	t.Error("metric gengine_game_duration_seconds not found")
}

func TestIncWebSocketConnection(t *testing.T) {
	resetMetrics()
	IncWebSocketConnection()
	IncWebSocketConnection()
	assert.Equal(t, 2.0, testutil.ToFloat64(WebSocketConnections))
}

func TestDecWebSocketConnection(t *testing.T) {
	resetMetrics()
	WebSocketConnections.Set(5)
	DecWebSocketConnection()
	assert.Equal(t, 4.0, testutil.ToFloat64(WebSocketConnections))
}

func TestIncLevelProgress(t *testing.T) {
	resetMetrics()
	before := testutil.ToFloat64(LevelProgressTotal)
	IncLevelProgress()
	after := testutil.ToFloat64(LevelProgressTotal)
	assert.Equal(t, before+1, after)
}

func TestIncAttempt(t *testing.T) {
	resetMetrics()
	IncAttempt(true)
	assert.Equal(t, 1.0, testutil.ToFloat64(AttemptsTotal.WithLabelValues("true")))
	IncAttempt(false)
	assert.Equal(t, 1.0, testutil.ToFloat64(AttemptsTotal.WithLabelValues("false")))
	IncAttempt(true)
	assert.Equal(t, 2.0, testutil.ToFloat64(AttemptsTotal.WithLabelValues("true")))
}

func TestSetEmailQueueSize(t *testing.T) {
	resetMetrics()
	SetEmailQueueSize(99)
	assert.Equal(t, 99.0, testutil.ToFloat64(EmailQueueSize))
}

func TestSetDatabaseConnections(t *testing.T) {
	resetMetrics()
	SetDatabaseConnections(15)
	assert.Equal(t, 15.0, testutil.ToFloat64(DatabaseConnections))
}

func TestIncCacheHit(t *testing.T) {
	resetMetrics()
	IncCacheHit("users")
	assert.Equal(t, 1.0, testutil.ToFloat64(CacheHitsTotal.WithLabelValues("users")))
	IncCacheHit("users")
	assert.Equal(t, 2.0, testutil.ToFloat64(CacheHitsTotal.WithLabelValues("users")))
}

func TestIncCacheMiss(t *testing.T) {
	resetMetrics()
	IncCacheMiss("users")
	assert.Equal(t, 1.0, testutil.ToFloat64(CacheMissesTotal.WithLabelValues("users")))
}

func TestGaugeMetricsAreRegistered(t *testing.T) {
	expected := []string{
		"gengine_games_active",
		"gengine_teams_total",
		"gengine_users_total",
		"gengine_users_active",
		"gengine_team_members_total",
		"gengine_game_duration_seconds",
		"gengine_websocket_connections",
		"gengine_level_progress_total",
		"gengine_email_queue_size",
		"gengine_database_connections",
	}

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	registered := make(map[string]bool)
	for _, f := range families {
		registered[f.GetName()] = true
	}

	for _, name := range expected {
		assert.True(t, registered[name], "metric %q should be registered", name)
	}
}
