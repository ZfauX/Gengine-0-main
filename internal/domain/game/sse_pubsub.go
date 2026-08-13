// internal/domain/game/sse_pubsub.go
//
// Cross-instance рассылка SSE-событий через Valkey pub/sub (MULTI-INSTANCE,
// PASS-12). Аналогично RoomHub: SSEManager держит подписки в памяти КОНКРЕТНОГО
// инстанса; источник события может работать на другом инстансе.
//
// Решение: при Broadcast публикуем {origin, game_id, type, data} в канал
// gengine:sse; все инстансы рассылают полученные события своим локальным
// сессиям. Anti-эхо: origin == instanceID отправителя пропускается (уже
// расслано локально). Без Valkey (bus == nil) — только локальная рассылка.
package game

import (
	"context"
	"encoding/json"
	"time"

	"gengine-0/internal/pkg/realtimebus"

	"github.com/rs/zerolog/log"
)

// sseBusMsg — сообщение в канале gengine:sse.
type sseBusMsg struct {
	Origin  string `json:"origin"` // instanceID отправителя (anti-эхо)
	GameID  uint   `json:"game_id"`
	Type    string `json:"type"`
	DataRaw string `json:"data"` // JSON-строка
}

const ssePubSubPublishTimeout = 2 * time.Second

// sseBusFields защищает конфигурацию pub/sub (устанавливается один раз).
// Доступ к полям синхронизируется через m.busMu (SSEManager).
type sseBusFields struct {
	bus        realtimebus.Bus
	instanceID string
	ctx        context.Context
	cancel     context.CancelFunc
}

// SetPubSub включает cross-instance рассылку SSE. bus == nil — локально.
func (m *SSEManager) SetPubSub(bus realtimebus.Bus, instanceID string) {
	m.busMu.Lock()
	defer m.busMu.Unlock()
	m.sseBus = &sseBusFields{bus: bus, instanceID: instanceID}
	if bus == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.sseBus.ctx = ctx
	m.sseBus.cancel = cancel
	bus.Subscribe(ctx, realtimebus.SSEChannel, m.handleRemoteBroadcast)
}

// StopPubSub отменяет подписку.
func (m *SSEManager) StopPubSub() {
	m.busMu.Lock()
	defer m.busMu.Unlock()
	if m.sseBus != nil && m.sseBus.cancel != nil {
		m.sseBus.cancel()
	}
}

// handleRemoteBroadcast рассылает событие от ДРУГОГО инстанса локальным сессиям.
func (m *SSEManager) handleRemoteBroadcast(_ string, payload []byte) {
	m.busMu.RLock()
	fields := m.sseBus
	m.busMu.RUnlock()
	if fields == nil {
		return
	}
	var msg sseBusMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Debug().Err(err).Msg("SSE: pub/sub malformed message")
		return
	}
	if msg.Origin == fields.instanceID {
		return // эхо собственного сообщения
	}
	var data any
	if msg.DataRaw != "" {
		if err := json.Unmarshal([]byte(msg.DataRaw), &data); err != nil {
			log.Debug().Err(err).Msg("SSE: pub/sub bad data")
			return
		}
	}
	// broadcastLocal — НЕ Broadcast (не публикует повторно).
	m.broadcastLocal(msg.GameID, msg.Type, data)
}

// publishToBus публикует SSE-событие (fail-open).
func (m *SSEManager) publishToBus(gameID uint, eventType string, data any) {
	m.busMu.RLock()
	fields := m.sseBus
	m.busMu.RUnlock()
	if fields == nil || fields.bus == nil || fields.instanceID == "" {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := sseBusMsg{
		Origin:  fields.instanceID,
		GameID:  gameID,
		Type:    eventType,
		DataRaw: string(raw),
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ssePubSubPublishTimeout)
	defer cancel()
	if err := fields.bus.Publish(ctx, realtimebus.SSEChannel, payload); err != nil {
		log.Debug().Err(err).Uint("game_id", gameID).Msg("SSE: pub/sub publish failed (local only)")
	}
}
