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
	// D1: новые методы интерфейса — заглушки с возвратом nil/пустых значений.
	// Конкретные тесты могут переопределять через поля при необходимости.
	upsertSettingsFn func(ctx context.Context, userID uint, settingsJSON string) error
	createFn         func(ctx context.Context, n *Notification) error
	countUnreadFn    func(ctx context.Context, userID uint) (int64, error)
	listFn           func(ctx context.Context, userID uint, offset, limit int, onlyUnread bool) ([]Notification, int64, error)
	markAsReadFn     func(ctx context.Context, userID, notificationID uint) (bool, error)
	markAllAsReadFn  func(ctx context.Context, userID uint) error
	subsFn           func(ctx context.Context, userID uint) ([]user.PushSubscription, error)
	deleteSubFn      func(ctx context.Context, id uint) error
	passingGameIDFn  func(ctx context.Context, passingID uint) (uint, error)
}

func (m *mockRepo) GetByUserID(ctx context.Context, userID uint) (*user.NotificationSetting, error) {
	return m.getByUserIDFn(ctx, userID)
}

func (m *mockRepo) Save(ctx context.Context, settings *user.NotificationSetting) error {
	return m.saveFn(ctx, settings)
}

func (m *mockRepo) UpsertSettings(ctx context.Context, userID uint, settingsJSON string) error {
	if m.upsertSettingsFn != nil {
		return m.upsertSettingsFn(ctx, userID, settingsJSON)
	}
	return nil
}

func (m *mockRepo) CreateNotification(ctx context.Context, n *Notification) error {
	if m.createFn != nil {
		return m.createFn(ctx, n)
	}
	return nil
}

func (m *mockRepo) CountUnread(ctx context.Context, userID uint) (int64, error) {
	if m.countUnreadFn != nil {
		return m.countUnreadFn(ctx, userID)
	}
	return 0, nil
}

func (m *mockRepo) ListByUser(ctx context.Context, userID uint, offset, limit int, onlyUnread bool) ([]Notification, int64, error) {
	if m.listFn != nil {
		return m.listFn(ctx, userID, offset, limit, onlyUnread)
	}
	return nil, 0, nil
}

func (m *mockRepo) ListRecentByUser(ctx context.Context, userID uint, limit int) ([]Notification, error) {
	if m.listFn != nil {
		items, _, err := m.listFn(ctx, userID, 0, limit, false)
		return items, err
	}
	return nil, nil
}

func (m *mockRepo) MarkAsRead(ctx context.Context, userID, notificationID uint) (bool, error) {
	if m.markAsReadFn != nil {
		return m.markAsReadFn(ctx, userID, notificationID)
	}
	return true, nil
}

func (m *mockRepo) MarkAllAsRead(ctx context.Context, userID uint) error {
	if m.markAllAsReadFn != nil {
		return m.markAllAsReadFn(ctx, userID)
	}
	return nil
}

func (m *mockRepo) DeleteOldRead(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}

