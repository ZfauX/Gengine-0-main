// internal/pkg/sessionstore/sessionstore_test.go
package sessionstore

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	gcsessions "github.com/gin-contrib/sessions"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKeys() ([]byte, []byte) {
	auth := make([]byte, 64)
	enc := make([]byte, 32)
	for i := range auth {
		auth[i] = byte(i)
	}
	for i := range enc {
		enc[i] = byte(i + 1)
	}
	return auth, enc
}

func TestServerStore_RoundTrip(t *testing.T) {
	auth, enc := testKeys()
	store := NewInMemoryStore(auth, enc)
	store.Options(gcsessions.Options{Path: "/", HttpOnly: true, MaxAge: 3600})

	// Создание сессии и запись.
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()
	sess, err := store.New(req, "gengine_session")
	require.NoError(t, err)
	sess.Values["user_id"] = uint(42)
	sess.Values["2fa_verified_42"] = int64(1234567890)
	require.NoError(t, store.Save(req, w, sess))

	// Чтение из cookie (новый запрос с тем же cookie).
	req2 := httptest.NewRequest("GET", "http://example.com/", nil)
	req2.AddCookie(w.Result().Cookies()[0])
	sess2, err := store.Get(req2, "gengine_session")
	require.NoError(t, err)
	require.False(t, sess2.IsNew, "session should be loaded from cookie")
	assert.Equal(t, uint(42), sess2.Values["user_id"])
	assert.Equal(t, int64(1234567890), sess2.Values["2fa_verified_42"])
	assert.Equal(t, sess.ID, sess2.ID, "same session ID across requests")
}

func TestServerStore_RenewToken_ChangesID(t *testing.T) {
	auth, enc := testKeys()
	store := NewInMemoryStore(auth, enc)

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()
	sess, err := store.New(req, "gengine_session")
	require.NoError(t, err)
	sess.Values["pending_user_id"] = uint(7)
	require.NoError(t, store.Save(req, w, sess))
	oldID := sess.ID

	// RenewToken — новый ID, те же данные.
	req2 := httptest.NewRequest("GET", "http://example.com/", nil)
	req2.AddCookie(w.Result().Cookies()[0])
	sess2, err := store.Get(req2, "gengine_session")
	require.NoError(t, err)
	renewed, err := store.RenewToken(req2, w, sess2)
	require.NoError(t, err)
	assert.NotEqual(t, oldID, renewed.ID, "RenewToken must change session ID")
	assert.Equal(t, uint(7), renewed.Values["pending_user_id"])

	// Сохраняем перевыпущенную сессию в ОТДЕЛЬНЫЙ Recorder (как новый ответ).
	w2 := httptest.NewRecorder()
	require.NoError(t, store.Save(req2, w2, renewed))
	// Напрямую проверяем backend: новая ID должна существовать.
	_, okNew, err := store.backend.Get(renewed.ID)
	require.NoError(t, err)
	assert.True(t, okNew, "renewed ID must exist in backend after Save")
	req3 := httptest.NewRequest("GET", "http://example.com/", nil)
	req3.AddCookie(w2.Result().Cookies()[0])
	sess3, err := store.Get(req3, "gengine_session")
	require.NoError(t, err)
	require.False(t, sess3.IsNew, "renewed session should be loadable")
	assert.Equal(t, renewed.ID, sess3.ID)

	// Старая ID в backend удалена.
	_, ok, err := store.backend.Get(oldID)
	require.NoError(t, err)
	assert.False(t, ok, "old session must be deleted from backend")
}

func TestServerStore_BadCookie_ReturnsNewSession(t *testing.T) {
	auth, enc := testKeys()
	store := NewInMemoryStore(auth, enc)

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	// Поддельная кука.
	req.AddCookie(&http.Cookie{Name: "gengine_session", Value: base64.StdEncoding.EncodeToString([]byte("forged"))})
	sess, err := store.Get(req, "gengine_session")
	require.NoError(t, err)
	assert.True(t, sess.IsNew, "forged cookie must yield a new session (not error)")
}

