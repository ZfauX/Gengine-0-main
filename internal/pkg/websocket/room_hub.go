// internal/pkg/websocket/room_hub.go
package websocket

import (
	"encoding/json"
	"log"
	"sync"
)

type RoomHub struct {
	rooms map[string]map[*Client]bool
	mu    sync.RWMutex
}

func NewRoomHub() *RoomHub {
	return &RoomHub{
		rooms: make(map[string]map[*Client]bool),
	}
}

func (h *RoomHub) Run() {}

func (h *RoomHub) RegisterClient(roomID string, client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[roomID] == nil {
		h.rooms[roomID] = make(map[*Client]bool)
	}
	h.rooms[roomID][client] = true
}

func (h *RoomHub) UnregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if clients, ok := h.rooms[client.RoomID]; ok {
		delete(clients, client)
	}
	client.Close()
}

func (h *RoomHub) BroadcastToRoom(roomID string, msg interface{}) {
	h.mu.RLock()
	clients, ok := h.rooms[roomID]
	h.mu.RUnlock()
	if !ok {
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Println("broadcast marshal error:", err)
		return
	}
	for client := range clients {
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
		}
	}
}