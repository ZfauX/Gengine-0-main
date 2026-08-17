// internal/pkg/websocket/room_hub_pubsub.go
//
// Cross-instance рассылка WebSocket-сообщений через Valkey pub/sub
// (MULTI-INSTANCE, PASS-12).
//
// Проблема: RoomHub держит подключения в памяти КОНКРЕТНОГО инстанса. Если
// игрок A подключён к инстансу 1, а сервис-источник события работает на
// инстансе 2 — локальный BroadcastToRoom на инстансе 2 не доставит сообщение.
//
// Решение: при BroadcastToRoom публикуем сообщение в канал gengine:ws;
// ВСЕ инстансы подписаны на него и рассылают сообщение своим локальным
// клиентам. Anti-эхо: каждое сообщение несёт Origin (instanceID отправителя);
// инстанс-отправитель уже расслал локально и пропускает своё сообщение из
// подписки. Без Valkey (bus == nil) поведение не меняется — только локально.
package websocket

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"

	"gengine-0/internal/pkg/realtimebus"

	"github.com/rs/zerolog/log"
)

// wsBusMsg — сообщение, публикуемое в канал gengine:ws.
type wsBusMsg struct {
	Origin string `json:"origin"` // instanceID отправителя (anti-эхо)
	Room   string `json:"room"`
	Data   string `json:"data"` // base64 (бинарно-безопасно)
	Sig    string `json:"sig"`  // base64 HMAC-SHA256(secret, origin|room|data) (M6, PASS-22)
}

// signWSMsg вычисляет HMAC-SHA256 подпись сообщения (M6, PASS-22):
// любой, кто может писать в Valkey pub/sub (или прослушивать канал), не должен
// уметь подделывать broadcast-сообщения. Ключ — instance-секрет приложения.
func signWSMsg(secret []byte, origin, room, data string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(origin))
	mac.Write([]byte("|"))
	mac.Write([]byte(room))
	mac.Write([]byte("|"))
	mac.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// verifyWSMsg проверяет подпись. Пустая Sig или несовпадение — отбрасываем.
func verifyWSMsg(secret []byte, msg *wsBusMsg) bool {
	if len(secret) == 0 {
		return true // secret не задан (несовместимый инстанс/тест) — пропускаем
	}
	want := signWSMsg(secret, msg.Origin, msg.Room, msg.Data)
	// want и msg.Sig — base64-строки одинаковой длины; hmac.Equal устойчив к
	// timing-атакам.
	return hmac.Equal([]byte(want), []byte(msg.Sig))
}

// pubSubPublishTimeout — таймаут публикации в Valkey. Ошибка публикации НЕ
// блокирует локальную рассылку (fail-open): сообщение всё равно дойдёт до
// клиентов этого инстанса, cross-instance доставка деградирует до локальной.
const pubSubPublishTimeout = 2 * time.Second

// pubSubPublishConcurrency (M9, PASS-22): макс. число параллельных публикаций
// в Valkey. Ограничивает фон-горутины publishToBus; при переполнении — скип.
const pubSubPublishConcurrency = 32

// busFields защищает конфигурацию pub/sub (устанавливается один раз до Run).
// Доступ к полям синхронизируется через h.busMu (RoomHub).
type busFields struct {
	bus        realtimebus.Bus
	instanceID string
	secret     []byte // M6 (PASS-22): ключ HMAC-подписи broadcast-сообщений
	ctx        context.Context
	cancel     context.CancelFunc
}

// SetPubSub включает cross-instance рассылку. bus == nil — работаем локально.
// instanceID — уникальный идентификатор этого инстанса (из main.go).
// secret (M6, PASS-22) — ключ HMAC-подписи сообщений (см. cfg.Session.Secret).
func (h *RoomHub) SetPubSub(bus realtimebus.Bus, instanceID string, secret []byte) {
	h.busMu.Lock()
	defer h.busMu.Unlock()
	h.busFields = &busFields{
		bus:        bus,
		instanceID: instanceID,
		secret:     append([]byte(nil), secret...),
	}
	if bus == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.busFields.ctx = ctx
	h.busFields.cancel = cancel
	// Подписываемся на канал WebSocket-сообщений. Обработчик вызывает
	// enqueueLocal (НЕ BroadcastToRoom — иначе бесконечный цикл publish).
	bus.Subscribe(ctx, realtimebus.WSChannel, h.handleRemoteBroadcast)
}

