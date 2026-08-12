// internal/pkg/websocket/room_hub.go
package websocket

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const hubChanCapacity = 128

// RoomHub управляет WebSocket-комнатами и клиентами.
type RoomHub struct {
	rooms      map[string]map[*Client]bool
	mu         sync.RWMutex
	register   chan *Client
	unregister chan *Client
	broadcast  chan *Message
	done       chan struct{}
	wg         sync.WaitGroup
	stopped    bool

	// roomClients (P-M2, PASS-8): кэш слайса клиентов комнаты для рассылки.
	// dispatchToRoom больше не аллоцирует слайс на КАЖДОЕ сообщение чата.
	// Инвалидируется (delete) при любой мутации комнаты (register/unregister/
	// удаление закрытого клиента/удаление пустой комнаты).
	roomClients map[string][]*Client

	// P-7 (pass 39): per-room очереди и воркеры — рассылка в одной комнате
	// не блокирует рассылку в других (раньше runLoop сериализовал все
	// broadcast'ы одной горутиной; на больших комнатах это тормозило
	// регистрацию новых соединений и другие комнаты).
	roomQueues    map[string]chan *Message
	roomWorkersWg sync.WaitGroup

	// Лимиты и счётчики
	maxTotalConns int
	maxConnsPerIP int
	totalConns    int
	connsPerIP    map[string]int

	// onRoomChange (IDEA-6): вызывается в runLoop после добавления/удаления
	// клиента из комнаты. Используется для presence-онлайн-индикатора в чате.
	// Вызов синхронный из runLoop — колбэк должен быть быстрым (без блокировок).
	onRoomChange func(roomID string)
}

// SetOnRoomChange регистрирует колбэк изменения состава комнаты (IDEA-6).
// Вызывается из runLoop после мутации rooms — безопасно читать RoomClientCount.
func (h *RoomHub) SetOnRoomChange(cb func(roomID string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onRoomChange = cb
}

// RoomClientCount возвращает число активных клиентов в комнате (IDEA-6).
func (h *RoomHub) RoomClientCount(roomID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[roomID])
}

// RoomUserIDs возвращает уникальные userID активных клиентов комнаты (IDEA-6).
// Может содержать 0 (клиент без userID). Порядок не гарантирован.
func (h *RoomHub) RoomUserIDs(roomID string) []uint {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := make(map[uint]bool, len(h.rooms[roomID]))
	ids := make([]uint, 0, len(h.rooms[roomID]))
	for client := range h.rooms[roomID] {
		if client.UserID == 0 || seen[client.UserID] {
			continue
		}
		seen[client.UserID] = true
		ids = append(ids, client.UserID)
	}
	return ids
}

// NewRoomHub создаёт новый хаб с лимитами по умолчанию.
func NewRoomHub() *RoomHub {
	return &RoomHub{
		rooms:         make(map[string]map[*Client]bool),
		roomClients:   make(map[string][]*Client),
		register:      make(chan *Client), // unbuffered — синхронная регистрация
		unregister:    make(chan *Client, hubChanCapacity),
		broadcast:     make(chan *Message, hubChanCapacity),
		done:          make(chan struct{}),
		roomQueues:    make(map[string]chan *Message),
		maxTotalConns: 1000,
		maxConnsPerIP: 50,
		connsPerIP:    make(map[string]int),
	}
}

// SetLimits устанавливает новые лимиты.
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

