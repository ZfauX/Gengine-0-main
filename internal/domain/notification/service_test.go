package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"gengine-0/internal/domain/user"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type mockRepo struct {
	getByUserIDFn func(ctx context.Context, userID uint) (*user.NotificationSetting, error)
	saveFn        func(ctx context.Context, settings *user.NotificationSetting) error
}

func (m *mockRepo) GetByUserID(ctx context.Context, userID uint) (*user.NotificationSetting, error) {
	return m.getByUserIDFn(ctx, userID)
}

func (m *mockRepo) Save(ctx context.Context, settings *user.NotificationSetting) error {
	return m.saveFn(ctx, settings)
}

func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)
	return gormDB, mock
}

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	assert.True(t, s.EmailEnabled)
	assert.True(t, s.BrowserEnabled)
	assert.False(t, s.PushEnabled)
	assert.True(t, s.EmailGameStarted)
	assert.True(t, s.EmailLevelCompleted)
	assert.True(t, s.EmailApplicationAccepted)
	assert.False(t, s.EmailApplicationRejected)
	assert.True(t, s.EmailTimeWarning)
	assert.True(t, s.EmailTimeExpired)
}

func TestNewNotificationService(t *testing.T) {
	db, _ := newMockDB(t)
	svc := NewNotificationService(db, nil)
	require.NotNil(t, svc)
	assert.Equal(t, db, svc.db)
	assert.Nil(t, svc.hub)
}

func TestWithHub(t *testing.T) {
	db, _ := newMockDB(t)
	hub := ws.NewRoomHub()
	svc := NewNotificationService(db, nil).WithHub(hub)
	assert.Equal(t, hub, svc.hub)
}

func TestGetSettings_ReturnsFromRepo(t *testing.T) {
	db, _ := newMockDB(t)
	svc := &NotificationService{
		db: db,
		repo: &mockRepo{
			getByUserIDFn: func(_ context.Context, _ uint) (*user.NotificationSetting, error) {
				return &user.NotificationSetting{
					UserID:       1,
					SettingsJSON: `{"email_enabled":true,"browser_enabled":true,"email_game_started":false,"email_level_completed":true}`,
				}, nil
			},
		},
	}

	settings, err := svc.GetSettings(context.Background(), 1)
	require.NoError(t, err)
	assert.True(t, settings.EmailEnabled)
	assert.False(t, settings.EmailGameStarted)
	assert.True(t, settings.EmailLevelCompleted)
}

func TestGetSettings_NotFound_ReturnsDefaults(t *testing.T) {
	db, _ := newMockDB(t)
	svc := &NotificationService{
		db: db,
		repo: &mockRepo{
			getByUserIDFn: func(_ context.Context, _ uint) (*user.NotificationSetting, error) {
				return nil, gorm.ErrRecordNotFound
			},
		},
	}

	settings, err := svc.GetSettings(context.Background(), 1)
	require.NoError(t, err)
	assert.True(t, settings.EmailEnabled)
}

func TestGetSettings_RepoError(t *testing.T) {
	db, _ := newMockDB(t)
	svc := &NotificationService{
		db: db,
		repo: &mockRepo{
			getByUserIDFn: func(_ context.Context, _ uint) (*user.NotificationSetting, error) {
				return nil, errors.New("db error")
			},
		},
	}

	_, err := svc.GetSettings(context.Background(), 1)
	assert.Error(t, err)
}

func TestSaveSettings_CreatesNew(t *testing.T) {
	db, _ := newMockDB(t)
	svc := &NotificationService{
		db: db,
		repo: &mockRepo{
			getByUserIDFn: func(_ context.Context, _ uint) (*user.NotificationSetting, error) {
				return nil, gorm.ErrRecordNotFound
			},
			saveFn: func(_ context.Context, s *user.NotificationSetting) error {
				assert.Equal(t, uint(1), s.UserID)
				assert.Contains(t, s.SettingsJSON, "email_enabled")
				return nil
			},
		},
	}

	err := svc.SaveSettings(context.Background(), 1, &Settings{EmailEnabled: true})
	assert.NoError(t, err)
}

func TestSaveSettings_UpdatesExisting(t *testing.T) {
	db, _ := newMockDB(t)
	svc := &NotificationService{
		db: db,
		repo: &mockRepo{
			getByUserIDFn: func(_ context.Context, _ uint) (*user.NotificationSetting, error) {
				return &user.NotificationSetting{
					UserID:       1,
					SettingsJSON: `{"email_enabled":true}`,
				}, nil
			},
			saveFn: func(_ context.Context, s *user.NotificationSetting) error {
				assert.Equal(t, uint(1), s.UserID)
				assert.Contains(t, s.SettingsJSON, "push_enabled")
				return nil
			},
		},
	}

	err := svc.SaveSettings(context.Background(), 1, &Settings{EmailEnabled: false, PushEnabled: true})
	assert.NoError(t, err)
}

