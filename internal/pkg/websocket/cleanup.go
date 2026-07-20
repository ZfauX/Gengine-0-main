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
func (h *RoomHub) cleanupInactiveClients() cleanupResult {
	start := time.Now()
	result := cleanupResult{}

	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	roomsToRemove := []string{}

	for roomID, room := range h.rooms {
		clientsToRemove := []*Client{}

		for client := range room {
			// Если клиент закрыт — удаляем
			if client.IsClosed() {
				clientsToRemove = append(clientsToRemove, client)
				continue
			}

			// Проверяем lastActivity
			client.mu.Lock()
			lastActivity := client.LastActivity
			client.mu.Unlock()

			// Если клиент не активен более maxInactiveDuration — удаляем
			if now.Sub(lastActivity) > maxInactiveDuration {
				log.Warn().
					Str("client_id", client.ID).
					Str("room", roomID).
					Dur("inactive", now.Sub(lastActivity)).
					Msg("WebSocket: removing inactive client")
				clientsToRemove = append(clientsToRemove, client)
			}
		}

		// Удаляем неактивных клиентов из комнаты
		for _, client := range clientsToRemove {
			_ = client.Conn.Close()
			client.Close()
			delete(room, client)
			// Уменьшаем счётчики напрямую (decConnection тоже блокирует mu.Lock(), что вызовет дедлок)
			if h.totalConns > 0 {
				h.totalConns--
			}
			if count, ok := h.connsPerIP[client.RemoteIP]; ok && count > 0 {
				if count == 1 {
					delete(h.connsPerIP, client.RemoteIP)
				} else {
					h.connsPerIP[client.RemoteIP] = count - 1
				}
			}
			result.removedClients++
		}

		// Если комната пуста — удаляем
		if len(room) == 0 {
			roomsToRemove = append(roomsToRemove, roomID)
		}
	}

	for _, roomID := range roomsToRemove {
		delete(h.rooms, roomID)
		result.removedRooms++
	}

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

	return map[string]any{
		"status":       "healthy",
		"total_conns":  h.totalConns,
		"max_total":    h.maxTotalConns,
		"rooms":        roomCount,
		"conns_per_ip": h.connsPerIP,
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
	}
}
