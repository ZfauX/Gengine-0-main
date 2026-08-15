// internal/pkg/websocket/cleanup.go
package websocket

import (
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// maxInactiveDuration — максимальное время бездействия клиента (2 * pongWait)
	maxInactiveDuration = pongWait * 2
	// cleanupInterval — интервал проверки неактивных клиентов
	cleanupInterval = 30 * time.Second
)

// cleanupResult хранит результаты очистки
type cleanupResult struct {
	removedClients int
	removedRooms   int
	elapsed        time.Duration
}

// StartCleanupPeriodic запускает периодическую проверку неактивных клиентов.
// Вызывается один раз при старте приложения.
func (h *RoomHub) StartCleanupPeriodic() {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		log.Info().Dur("interval", cleanupInterval).Msg("WebSocket: cleanup periodic started")

		for {
			select {
			case <-h.done:
				log.Info().Msg("WebSocket: cleanup periodic stopped")
				return
			case <-ticker.C:
				result := h.cleanupInactiveClients()
				if result.removedClients > 0 {
					log.Info().
						Int("removed_clients", result.removedClients).
						Int("removed_rooms", result.removedRooms).
						Dur("elapsed", result.elapsed).
						Msg("WebSocket: cleanup completed")
				}
			}
		}
	}()
}

// cleanupInactiveClients удаляет клиентов с истёкшим временем бездействия.
// M10 (PASS-16): сбор кандидатов под RLock, удаление батчем под Lock — раньше
// полный проход по всем комнатам держал h.mu.Lock() (при тысячах соединений
// sweep блокировал register/unregister/dispatch на время прохода).
func (h *RoomHub) cleanupInactiveClients() cleanupResult {
	start := time.Now()
	result := cleanupResult{}
	now := time.Now()

	// Фаза 1 (RLock): собираем кандидатов. Чтение rooms/roomQueues безопасно
	// под RLock; LastActivity читается через отдельный client.mu (порядок
	// локов h.mu → client.mu консистентен с dispatchToRoom).
	type candidate struct {
		roomID string
		client *Client
	}
	var toRemove []candidate
	var roomsToRemove []string

	h.mu.RLock()
	for roomID, room := range h.rooms {
		for client := range room {
			if client.IsClosed() {
				toRemove = append(toRemove, candidate{roomID, client})
				continue
			}
			client.mu.Lock()
			lastActivity := client.LastActivity
			client.mu.Unlock()
			if now.Sub(lastActivity) > maxInactiveDuration {
				log.Warn().
					Str("client_id", client.ID).
					Str("room", roomID).
					Dur("inactive", now.Sub(lastActivity)).
					Msg("WebSocket: removing inactive client")
				toRemove = append(toRemove, candidate{roomID, client})
			}
		}
	}
	h.mu.RUnlock()

	// Фаза 2 (Lock): применяем удаление. client.Close() вне h.mu не обязателен
	// (Close идемпотентен и не требует h.mu), но delete комнаты — мутация.
	h.mu.Lock()
	removedByRoom := make(map[string]int, len(toRemove))
	for _, cand := range toRemove {
		room, ok := h.rooms[cand.roomID]
		if !ok {
			continue
		}
		if _, exists := room[cand.client]; !exists {
			continue // клиент уже удалён dispatch-ом между фазами
		}
		cand.client.Close()
		delete(room, cand.client)
		removedByRoom[cand.roomID]++
		result.removedClients++
	}
	for roomID, removed := range removedByRoom {
		if removed > 0 {
			// Состав комнаты изменился — кэш слайса stale (P-M2).
			delete(h.roomClients, roomID)
		}
		if len(h.rooms[roomID]) == 0 {
			roomsToRemove = append(roomsToRemove, roomID)
		}
	}
	for _, roomID := range roomsToRemove {
		delete(h.rooms, roomID)
		// C-1/G2 (pass 40): очередь НЕ закрываем (send-on-closed panic);
		// воркер завершится по idle-таймеру. Удаляем из map, чтобы новые
		// broadcast создали свежую очередь/воркер.
		delete(h.roomQueues, roomID)
		delete(h.roomClients, roomID)
		result.removedRooms++
	}
	h.mu.Unlock()

	result.elapsed = time.Since(start)
	return result
}

// GetHealthStatus возвращает состояние WebSocket-хаба для health-check.
func (h *RoomHub) GetHealthStatus() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Подсчитаем уникальные комнаты
	roomCount := 0
	for range h.rooms {
		roomCount++
	}

	// P2: копируем map — caller (admin handler) сериализует её в JSON после
	// освобождения лока, одновременная запись в живую map = concurrent map panic.
	perIPCpy := make(map[string]int, len(h.connsPerIP))
	for k, v := range h.connsPerIP {
		perIPCpy[k] = v
	}

	return map[string]any{
		"status":       "healthy",
		"total_conns":  h.totalConns,
		"max_total":    h.maxTotalConns,
		"rooms":        roomCount,
		"conns_per_ip": perIPCpy,
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
	}
}
