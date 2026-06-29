// internal/pkg/websocket/room_hub.go
package websocket

import (
	"encoding/json"
	"sync"
	"time"

	"gengine-0/internal/pkg/metrics"

	"github.com/rs/zerolog/log"
)

type RoomHub struct {
	rooms     map[string]map[*Client]bool
	mu        sync.RWMutex
	stopClean chan struct{}
	cleanOnce sync.Once
}

func NewRoomHub() *RoomHub {
	h := &RoomHub{
		rooms:     make(map[string]map[*Client]bool),
		stopClean: make(chan struct{}),
	}
	go h.cleanupLoop()
	return h
}

func (h *RoomHub) Run() {}

// RegisterClient регистрирует клиента в комнате и увеличивает счётчик метрик.
func (h *RoomHub) RegisterClient(roomID string, client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[roomID] == nil {
		h.rooms[roomID] = make(map[*Client]bool)
	}
	h.rooms[roomID][client] = true
	metrics.IncWebSocketConnection()
	log.Debug().Str("room", roomID).Str("client", client.ID).Msg("WebSocket client registered")
}

// UnregisterClient удаляет клиента из комнаты и уменьшает счётчик метрик.
// Комната остаётся даже если пуста (это упрощает логику и тесты).
func (h *RoomHub) UnregisterClient(client *Client) {
	if client == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if clients, ok := h.rooms[client.RoomID]; ok {
		if _, exists := clients[client]; exists {
			delete(clients, client)
			metrics.DecWebSocketConnection()
			log.Debug().Str("room", client.RoomID).Str("client", client.ID).Msg("WebSocket client unregistered")
		}
	}
	// Комнату не удаляем, даже если она пуста
}

// BroadcastToRoom отправляет сообщение всем клиентам в комнате с защитой от блокировок.
func (h *RoomHub) BroadcastToRoom(roomID string, msg any) {
	h.mu.RLock()
	clients, ok := h.rooms[roomID]
	if !ok || len(clients) == 0 {
		h.mu.RUnlock()
		return
	}
	snapshot := make([]*Client, 0, len(clients))
	for client := range clients {
		snapshot = append(snapshot, client)
	}
	h.mu.RUnlock()

	data, err := json.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Str("room", roomID).Msg("broadcast marshal error")
		return
	}

	for _, client := range snapshot {
		client.mu.Lock()
		if client.closed {
			client.mu.Unlock()
			continue
		}
		client.mu.Unlock()

		select {
		case client.Send <- data:
		default:
			client.Close()
			go h.UnregisterClient(client)
		}
	}
}

// cleanupLoop периодически удаляет клиентов, у которых закрыт канал Send,
// чтобы избежать утечек памяти.
func (h *RoomHub) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopClean:
			return
		case <-ticker.C:
			h.cleanStaleClients()
		}
	}
}

// cleanStaleClients удаляет клиентов, у которых канал Send закрыт или соединение разорвано.
func (h *RoomHub) cleanStaleClients() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for roomID, clients := range h.rooms {
		for client := range clients {
			client.mu.Lock()
			isClosed := client.closed
			client.mu.Unlock()

			if isClosed {
				delete(clients, client)
				metrics.DecWebSocketConnection()
				log.Debug().Str("room", roomID).Str("client", client.ID).Msg("stale client cleaned up")
			}
		}
		// Оставляем пустую комнату
	}
}

// Stop останавливает фоновую очистку (для graceful shutdown).
func (h *RoomHub) Stop() {
	h.cleanOnce.Do(func() {
		close(h.stopClean)
	})
}
