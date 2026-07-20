package health

import (
	"context"
	"testing"

	ws "gengine-0/internal/pkg/websocket"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	assert.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectPing().WillReturnError(nil)
	rows := sqlmock.NewRows([]string{"count"}).AddRow(0)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "queued_emails"`).WillReturnRows(rows)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	assert.NoError(t, err)
	return gormDB, mock
}

func TestChecker_HubNil(t *testing.T) {
	gormDB, _ := newMockDB(t)
	checker := NewChecker(gormDB, nil)
	resp := checker.Check(context.Background())
	assert.Equal(t, "error", resp.Components["websocket_hub"].Status)
}

func TestChecker_HubStopped(t *testing.T) {
	gormDB, _ := newMockDB(t)
	hub := ws.NewRoomHub()
	hub.Stop()
	checker := NewChecker(gormDB, hub)
	resp := checker.Check(context.Background())
	assert.Equal(t, "error", resp.Components["websocket_hub"].Status)
}

func TestChecker_ResponseStructure(t *testing.T) {
	gormDB, mock := newMockDB(t)
	hub := ws.NewRoomHub()
	checker := NewChecker(gormDB, hub)
	resp := checker.Check(context.Background())
	assert.NotEmpty(t, resp.Status)
	assert.NotEmpty(t, resp.Timestamp)
	assert.Contains(t, resp.Components, "database")
	assert.Contains(t, resp.Components, "websocket_hub")
	assert.Contains(t, resp.Components, "email_queue")
	_ = mock
}