func TestSaveSettings_GetError(t *testing.T) {
	db, _ := newMockDB(t)
	svc := &NotificationService{
		db: db,
		repo: &mockRepo{
			getByUserIDFn: func(_ context.Context, _ uint) (*user.NotificationSetting, error) {
				return nil, errors.New("get error")
			},
		},
	}

	err := svc.SaveSettings(context.Background(), 1, &Settings{})
	assert.Error(t, err)
}

func TestSaveSettings_SaveError(t *testing.T) {
	db, _ := newMockDB(t)
	svc := &NotificationService{
		db: db,
		repo: &mockRepo{
			getByUserIDFn: func(_ context.Context, _ uint) (*user.NotificationSetting, error) {
				return nil, gorm.ErrRecordNotFound
			},
			saveFn: func(_ context.Context, _ *user.NotificationSetting) error {
				return errors.New("save error")
			},
		},
	}

	err := svc.SaveSettings(context.Background(), 1, &Settings{})
	assert.Error(t, err)
}

func TestGetEmailNotificationFlags(t *testing.T) {
	db, _ := newMockDB(t)
	svc := &NotificationService{
		db: db,
		repo: &mockRepo{
			getByUserIDFn: func(_ context.Context, _ uint) (*user.NotificationSetting, error) {
				return &user.NotificationSetting{
					SettingsJSON: `{"email_enabled":true,"browser_enabled":true,"email_game_started":true,"email_level_completed":false,"email_application_accepted":true,"email_application_rejected":false,"email_time_warning":true,"email_time_expired":false,"push_enabled":false}`,
				}, nil
			},
		},
	}

	flags, err := svc.GetEmailNotificationFlags(context.Background(), 1)
	require.NoError(t, err)
	assert.True(t, flags["email_enabled"].(bool))
	assert.False(t, flags["email_level_completed"].(bool))
	assert.Equal(t, 9, len(flags))
}

func TestGetEmailNotificationFlags_Error(t *testing.T) {
	db, _ := newMockDB(t)
	svc := &NotificationService{
		db: db,
		repo: &mockRepo{
			getByUserIDFn: func(_ context.Context, _ uint) (*user.NotificationSetting, error) {
				return nil, errors.New("err")
			},
		},
	}

	_, err := svc.GetEmailNotificationFlags(context.Background(), 1)
	assert.Error(t, err)
}

func TestCreate_SavesToDB(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "notifications"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	svc := NewNotificationService(db, nil)
	err := svc.Create(context.Background(), 1, NotificationTypeGameStarted, "Title", "Message", "/url", "")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_WithHub_SendsWebSocket(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "notifications"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, time.Now()))
	mock.ExpectCommit()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "notifications"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	hub := ws.NewRoomHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	svc := NewNotificationService(db, hub)
	err := svc.Create(context.Background(), 1, NotificationTypeGameStarted, "Title", "Message", "/url", "")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_DBError(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "notifications"`).
		WillReturnError(errors.New("insert error"))
	mock.ExpectRollback()

	svc := NewNotificationService(db, ws.NewRoomHub())
	err := svc.Create(context.Background(), 1, NotificationTypeGameStarted, "T", "M", "", "")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetByUser_DefaultPagination(t *testing.T) {
	// This method requires Model() to be set for Count — tested in integration.
	t.Skip("requires real DB (GORM needs Model for Count with sqlmock)")
}

func TestMarkAsRead(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "notifications" SET`).
		WithArgs(true, sqlmock.AnyArg(), uint(10), uint(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	svc := NewNotificationService(db, nil)
	err := svc.MarkAsRead(context.Background(), 1, 10)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkAsRead_NotFound(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "notifications" SET`).
		WithArgs(true, sqlmock.AnyArg(), uint(10), uint(1)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	svc := NewNotificationService(db, nil)
	err := svc.MarkAsRead(context.Background(), 1, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "notification not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkAllAsRead(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "notifications" SET "read"`).
		WithArgs(true, sqlmock.AnyArg(), uint(1), false).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	svc := NewNotificationService(db, nil)
	err := svc.MarkAllAsRead(context.Background(), 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUnreadCount(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "notifications"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	svc := NewNotificationService(db, nil)
	count := svc.GetUnreadCount(1)
	assert.Equal(t, 5, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSendTimeWarning(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "notifications"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, time.Now()))
	mock.ExpectCommit()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "notifications"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	hub := ws.NewRoomHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	svc := NewNotificationService(db, hub)
	err := svc.SendTimeWarning(context.Background(), 1, 42, 300)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSendTimeExpired(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "notifications"`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, time.Now()))
	mock.ExpectCommit()

	mock.ExpectQuery(`SELECT count\(\*\) FROM "notifications"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	hub := ws.NewRoomHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	svc := NewNotificationService(db, hub)
	err := svc.SendTimeExpired(context.Background(), 1, 42)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