func TestServerStore_ValkeyBackend_RoundTrip(t *testing.T) {
	// miniredis — in-process Valkey/Redis (тест без внешнего сервера).
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	auth, enc := testKeys()
	store := NewValkeyStore(client, "gengine:session", auth, enc)
	store.Options(gcsessions.Options{Path: "/", HttpOnly: true, MaxAge: 3600})

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()
	sess, err := store.New(req, "gengine_session")
	require.NoError(t, err)
	sess.Values["pending_user_id"] = uint(7)
	require.NoError(t, store.Save(req, w, sess))
	require.Len(t, mr.Keys(), 1, "session must be stored in Valkey backend")

	req2 := httptest.NewRequest("GET", "http://example.com/", nil)
	req2.AddCookie(w.Result().Cookies()[0])
	sess2, err := store.Get(req2, "gengine_session")
	require.NoError(t, err)
	require.False(t, sess2.IsNew, "session should be loaded from Valkey backend")
	assert.Equal(t, uint(7), sess2.Values["pending_user_id"])
	assert.Equal(t, sess.ID, sess2.ID)
}

func TestTypedValue_RoundTrip(t *testing.T) {
	values := []any{
		uint(42), uint64(1 << 40), int(7), int64(1234567890),
		"hello", true, 3.14, time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
		[]byte{1, 2, 3}, nil,
	}
	for _, v := range values {
		tv := encodeTypedValue(v)
		got, err := tv.Decode()
		require.NoError(t, err)
		assert.Equal(t, v, got, "typed value must round-trip type and value")
	}
	// Неизвестный тип (map) — JSON-сериализация, decode даст map[string]any.
	tv := encodeTypedValue(map[string]any{"a": 1})
	got, err := tv.Decode()
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"a": float64(1)}, got)
}

func TestServerStore_ValkeyBackend_PreservesTypes(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	auth, enc := testKeys()
	store := NewValkeyStore(client, "gengine:session", auth, enc)

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()
	sess, err := store.New(req, "gengine_session")
	require.NoError(t, err)
	sess.Values["pending_user_id"] = uint(42)          // как в Login
	sess.Values["pending_expires"] = int64(1234567890) // как в Login
	sess.Values["pending_email"] = "user@example.com"
	sess.Values["2fa_verified_42"] = int64(999)
	require.NoError(t, store.Save(req, w, sess))

	req2 := httptest.NewRequest("GET", "http://example.com/", nil)
	req2.AddCookie(w.Result().Cookies()[0])
	sess2, err := store.Get(req2, "gengine_session")
	require.NoError(t, err)
	require.False(t, sess2.IsNew)
	assert.Equal(t, uint(42), sess2.Values["pending_user_id"])
	assert.Equal(t, int64(1234567890), sess2.Values["pending_expires"])
	assert.Equal(t, "user@example.com", sess2.Values["pending_email"])
	assert.Equal(t, int64(999), sess2.Values["2fa_verified_42"])
}

func TestServerStore_ValkeyBackend_RenewToken(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	auth, enc := testKeys()
	store := NewValkeyStore(client, "gengine:session", auth, enc)

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()
	sess, err := store.New(req, "gengine_session")
	require.NoError(t, err)
	sess.Values["2fa_verified_9"] = int64(12345)
	require.NoError(t, store.Save(req, w, sess))
	oldID := sess.ID
	require.Len(t, mr.Keys(), 1)

	req2 := httptest.NewRequest("GET", "http://example.com/", nil)
	req2.AddCookie(w.Result().Cookies()[0])
	sess2, err := store.Get(req2, "gengine_session")
	require.NoError(t, err)
	renewed, err := store.RenewToken(req2, w, sess2)
	require.NoError(t, err)
	assert.NotEqual(t, oldID, renewed.ID, "RenewToken must change session ID")
	assert.Equal(t, int64(12345), renewed.Values["2fa_verified_9"])

	w2 := httptest.NewRecorder()
	require.NoError(t, store.Save(req2, w2, renewed))
	// Старая удалена, новая существует.
	_, okOld, err := store.backend.Get(oldID)
	require.NoError(t, err)
	assert.False(t, okOld, "old Valkey session must be deleted")
	_, okNew, err := store.backend.Get(renewed.ID)
	require.NoError(t, err)
	assert.True(t, okNew, "renewed Valkey session must exist")
}