func (m *mockRepo) ListPushSubscriptions(ctx context.Context, userID uint) ([]user.PushSubscription, error) {
	if m.subsFn != nil {
		return m.subsFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockRepo) DeletePushSubscription(ctx context.Context, id uint) error {
	if m.deleteSubFn != nil {
		return m.deleteSubFn(ctx, id)
	}
	return nil
}

func (m *mockRepo) GetGamePassingGameID(ctx context.Context, passingID uint) (uint, error) {
	if m.passingGameIDFn != nil {
		return m.passingGameIDFn(ctx, passingID)
	}
	return 0, nil
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
	svc := NewNotificationService(NewNotificationRepository(db), nil)
	require.NotNil(t, svc)
	assert.Nil(t, svc.hub)
}

func TestWithHub(t *testing.T) {
	db, _ := newMockDB(t)
	hub := ws.NewRoomHub()
	svc := NewNotificationService(NewNotificationRepository(db), nil).WithHub(hub)
	assert.Equal(t, hub, svc.hub)
}

func TestGetSettings_ReturnsFromRepo(t *testing.T) {
	_, _ = newMockDB(t)
	svc := &NotificationService{
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
	_, _ = newMockDB(t)
	svc := &NotificationService{
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
	_, _ = newMockDB(t)
	svc := &NotificationService{
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
	svc := &NotificationService{
		repo: &mockRepo{
			upsertSettingsFn: func(_ context.Context, _ uint, _ string) error {
				return nil
			},
		},
	}

	// Upsert через repo (ON CONFLICT в gorm-реализации, C-M1).
	err := svc.SaveSettings(context.Background(), 1, &Settings{EmailEnabled: true})
	require.NoError(t, err)
}

func TestSaveSettings_UpdatesExisting(t *testing.T) {
	svc := &NotificationService{
		repo: &mockRepo{
			upsertSettingsFn: func(_ context.Context, _ uint, _ string) error {
				return nil
			},
		},
	}

	err := svc.SaveSettings(context.Background(), 1, &Settings{EmailEnabled: false, PushEnabled: true})
	require.NoError(t, err)
}

func TestSaveSettings_GetError(t *testing.T) {
	svc := &NotificationService{
		repo: &mockRepo{
			upsertSettingsFn: func(_ context.Context, _ uint, _ string) error {
				return errors.New("upsert error")
			},
		},
	}

	err := svc.SaveSettings(context.Background(), 1, &Settings{})
	assert.Error(t, err)
}

func TestSaveSettings_SaveError(t *testing.T) {
	svc := &NotificationService{
		repo: &mockRepo{
			upsertSettingsFn: func(_ context.Context, _ uint, _ string) error {
				return errors.New("save error")
			},
		},
	}

	err := svc.SaveSettings(context.Background(), 1, &Settings{})
	assert.Error(t, err)
}

func TestGetEmailNotificationFlags(t *testing.T) {
	_, _ = newMockDB(t)
	svc := &NotificationService{
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
	_, _ = newMockDB(t)
	svc := &NotificationService{
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

	svc := NewNotificationService(NewNotificationRepository(db), nil)
	err := svc.Create(context.Background(), 1, NotificationTypeGameStarted, "Title", "Message", "/url")
	require.NoError(t, err)
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
	hub.Run()
	t.Cleanup(hub.Stop)

	svc := NewNotificationService(NewNotificationRepository(db), hub)
	err := svc.Create(context.Background(), 1, NotificationTypeGameStarted, "Title", "Message", "/url")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_DBError(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "notifications"`).
		WillReturnError(errors.New("insert error"))
	mock.ExpectRollback()

	svc := NewNotificationService(NewNotificationRepository(db), ws.NewRoomHub())
	err := svc.Create(context.Background(), 1, NotificationTypeGameStarted, "T", "M", "")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// MED-3 (pass 29): был двойной t.Skip — метод GetByUser не покрывался.
func TestGetByUser_DefaultPagination(t *testing.T) {
	svc := &NotificationService{
		repo: &mockRepo{
			listFn: func(_ context.Context, _ uint, offset, limit int, _ bool) ([]Notification, int64, error) {
				// page=1, perPage=0 → дефолты page=1, perPage=20 → offset=0, limit=20.
				assert.Equal(t, 0, offset)
				assert.Equal(t, 20, limit)
				return []Notification{{ID: 1}}, 1, nil
			},
		},
	}

	notifs, total, err := svc.GetByUser(context.Background(), 7, 0, 0, false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, notifs, 1)
	assert.Equal(t, uint(1), notifs[0].ID)
}

func TestGetByUser_Paginated(t *testing.T) {
	svc := &NotificationService{
		repo: &mockRepo{
			listFn: func(_ context.Context, _ uint, offset, limit int, _ bool) ([]Notification, int64, error) {
				// page=3, perPage=10 → offset=20, limit=10.
				assert.Equal(t, 20, offset)
				assert.Equal(t, 10, limit)
				return nil, 25, nil
			},
		},
	}

	notifs, total, err := svc.GetByUser(context.Background(), 7, 3, 10, false)
	require.NoError(t, err)
	assert.Equal(t, int64(25), total)
	assert.Empty(t, notifs)
}

func TestMarkAsRead(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "notifications" SET`).
		WithArgs(true, sqlmock.AnyArg(), sqlmock.AnyArg(), uint(10), uint(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	svc := NewNotificationService(NewNotificationRepository(db), nil)
	err := svc.MarkAsRead(context.Background(), 1, 10)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkAsRead_NotFound(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "notifications" SET`).
		WithArgs(true, sqlmock.AnyArg(), sqlmock.AnyArg(), uint(10), uint(1)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	svc := NewNotificationService(NewNotificationRepository(db), nil)
	err := svc.MarkAsRead(context.Background(), 1, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "notification not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkAllAsRead(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectBegin()
	// M2 (PASS-20): MarkAllAsRead теперь ставит read_at + updated_at (Updates map).
	mock.ExpectExec(`UPDATE "notifications" SET "read"`).
		WithArgs(true, sqlmock.AnyArg(), sqlmock.AnyArg(), uint(1), false).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	svc := NewNotificationService(NewNotificationRepository(db), nil)
	err := svc.MarkAllAsRead(context.Background(), 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUnreadCount(t *testing.T) {
	db, mock := newMockDB(t)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "notifications"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))

	svc := NewNotificationService(NewNotificationRepository(db), nil)
	count := svc.GetUnreadCount(context.Background(), 1)
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
	hub.Run()
	t.Cleanup(hub.Stop)

	svc := NewNotificationService(NewNotificationRepository(db), hub)
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
	hub.Run()
	t.Cleanup(hub.Stop)

	svc := NewNotificationService(NewNotificationRepository(db), hub)
	err := svc.SendTimeExpired(context.Background(), 1, 42)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// F-4 (pass 48): фильтр «только непрочитанные» пробрасывается в репозиторий.
func TestGetByUser_OnlyUnread(t *testing.T) {
	svc := &NotificationService{
		repo: &mockRepo{
			listFn: func(_ context.Context, _ uint, offset, limit int, onlyUnread bool) ([]Notification, int64, error) {
				assert.True(t, onlyUnread, "onlyUnread должен доходить до репозитория")
				return []Notification{{ID: 9, Read: false}}, 1, nil
			},
		},
	}

	notifs, total, err := svc.GetByUser(context.Background(), 7, 1, 20, true)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, notifs, 1)
	assert.False(t, notifs[0].Read)
}
