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
	done       chan struct{} // сигнал остановки хаба
	wg         sync.WaitGroup
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
		done:       make(chan struct{}),
	}
}

// Run запускает основной цикл обработки событий хаба.
// Блокирует выполнение, пока не будет вызван Stop() или каналы не закроются.
func (h *RoomHub) Run() {
	h.wg.Add(1)
	defer h.wg.Done()

	for {
		select {
		case <-h.done:
			log.Info().Msg("RoomHub: stopping")
			return
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

// Stop останавливает хаб, дожидаясь завершения всех операций.
func (h *RoomHub) Stop() {
	close(h.done)
	h.wg.Wait()
	log.Info().Msg("RoomHub: stopped")
}

// RegisterClient регистрирует клиента в хабе.
func (h *RoomHub) RegisterClient(client *Client) {
	select {
	case h.register <- client:
	case <-h.done:
		log.Warn().Msg("RoomHub: register failed, hub is stopped")
	}
}

// UnregisterClient удаляет клиента из хаба.
func (h *RoomHub) UnregisterClient(client *Client) {
	select {
	case h.unregister <- client:
	case <-h.done:
		log.Warn().Msg("RoomHub: unregister failed, hub is stopped")
	}
}

// BroadcastToRoom отправляет сообщение всем клиентам в комнате.
func (h *RoomHub) BroadcastToRoom(roomID string, data []byte) {
	select {
	case h.broadcast <- &Message{Room: roomID, Data: data}:
	case <-h.done:
		log.Warn().Str("room", roomID).Msg("RoomHub: broadcast failed, hub is stopped")
	}
}
