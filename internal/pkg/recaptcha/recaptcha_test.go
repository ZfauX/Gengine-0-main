// internal/pkg/recaptcha/recaptcha_test.go
package recaptcha

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer создаёт фейк Google siteverify.
func newTestServer(t *testing.T, status int, body string) (*httptest.Server, func() string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/siteverify" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	// Меняем verifyURL глобально (пакетный тест, без параллельности).
	origURL := verifyURL
	verifyURL = srv.URL + "/api/siteverify"
	t.Cleanup(func() {
		srv.Close()
		verifyURL = origURL
	})
	return srv, func() string { return verifyURL }
}

func TestClient_Disabled_AlwaysPasses(t *testing.T) {
	c := NewClient(false, "secret")
	if c.Enabled() {
		t.Fatal("disabled client should not be enabled")
	}
	if err := c.Verify(context.Background(), ""); err != nil {
		t.Fatalf("disabled client should pass empty token, got %v", err)
	}
}

func TestClient_Enabled_EmptyToken(t *testing.T) {
	newTestServer(t, http.StatusOK, `{"success":true}`)
	c := NewClient(true, "secret")
	if !c.Enabled() {
		t.Fatal("enabled client should be enabled")
	}
	if err := c.Verify(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestClient_Verify_Success(t *testing.T) {
	newTestServer(t, http.StatusOK, `{"success":true}`)
	c := NewClient(true, "secret")
	if err := c.Verify(context.Background(), "token"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestClient_Verify_Failure(t *testing.T) {
	newTestServer(t, http.StatusOK, `{"success":false,"error-codes":["invalid-input-response"]}`)
	c := NewClient(true, "secret")
	err := c.Verify(context.Background(), "token")
	if err == nil {
		t.Fatal("expected error for success=false")
	}
	if !strings.Contains(err.Error(), "invalid-input-response") {
		t.Fatalf("expected error-codes in message, got %v", err)
	}
}

func TestClient_Verify_Non200(t *testing.T) {
	newTestServer(t, http.StatusBadGateway, `{"success":false}`)
	c := NewClient(true, "secret")
	if err := c.Verify(context.Background(), "token"); err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestClient_Verify_BadJSON(t *testing.T) {
	newTestServer(t, http.StatusOK, `not-json`)
	c := NewClient(true, "secret")
	if err := c.Verify(context.Background(), "token"); err == nil {
		t.Fatal("expected error for bad JSON")
	}
}

func TestClient_Verify_NetworkError(t *testing.T) {
	// Закрытый сервер — соединение упадёт.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	verifyURL = srv.URL + "/api/siteverify"
	srv.Close()
	defer func() { verifyURL = "https://www.google.com/recaptcha/api/siteverify" }()

	c := NewClient(true, "secret")
	if err := c.Verify(context.Background(), "token"); err == nil {
		t.Fatal("expected error for network failure")
	}
}