// StopPubSub отменяет подписку (вызывается в Stop).
func (h *RoomHub) StopPubSub() {
	h.busMu.Lock()
	defer h.busMu.Unlock()
	if h.busFields != nil && h.busFields.cancel != nil {
		h.busFields.cancel()
	}
}

// handleRemoteBroadcast рассылает сообщение, полученное от ДРУГОГО инстанса,
// локальным клиентам. Своё сообщение (origin == instanceID) пропускаем.
func (h *RoomHub) handleRemoteBroadcast(_ string, payload []byte) {
	h.busMu.RLock()
	fields := h.busFields
	h.busMu.RUnlock()
	if fields == nil {
		return
	}
	var msg wsBusMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Debug().Err(err).Msg("RoomHub: pub/sub malformed message")
		return
	}
	if msg.Origin == fields.instanceID {
		return // эхо собственного сообщения — уже расслано локально
	}
	// M6 (PASS-22): подпись — отбрасываем подделанные/несанкционированные
	// публикации в pub/sub канал.
	if !verifyWSMsg(fields.secret, &msg) {
		log.Warn().Str("origin", msg.Origin).Msg("RoomHub: pub/sub message with invalid HMAC dropped")
		return
	}
	data, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil {
		log.Debug().Err(err).Msg("RoomHub: pub/sub bad base64 payload")
		return
	}
	// enqueueLocal (через канал broadcast) — НЕ BroadcastToRoom, чтобы не
	// публиковать повторно.
	h.enqueueLocal(msg.Room, data)
}

// publishToBus публикует сообщение в канал (fail-open). Вызывается из
// BroadcastToRoom ДО локальной рассылки. M9 (PASS-22): публикация асинхронная
// (фоновые горутины с семафором publishSem) — недоступный/медленный Valkey
// (Publish блокируется до pubSubPublishTimeout) больше НЕ задерживает
// локальную рассылку.
func (h *RoomHub) publishToBus(roomID string, data []byte) {
	h.busMu.RLock()
	fields := h.busFields
	h.busMu.RUnlock()
	if fields == nil || fields.bus == nil || fields.instanceID == "" {
		return
	}
	msg := wsBusMsg{
		Origin: fields.instanceID,
		Room:   roomID,
		Data:   base64.StdEncoding.EncodeToString(data),
		Sig:    signWSMsg(fields.secret, fields.instanceID, roomID, base64.StdEncoding.EncodeToString(data)),
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	// Семафор: не плодим горутины при пике сообщений/недоступном Valkey.
	select {
	case h.publishSem <- struct{}{}:
	default:
		log.Debug().Str("room", roomID).Msg("RoomHub: pub/sub publish concurrency limit hit, skipping (local only)")
		return
	}
	go func() {
		defer func() { <-h.publishSem }()
		ctx, cancel := context.WithTimeout(context.Background(), pubSubPublishTimeout)
		defer cancel()
		if err := fields.bus.Publish(ctx, realtimebus.WSChannel, payload); err != nil {
			log.Debug().Err(err).Str("room", roomID).Msg("RoomHub: pub/sub publish failed (local only)")
		}
	}()
}

// enqueueLocal кладёт сообщение в канал broadcast (как BroadcastToRoom), НЕ
// публикуя его в Valkey. Используется подписчиком для локальной рассылки.
func (h *RoomHub) enqueueLocal(roomID string, data []byte) {
	select {
	case h.broadcast <- &Message{Room: roomID, Data: data}:
	case <-h.done:
		log.Warn().Str("room", roomID).Msg("RoomHub: broadcast failed, hub is stopped")
	default:
		log.Warn().Str("room", roomID).Msg("RoomHub: broadcast buffer full, dropping message")
	}
}
