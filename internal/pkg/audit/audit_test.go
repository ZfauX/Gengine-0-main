// internal/pkg/audit/audit_test.go
package audit_test

import (
	"context"
	"strconv"
	"testing"

	"gengine-0/internal/pkg/audit"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// testUser – минимальная модель для таблицы users.
// GORM по умолчанию создал бы таблицу test_users, но audit.go выполняет LEFT JOIN users,
// поэтому принудительно задаём имя таблицы "users".
type testUser struct {
	gorm.Model
	Name string
}

func (testUser) TableName() string { return "users" }

// setupAuditTestDB создаёт тестовую БД с таблицами audit_logs и users.
func setupAuditTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t, &audit.Entry{}, &testUser{})
}

// createTestUser создаёт тестового пользователя через GORM и возвращает его ID.
func createTestUser(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	u := testUser{Name: name}
	require.NoError(t, db.Create(&u).Error)
	return u.ID
}

func TestAuditService_Log(t *testing.T) {
	db := setupAuditTestDB(t)
	svc := audit.NewService(db)

	userID := createTestUser(t, db, "alice")

	svc.Log(userID, "login", "user", userID, "user logged in")

	var entries []audit.Entry
	err := db.Find(&entries).Error
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	e := entries[0]
	assert.Equal(t, userID, e.UserID)
	assert.Equal(t, "login", e.Action)
	assert.Equal(t, "user", e.ObjectType)
	assert.Equal(t, userID, e.ObjectID)
	assert.Equal(t, "user logged in", e.Details)
}

func TestAuditService_Count(t *testing.T) {
	db := setupAuditTestDB(t)
	svc := audit.NewService(db)

	userID1 := createTestUser(t, db, "alice")
	userID2 := createTestUser(t, db, "bob")

	svc.Log(userID1, "login", "user", userID1, "")
	svc.Log(userID1, "logout", "user", userID1, "")
	svc.Log(userID2, "login", "user", userID2, "")

	count, err := svc.Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

func TestAuditService_List_Pagination(t *testing.T) {
	db := setupAuditTestDB(t)
	svc := audit.NewService(db)

	userID := createTestUser(t, db, "charlie")

	for i := 0; i < 10; i++ {
		svc.Log(userID, "action_"+strconv.Itoa(i), "test", uint(i), "details")
	}

	entries, total, err := svc.List(context.Background(), "", "", 1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(10), total)
	assert.Len(t, entries, 3)
	assert.NotEmpty(t, entries[0].ID)
}

func TestAuditService_List_FilterByUser(t *testing.T) {
	db := setupAuditTestDB(t)
	svc := audit.NewService(db)

	userID1 := createTestUser(t, db, "dave")
	userID2 := createTestUser(t, db, "eve")

	svc.Log(userID1, "login", "user", userID1, "")
	svc.Log(userID2, "login", "user", userID2, "")
	svc.Log(userID1, "logout", "user", userID1, "")

	entries, total, err := svc.List(context.Background(), strconv.FormatUint(uint64(userID1), 10), "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, entries, 2)
	for _, e := range entries {
		assert.Equal(t, userID1, e.UserID)
	}
}

func TestAuditService_List_FilterByAction(t *testing.T) {
	db := setupAuditTestDB(t)
	svc := audit.NewService(db)

	userID := createTestUser(t, db, "frank")

	svc.Log(userID, "login", "user", userID, "")
	svc.Log(userID, "logout", "user", userID, "")
	svc.Log(userID, "update", "game", 1, "")

	entries, total, err := svc.List(context.Background(), "", "login", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, entries, 1)
	assert.Equal(t, "login", entries[0].Action)
}

func TestAuditService_List_CombinedFilter(t *testing.T) {
	db := setupAuditTestDB(t)
	svc := audit.NewService(db)

	userID1 := createTestUser(t, db, "grace")
	userID2 := createTestUser(t, db, "henry")

	svc.Log(userID1, "login", "user", userID1, "")
	svc.Log(userID1, "logout", "user", userID1, "")
	svc.Log(userID2, "login", "user", userID2, "")

	entries, total, err := svc.List(context.Background(), strconv.FormatUint(uint64(userID1), 10), "login", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, entries, 1)
	assert.Equal(t, userID1, entries[0].UserID)
	assert.Equal(t, "login", entries[0].Action)
}

func TestAuditService_List_Empty(t *testing.T) {
	db := setupAuditTestDB(t)
	svc := audit.NewService(db)

	entries, total, err := svc.List(context.Background(), "", "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, entries)
}

func TestAuditService_Log_DoesNotPanicOnError(t *testing.T) {
	db := setupAuditTestDB(t)
	svc := audit.NewService(db)

	// Удаляем таблицу audit_logs, чтобы вызвать ошибку
	require.NoError(t, db.Migrator().DropTable(&audit.Entry{}))

	// Логирование не должно паниковать, только логировать ошибку
	svc.Log(1, "test", "test", 1, "test")

	// Проверяем, что ничего не сломалось
	assert.True(t, true)
}
