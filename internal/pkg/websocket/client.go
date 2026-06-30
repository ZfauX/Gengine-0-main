// internal/pkg/websocket/client.go
package websocket

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

type Client struct {
	ID       string
	Conn     *websocket.Conn
	Send     chan []byte
	RoomID   string
	RemoteIP string // IP-адрес клиента для лимитов
	Hub      *RoomHub
	mu       sync.Mutex
	closed   bool
	done     chan struct{} // сигнал о завершении всех горутин
}

func NewClient(conn *websocket.Conn, roomID, remoteIP string) *Client {
	return &Client{
		ID:       uuid.New().String(),
		Conn:     conn,
		Send:     make(chan []byte, 256),
		RoomID:   roomID,
		RemoteIP: remoteIP,
		done:     make(chan struct{}),
	}
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.Send)
		if c.done != nil {
			close(c.done)
		}
	}
}

func (c *Client) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// writePump отправляет сообщения из канала Send в WebSocket-соединение.
// Принимает контекст для graceful shutdown.
func (c *Client) writePump(ctx context.Context) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.Conn.Close()
		if c.Hub != nil {
			c.Hub.UnregisterClient(c)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			log.Debug().Str("client_id", c.ID).Msg("writePump: context cancelled, stopping")
			return
		case <-c.done:
			log.Debug().Str("client_id", c.ID).Msg("writePump: client closed")
			return
		case message, ok := <-c.Send:
			if !ok {
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Error().Err(err).Str("client_id", c.ID).Msg("writePump: write error")
				c.Close()
				return
			}
		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Error().Err(err).Str("client_id", c.ID).Msg("writePump: ping error")
				c.Close()
				return
			}
		}
	}
}

// HandleWebSocket запускает writePump и начинает чтение сообщений.
// Устаревший метод, используйте HandleWebSocketWithContext.
func HandleWebSocket(client *Client) {
	ctx := context.Background()
	HandleWebSocketWithContext(ctx, client)
}

// HandleWebSocketWithContext запускает writePump с контекстом и читает сообщения.
func HandleWebSocketWithContext(ctx context.Context, client *Client) {
	go client.writePump(ctx)

	_ = client.Conn.SetReadDeadline(time.Now().Add(pongWait))
	client.Conn.SetPongHandler(func(string) error {
		_ = client.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Цикл чтения с поддержкой отмены
	for {
		select {
		case <-ctx.Done():
			log.Debug().Str("client_id", client.ID).Msg("HandleWebSocketWithContext: context cancelled, stopping read loop")
			return
		case <-client.done:
			log.Debug().Str("client_id", client.ID).Msg("HandleWebSocketWithContext: client closed")
			return
		default:
			_, _, err := client.Conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Error().Err(err).Str("client_id", client.ID).Msg("HandleWebSocketWithContext: read error")
				}
				return
			}
		}
	}
}

// WritePump запускает writePump без контекста (устаревший метод).
func WritePump(client *Client) {
	ctx := context.Background()
	WritePumpWithContext(ctx, client)
}

// WritePumpWithContext запускает writePump с поддержкой контекста.
func WritePumpWithContext(ctx context.Context, client *Client) {
	go client.writePump(ctx)
}
