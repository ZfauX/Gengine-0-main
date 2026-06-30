// internal/pkg/websocket/room_hub.go
package websocket

import (
	"sync"

	"github.com/rs/zerolog/log"
)

// RoomHub управляет WebSocket-комнатами и клиентами.
// Также отслеживает общее количество соединений и лимиты на IP.
type RoomHub struct {
	rooms      map[string]map[*Client]bool
	mu         sync.Mutex
	register   chan *Client
	unregister chan *Client
	broadcast  chan *Message
	done       chan struct{}
	wg         sync.WaitGroup

	// Лимиты и счётчики
	maxTotalConns int
	maxConnsPerIP int
	totalConns    int
	connsPerIP    map[string]int
}

// NewRoomHub создаёт новый хаб с лимитами по умолчанию.
// maxTotalConns — общее максимальное количество соединений (0 = без лимита).
// maxConnsPerIP — максимальное соединений с одного IP (0 = без лимита).
func NewRoomHub() *RoomHub {
	return &RoomHub{
		rooms:         make(map[string]map[*Client]bool),
		register:      make(chan *Client),
		unregister:    make(chan *Client),
		broadcast:     make(chan *Message),
		done:          make(chan struct{}),
		maxTotalConns: 1000, // разумный лимит по умолчанию
		maxConnsPerIP: 50,   // защита от DoS с одного IP
		connsPerIP:    make(map[string]int),
	}
}

// SetLimits устанавливает новые лимиты (можно вызвать до запуска хаба).
func (h *RoomHub) SetLimits(maxTotalConns, maxConnsPerIP int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if maxTotalConns > 0 {
		h.maxTotalConns = maxTotalConns
	}
	if maxConnsPerIP > 0 {
		h.maxConnsPerIP = maxConnsPerIP
	}
}

// CanAccept проверяет, можно ли принять новое соединение с указанного IP.
// Возвращает true, если лимиты не превышены.
func (h *RoomHub) CanAccept(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.maxTotalConns > 0 && h.totalConns >= h.maxTotalConns {
		log.Warn().Int("total", h.totalConns).Int("limit", h.maxTotalConns).Msg("WebSocket: total connections limit reached")
		return false
	}
	if h.maxConnsPerIP > 0 && h.connsPerIP[ip] >= h.maxConnsPerIP {
		log.Warn().Str("ip", ip).Int("count", h.connsPerIP[ip]).Int("limit", h.maxConnsPerIP).Msg("WebSocket: per-IP limit reached")
		return false
	}
	return true
}

// incConnection увеличивает счётчики при регистрации клиента.
func (h *RoomHub) incConnection(ip string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.totalConns++
	h.connsPerIP[ip]++
}

// decConnection уменьшает счётчики при отписке клиента.
func (h *RoomHub) decConnection(ip string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.totalConns > 0 {
		h.totalConns--
	}
	if count, ok := h.connsPerIP[ip]; ok && count > 0 {
		if count == 1 {
			delete(h.connsPerIP, ip)
		} else {
			h.connsPerIP[ip] = count - 1
		}
	}
}

// GetStats возвращает текущую статистику соединений (для метрик).
func (h *RoomHub) GetStats() (total int, perIP map[string]int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	perIPCopy := make(map[string]int, len(h.connsPerIP))
	for k, v := range h.connsPerIP {
		perIPCopy[k] = v
	}
	return h.totalConns, perIPCopy
}

// Run запускает основной цикл обработки событий хаба.
func (h *RoomHub) Run() {
	h.wg.Add(1)
	defer h.wg.Done()

	for {
		select {
		case <-h.done:
			log.Info().Msg("RoomHub: stopping")
			return
		case client := <-h.register:
			// Увеличиваем счётчики перед регистрацией (но проверка уже выполнена в CanAccept)
			h.incConnection(client.RemoteIP)
			h.mu.Lock()
			client.Hub = h
			if _, ok := h.rooms[client.RoomID]; !ok {
				h.rooms[client.RoomID] = make(map[*Client]bool)
			}
			h.rooms[client.RoomID][client] = true
			h.mu.Unlock()
			log.Debug().Str("room", client.RoomID).Str("ip", client.RemoteIP).Msg("WebSocket client registered")

		case client := <-h.unregister:
			// Уменьшаем счётчики при отписке
			h.decConnection(client.RemoteIP)
			h.mu.Lock()
			if room, ok := h.rooms[client.RoomID]; ok {
				delete(room, client)
				if len(room) == 0 {
					delete(h.rooms, client.RoomID)
					log.Debug().Str("room", client.RoomID).Msg("Room removed (empty)")
				}
			}
			h.mu.Unlock()
			log.Debug().Str("room", client.RoomID).Str("ip", client.RemoteIP).Msg("WebSocket client unregistered")

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

// RegisterClient регистрирует клиента в хабе (предварительно вызвав CanAccept).
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

type Message struct {
	Room string
	Data []byte
}
