package websocket

import (
	"encoding/json"
	"sync"

	"github.com/rs/zerolog/log"
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
}

func (h *RoomHub) BroadcastToRoom(roomID string, msg any) {
	h.mu.RLock()
	clients, ok := h.rooms[roomID]
	snapshot := make([]*Client, 0, len(clients))
	for client := range clients {
		snapshot = append(snapshot, client)
	}
	h.mu.RUnlock()
	if !ok {
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("broadcast marshal error")
		return
	}
	for _, client := range snapshot {
		client.mu.Lock()
		if client.closed {
			client.mu.Unlock()
			continue
		}
		client.mu.Unlock()
		func() {
			defer func() { _ = recover() }()
			select {
			case client.Send <- data:
			default:
				client.Close()
			}
		}()
	}
}