// CanAccept проверяет, можно ли принять новое соединение.
func (h *RoomHub) CanAccept(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
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

// Acquire атомарно проверяет лимиты и инкрементирует счётчики под одним lock
// (DEEP-REVIEW, pass 46): раньше CanAccept и incConnection были раздельными —
// два конкурентных handshake могли оба пройти CanAccept и превысить лимиты.
// Возвращает false, если лимит превышен или хаб остановлен.
func (h *RoomHub) Acquire(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
	if h.maxTotalConns > 0 && h.totalConns >= h.maxTotalConns {
		log.Warn().Int("total", h.totalConns).Int("limit", h.maxTotalConns).Msg("WebSocket: total connections limit reached")
		return false
	}
	if h.maxConnsPerIP > 0 && h.connsPerIP[ip] >= h.maxConnsPerIP {
		log.Warn().Str("ip", ip).Int("count", h.connsPerIP[ip]).Int("limit", h.maxConnsPerIP).Msg("WebSocket: per-IP limit reached")
		return false
	}
	h.totalConns++
	h.connsPerIP[ip]++
	return true
}

// decConnectionNoLock уменьшает счётчики при отписке клиента.
// Вызывается из cleanupInactiveClients и unregister (которые уже держат h.mu.Lock()).
func (h *RoomHub) decConnectionNoLock(ip string) {
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

// GetStats возвращает текущую статистику соединений.
func (h *RoomHub) GetStats() (total int, perIP map[string]int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	perIPCopy := make(map[string]int, len(h.connsPerIP))
	for k, v := range h.connsPerIP {
		perIPCopy[k] = v
	}
	return h.totalConns, perIPCopy
}

// Run запускает основной цикл обработки событий хаба в фоновой горутине.
func (h *RoomHub) Run() {
	h.wg.Add(1)
	go h.runLoop()
}

func (h *RoomHub) runLoop() {
	defer h.wg.Done()

	for {
		select {
		case <-h.done:
			log.Info().Msg("RoomHub: stopping")
			return
		case client := <-h.register:
			if h.isStopped() {
				log.Warn().Msg("RoomHub: registration rejected, hub is stopping")
				client.Close()
				continue
			}
			// DEEP-REVIEW (pass 46): атомарная проверка+инкремент лимитов —
			// вместо раздельных CanAccept+incConnection (TOCTOU).
			if !h.Acquire(client.RemoteIP) {
				log.Warn().Str("ip", client.RemoteIP).Msg("RoomHub: connection rejected (limit reached)")
				client.Close()
				continue
			}
			h.mu.Lock()
			client.setHub(h)
			client.registered = true
			if _, ok := h.rooms[client.RoomID]; !ok {
				h.rooms[client.RoomID] = make(map[*Client]bool)
			}
			h.rooms[client.RoomID][client] = true
			// P-M2 (PASS-8): состав комнаты изменился — сбрасываем кэш слайса.
			delete(h.roomClients, client.RoomID)
			cb := h.onRoomChange
			h.mu.Unlock()
			log.Debug().Str("room", client.RoomID).Str("ip", client.RemoteIP).Msg("WebSocket client registered")
			// IDEA-6: presence-уведомление после добавления клиента (вне лока).
			if cb != nil {
				cb(client.RoomID)
			}

		case client := <-h.unregister:
			h.mu.Lock()
			// P1: handler-defer и writePump оба вызывают UnregisterClient —
			// обрабатываем только первую отписку.
			if !client.registered {
				h.mu.Unlock()
				continue
			}
			client.registered = false
			h.decConnectionNoLock(client.RemoteIP)
			if room, ok := h.rooms[client.RoomID]; ok {
				delete(room, client)
				// P-M2 (PASS-8): состав изменился — сбрасываем кэш слайса.
				delete(h.roomClients, client.RoomID)
				if len(room) == 0 {
					delete(h.rooms, client.RoomID)
					// C-1 (pass 40): очередь НЕ закрываем (иначе broadcast может
					// сделать send-on-closed → panic); воркер завершится по
					// idle-таймеру, когда увидит, что комнаты нет.
					delete(h.roomQueues, client.RoomID)
					log.Debug().Str("room", client.RoomID).Msg("Room removed (empty)")
				}
			}
			cb := h.onRoomChange
			h.mu.Unlock()
			log.Debug().Str("room", client.RoomID).Str("ip", client.RemoteIP).Msg("WebSocket client unregistered")
			// IDEA-6: presence-уведомление после удаления клиента (вне лока).
			if cb != nil {
				cb(client.RoomID)
			}

		case msg := <-h.broadcast:
			if h.isStopped() {
				log.Warn().Str("room", msg.Room).Msg("RoomHub: broadcast skipped, hub is stopping")
				continue
			}
			// P-7 (pass 39): диспатчим в per-room очередь — рассылка идёт в
			// воркере комнаты, не блокируя другие комнаты.
			h.mu.RLock()
			_, roomExists := h.rooms[msg.Room]
			queue := h.roomQueues[msg.Room]
			h.mu.RUnlock()
			if !roomExists {
				continue
			}
			if queue == nil {
				// Первый broadcast в комнату — создаём очередь и воркер.
				h.mu.Lock()
				queue = h.roomQueues[msg.Room]
				if queue == nil {
					queue = make(chan *Message, hubChanCapacity)
					h.roomQueues[msg.Room] = queue
					h.roomWorkersWg.Add(1)
					go h.roomWorker(msg.Room, queue)
				}
				h.mu.Unlock()
			}
			// Неблокирующая отправка в очередь комнаты (drop-on-full).
			select {
			case queue <- msg:
			default:
				log.Debug().Str("room", msg.Room).Msg("RoomHub: room queue full, dropping message")
			}
		}
	}
}

// roomWorker рассылает сообщения очереди комнаты (P-7, pass 39).
// C-1 (pass 40): каналы очередей НИКОГДА не закрываются (иначе broadcast
// может сделать send-on-closed → panic). Воркер завершается по h.done
// (при Stop) или по idle-таймеру, если комната удалена (нет утечки).
func (h *RoomHub) roomWorker(roomID string, queue chan *Message) {
	defer h.roomWorkersWg.Done()

	// L3 (PASS-8): между созданием очереди (в runLoop под Lock) и стартом
	// воркера комната могла быть удалена (гонка broadcast/удаление). Раньше
	// воркер жил до idle-таймера (30с) впустую — выходим сразу.
	h.mu.RLock()
	_, exists := h.rooms[roomID]
	h.mu.RUnlock()
	if !exists {
		log.Debug().Str("room", roomID).Msg("RoomHub: room worker exiting (room removed before start)")
		return
	}

	idleTicker := time.NewTicker(30 * time.Second)
	defer idleTicker.Stop()

	for {
		select {
		case <-h.done:
			return
		case msg, ok := <-queue:
			if !ok {
				return
			}
			h.dispatchToRoom(roomID, msg.Data)
		case <-idleTicker.C:
			// Комната могла быть удалена или очередь пересоздана (новый
			// register после удаления) — воркер старой очереди должен выйти.
			h.mu.RLock()
			_, exists := h.rooms[roomID]
			currentQueue := h.roomQueues[roomID]
			h.mu.RUnlock()
			if !exists || currentQueue != queue {
				log.Debug().Str("room", roomID).Msg("RoomHub: room worker exiting (room removed or queue replaced)")
				return
			}
		}
	}
}

// dispatchToRoom копирует клиентов комнаты (из кэша слайса, P-M2) и рассылает
// сообщение без удержания лока. Слайс кэшируется при register/unregister и
// пересобирается только после удаления закрытых клиентов.
func (h *RoomHub) dispatchToRoom(roomID string, data []byte) {
	h.mu.RLock()
	room, ok := h.rooms[roomID]
	if !ok {
		h.mu.RUnlock()
		return
	}
	clients := h.roomClients[roomID]
	if clients == nil {
		// Кэша нет (первая рассылка после мутации) — собираем слайс.
		clients = make([]*Client, 0, len(room))
		for client := range room {
			clients = append(clients, client)
		}
		h.roomClients[roomID] = clients
	}
	// Копируем заголовок слайса, чтобы итерация шла по снимку; элементы
	// слайса не мутируются, поэтому безопасно читать без лока после RUnlock.
	snapshot := clients[:len(clients):len(clients)]
	h.mu.RUnlock()

	removed := false
	// Рассылка БЕЗ удержания лока
	for _, client := range snapshot {
		if client.IsClosed() {
			// Удаляем из оригинальной map под локом
			h.mu.Lock()
			if h.rooms[roomID] == nil {
				h.mu.Unlock()
				continue
			}
			delete(h.rooms[roomID], client)
			removed = true
			h.mu.Unlock()
			continue
		}
		select {
		case client.Send <- data:
		case <-client.Done():
			h.mu.Lock()
			if h.rooms[roomID] == nil {
				h.mu.Unlock()
				continue
			}
			delete(h.rooms[roomID], client)
			removed = true
			h.mu.Unlock()
		default:
			log.Debug().Str("room", roomID).Msg("broadcast: client buffer full, dropping message")
		}
	}
	h.mu.Lock()
	if removed {
		// Кэш слайса стал stale (удалили закрытых клиентов) — сбрасываем.
		delete(h.roomClients, roomID)
	}
	if current, exists := h.rooms[roomID]; exists && len(current) == 0 {
		delete(h.rooms, roomID)
		delete(h.roomClients, roomID)
		// C-1 (pass 40): очередь НЕ закрываем (send-on-closed panic);
		// воркер завершится по idle-таймеру.
		delete(h.roomQueues, roomID)
		log.Debug().Str("room", roomID).Msg("Room removed (empty)")
	}
	h.mu.Unlock()
}

// isStopped проверяет, остановлен ли хаб.
func (h *RoomHub) isStopped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopped
}

// IsStopped проверяет, остановлен ли хаб (публичный метод для health check).
func (h *RoomHub) IsStopped() bool {
	return h.isStopped()
}

// Stop останавливает хаб и закрывает все соединения, отправив CloseMessage.
func (h *RoomHub) Stop() {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	// Закрываем done под тем же локом, чтобы никакой новый register
	// не мог пройти между установкой stopped и закрытием done
	close(h.done)
	// C-1 (pass 40): очереди НЕ закрываем (send-on-closed panic); воркеры
	// завершатся по select h.done. Опустошаем map.
	h.roomQueues = make(map[string]chan *Message)
	h.mu.Unlock()
	h.wg.Wait()
	// P-7 (pass 39): ждём завершения per-room воркеров.
	h.roomWorkersWg.Wait()

	// Теперь безопасно закрываем все соединения (Run() уже не рассылает)
	h.mu.Lock()
	for roomID, room := range h.rooms {
		for client := range room {
			client.Close()
			delete(room, client)
		}
		delete(h.rooms, roomID)
	}
	h.mu.Unlock()

	log.Info().Msg("RoomHub: stopped")
}

// RegisterClient регистрирует клиента в хабе.
func (h *RoomHub) RegisterClient(client *Client) {
	select {
	case h.register <- client:
	case <-h.done:
		log.Warn().Msg("RoomHub: register failed, hub is stopped")
		client.Close()
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
// Неблокирующая: при переполнении буфера сообщение отбрасывается, чтобы не
// задерживать обработчики HTTP/WS после коммита транзакции.
func (h *RoomHub) BroadcastToRoom(roomID string, data []byte) {
	select {
	case h.broadcast <- &Message{Room: roomID, Data: data}:
	case <-h.done:
		log.Warn().Str("room", roomID).Msg("RoomHub: broadcast failed, hub is stopped")
	default:
		log.Warn().Str("room", roomID).Msg("RoomHub: broadcast buffer full, dropping message")
	}
}

type Message struct {
	Room string
	Data []byte
}
