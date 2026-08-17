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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
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
	Sig     string `json:"sig"`  // base64 HMAC-SHA256(secret, origin|game_id|type|data) (M6, PASS-22)
}

// signSSEMsg вычисляет HMAC-SHA256 подпись SSE-события (M6, PASS-22).
func signSSEMsg(secret []byte, origin string, gameID uint, eventType, data string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(origin))
	mac.Write([]byte("|"))
	mac.Write([]byte(strconv.FormatUint(uint64(gameID), 10)))
	mac.Write([]byte("|"))
	mac.Write([]byte(eventType))
	mac.Write([]byte("|"))
	mac.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// verifySSEMsg проверяет подпись (M6, PASS-22). Пустой secret — пропускаем
// (несовместимый инстанс/тест); пустая/неверная Sig — отбрасываем.
func verifySSEMsg(secret []byte, msg *sseBusMsg) bool {
	if len(secret) == 0 {
		return true
	}
	want := signSSEMsg(secret, msg.Origin, msg.GameID, msg.Type, msg.DataRaw)
	// want и msg.Sig — base64-строки одинаковой длины; hmac.Equal устойчив к
	// timing-атакам. Декодировать не нужно — сравниваем строки.
	return hmac.Equal([]byte(want), []byte(msg.Sig))
}

const ssePubSubPublishTimeout = 2 * time.Second

// sseBusFields защищает конфигурацию pub/sub (устанавливается один раз).
// Доступ к полям синхронизируется через m.busMu (SSEManager).
type sseBusFields struct {
	bus        realtimebus.Bus
	instanceID string
	secret     []byte // M6 (PASS-22): ключ HMAC-подписи событий
	ctx        context.Context
	cancel     context.CancelFunc
}

// SetPubSub включает cross-instance рассылку SSE. bus == nil — локально.
// secret (M6, PASS-22) — ключ HMAC-подписи (см. cfg.Session.Secret).
func (m *SSEManager) SetPubSub(bus realtimebus.Bus, instanceID string, secret []byte) {
	m.busMu.Lock()
	defer m.busMu.Unlock()
	m.sseBus = &sseBusFields{bus: bus, instanceID: instanceID, secret: append([]byte(nil), secret...)}
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
	// M6 (PASS-22): подпись — отбрасываем подделанные публикации в канал.
	if !verifySSEMsg(fields.secret, &msg) {
		log.Warn().Str("origin", msg.Origin).Uint("game_id", msg.GameID).Msg("SSE: pub/sub message with invalid HMAC dropped")
		return
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
		Sig:     signSSEMsg(fields.secret, fields.instanceID, gameID, eventType, string(raw)),
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
