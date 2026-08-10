// internal/domain/user/search_users_test.go
package user

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gengine-0/internal/testutil"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSearchUsersAPI_ShortQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Empty query (less than 2 chars) should return empty result without hitting DB
	handler := SearchUsersAPI(nil) // nil DB is safe because short-circuit returns before DB query

	tests := []struct {
		name  string
		query string
	}{
		{"empty query", ""},
		{"single char", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest(http.MethodGet, "/api/users/search?q="+tt.query, nil)
			c.Request = req

			handler(c)

			assert.Equal(t, http.StatusOK, w.Code)

			var resp struct {
				Users []any `json:"users"`
			}
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Empty(t, resp.Users, "should return empty users array for short query")
		})
	}
}

func TestSearchUsersAPI_Results(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (requires PostgreSQL)")
	}

	gin.SetMode(gin.TestMode)

	db := testutil.SetupPostgresDB(t, &User{})

	// Create test users
	testUsers := []User{
		{Email: "alice@example.com", Password: "hashed1", Name: "Alice Wonderland"},
		{Email: "bob@example.com", Password: "hashed2", Name: "Bob Builder"},
		{Email: "charlie@example.com", Password: "hashed3", Name: "Charlie Brown"},
	}
	for _, u := range testUsers {
		require.NoError(t, db.Create(&u).Error)
	}

	handler := SearchUsersAPI(NewGormUserRepo(db))

	tests := []struct {
		name         string
		query        string
		expectedLen  int
		checkResults func(t *testing.T, users []map[string]any)
	}{
		{
			name:        "search by name",
			query:       "alice",
			expectedLen: 1,
			checkResults: func(t *testing.T, users []map[string]any) {
				assert.Equal(t, "Alice Wonderland", users[0]["name"])
				// Email is masked for non-admin users (PII protection)
				assert.Equal(t, "a***@example.com", users[0]["email"])
			},
		},
		{
			name:        "search by email",
			query:       "bob@example",
			expectedLen: 1,
			checkResults: func(t *testing.T, users []map[string]any) {
				assert.Equal(t, "Bob Builder", users[0]["name"])
			},
		},
		{
			name:        "search partial",
			query:       "charl",
			expectedLen: 1,
		},
		{
			name:        "search all",
			query:       "@example",
			expectedLen: 3,
		},
		{
			name:        "no results",
			query:       "nonexistent",
			expectedLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest(http.MethodGet, "/api/users/search?q="+tt.query, nil)
			c.Request = req
			// S-2 (pass 34): неавторизованные пользователи не видят email даже
			// в маске (защита PII). Авторизованные видят маску, админы — полный.
			c.Set("userID", uint(1))
			c.Set("role", "user")

			handler(c)

			assert.Equal(t, http.StatusOK, w.Code)

			var resp struct {
				Users []map[string]any `json:"users"`
			}
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Len(t, resp.Users, tt.expectedLen, "unexpected number of results for query %q", tt.query)

			if tt.checkResults != nil && len(resp.Users) > 0 {
				tt.checkResults(t, resp.Users)
			}
		})
	}
}

// TestSearchUsersAPI_AnonymousNoEmail проверяет, что аноним не получает email
// (S-2, pass 34): публичный поиск не раскрывает email даже в маске.
func TestSearchUsersAPI_AnonymousNoEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (requires PostgreSQL)")
	}

	gin.SetMode(gin.TestMode)

	db := testutil.SetupPostgresDB(t, &User{})

	u := User{Email: "anon@example.com", Password: "hashed1", Name: "Anon User"}
	require.NoError(t, db.Create(&u).Error)

	handler := SearchUsersAPI(NewGormUserRepo(db))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/api/users/search?q=anon", nil)
	c.Request = req
	// Нет userID в контексте — аноним.

	handler(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Users []map[string]any `json:"users"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Users, 1)
	assert.Equal(t, "", resp.Users[0]["email"], "аноним не должен видеть email")
}

func TestSearchUsersAPI_NoDB(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// When DB is nil and query is >= 2 chars, handler will panic with nil pointer.
	// This test ensures the short-query path returns early without touching DB.
	handler := SearchUsersAPI(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/api/users/search?q=a", nil)
	c.Request = req

	handler(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Users []any `json:"users"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Empty(t, resp.Users)
}

// TestSearchUsersAPI_DBError verifies the handler returns 500 when DB query fails.
// We use a closed DB connection to simulate an error.
func TestSearchUsersAPI_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create a DB connection and immediately close it
	// This requires a real Postgres, skip if short.
	if testing.Short() {
		t.Skip("skipping integration test (requires PostgreSQL)")
	}

	db := testutil.SetupPostgresDB(t, &User{})
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.Close() // close connection to cause query error

	handler := SearchUsersAPI(NewGormUserRepo(db))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/api/users/search?q=test", nil)
	c.Request = req

	handler(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var resp struct {
		Error string `json:"error"`
	}
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	// Error message is localized (handler.operation_failed → RU)
	assert.Contains(t, resp.Error, "Не удалось выполнить операцию")
}

// ensure unused import doesn't cause compile error
var _ = gorm.DB{}
