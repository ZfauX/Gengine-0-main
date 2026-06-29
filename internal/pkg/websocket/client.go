// internal/pkg/websocket/client.go
package websocket

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

// Client представляет WebSocket-клиента с уникальным идентификатором.
type Client struct {
	ID     string          // уникальный идентификатор клиента (генерируется при создании)
	Conn   *websocket.Conn // WebSocket-соединение
	Send   chan []byte     // канал для отправки сообщений
	RoomID string          // ID комнаты, в которой состоит клиент
	mu     sync.Mutex      // защита поля closed
	closed bool            // флаг закрытия
}

// NewClient создаёт нового клиента с уникальным ID и каналом Send.
func NewClient(conn *websocket.Conn, roomID string) *Client {
	return &Client{
		ID:     uuid.New().String(),
		Conn:   conn,
		Send:   make(chan []byte, 256),
		RoomID: roomID,
	}
}

// Close безопасно закрывает клиента.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.Send)
	}
}

// HandleWebSocket запускает цикл чтения и writePump в горутине.
func HandleWebSocket(client *Client) {
	go client.writePump()
	_ = client.Conn.SetReadDeadline(time.Now().Add(pongWait))
	client.Conn.SetPongHandler(func(string) error {
		_ = client.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, _, err := client.Conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// WritePump запускает pump записи в горутине от имени клиента.
func WritePump(client *Client) {
	go client.writePump()
}

// writePump обрабатывает отправку сообщений и пинги.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.Conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.Send:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.Close()
				return
			}
		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.Close()
				return
			}
		}
	}
}
