// internal/pkg/websocket/room_hub.go
package websocket

import (
	"sync"

	"github.com/rs/zerolog/log"
)

type RoomHub struct {
	rooms      map[string]map[*Client]bool
	mu         sync.Mutex
	register   chan *Client
	unregister chan *Client
	broadcast  chan *Message
}

type Message struct {
	Room string
	Data []byte
}

func NewRoomHub() *RoomHub {
	return &RoomHub{
		rooms:      make(map[string]map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan *Message),
	}
}

func (h *RoomHub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			client.Hub = h
			if _, ok := h.rooms[client.RoomID]; !ok {
				h.rooms[client.RoomID] = make(map[*Client]bool)
			}
			h.rooms[client.RoomID][client] = true
			h.mu.Unlock()
			log.Debug().Str("room", client.RoomID).Msg("WebSocket client registered")

		case client := <-h.unregister:
			h.mu.Lock()
			if room, ok := h.rooms[client.RoomID]; ok {
				delete(room, client)
				if len(room) == 0 {
					delete(h.rooms, client.RoomID)
					log.Debug().Str("room", client.RoomID).Msg("Room removed (empty)")
				}
			}
			h.mu.Unlock()
			log.Debug().Str("room", client.RoomID).Msg("WebSocket client unregistered")

		case msg := <-h.broadcast:
			h.mu.Lock()
			room, ok := h.rooms[msg.Room]
			if !ok {
				h.mu.Unlock()
				continue
			}
			for client := range room {
				if client.IsClosed() {
					delete(room, client)
					continue
				}
				select {
				case client.Send <- msg.Data:
				default:
					// канал полон – закрываем клиента и удаляем
					client.Close()
					delete(room, client)
				}
			}
			if len(room) == 0 {
				delete(h.rooms, msg.Room)
				log.Debug().Str("room", msg.Room).Msg("Room removed (empty)")
			}
			h.mu.Unlock()
		}
	}
}

func (h *RoomHub) RegisterClient(client *Client) {
	h.register <- client
}

func (h *RoomHub) UnregisterClient(client *Client) {
	h.unregister <- client
}

func (h *RoomHub) BroadcastToRoom(roomID string, data []byte) {
	h.broadcast <- &Message{Room: roomID, Data: data}
}
