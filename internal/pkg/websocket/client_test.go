// internal/pkg/websocket/client_test.go
package websocket

import (
	"testing"
)

func TestClient_WritePump_SendMessage(t *testing.T) {
	t.Skip("writePump requires real websocket.Conn, use integration tests")
}

func TestClient_WritePump_CloseOnSendChannelClose(t *testing.T) {
	t.Skip("requires real websocket.Conn")
}

func TestClient_WritePump_Ping(t *testing.T) {
	t.Skip("requires real websocket.Conn")
}
