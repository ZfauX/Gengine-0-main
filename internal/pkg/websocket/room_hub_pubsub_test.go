// internal/pkg/websocket/room_hub_pubsub_test.go
//
// Тесты cross-instance рассылки RoomHub через Valkey pub/sub (miniredis):
//   - сообщение, опубликованное на инстансе 1, доходит до клиента на инстансе 2;
//   - отсутствие эха (своё сообщение не рассылается повторно);
//   - без Valkey (bus == nil) — только локальная рассылка (старое поведение).
package websocket

import (
	"testing"
	"time"

	"gengine-0/internal/pkg/realtimebus"

	"github.com/alicebob/miniredis/v2"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestHubWithBus создаёт хаб с cross-instance шиной на miniredis.
func newTestHubWithBus(t *testing.T, mr *miniredis.Miniredis, instanceID string) *RoomHub {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	bus := realtimebus.NewValkeyBus(client)
	t.Cleanup(bus.Close)

	hub := NewRoomHub()
	hub.SetPubSub(bus, instanceID)
	hub.Run()
	t.Cleanup(hub.Stop)
	return hub
}

// registerTestClient регистрирует тестового клиента в комнате. dispatchToRoom
// пишет только в client.Send (Conn не используется) — достаточно нулевого
// *websocket.Conn. Возвращает канал с сообщениями.
func registerTestClient(t *testing.T, hub *RoomHub, roomID string) <-chan []byte {
	t.Helper()
	client := NewClient(&websocket.Conn{}, roomID, "127.0.0.1")
	client.Send = make(chan []byte, 32)
	hub.RegisterClient(client)
	t.Cleanup(func() { hub.UnregisterClient(client) })
	return client.Send
}

// publishWithRetry публикует сообщение несколько раз с паузой: подписка
// асинхронная (redis Subscribe), первая публикация может потеряться, пока
// каналы не готовы. Реальный клиент на reconnect ведёт себя так же.
func publishWithRetry(hub *RoomHub, roomID string, data []byte) {
	for range 5 {
		hub.BroadcastToRoom(roomID, data)
		time.Sleep(100 * time.Millisecond)
	}
}

func TestRoomHub_PubSub_CrossInstance(t *testing.T) {
	mr := miniredis.RunT(t)

	hub1 := newTestHubWithBus(t, mr, "instance-1")
	hub2 := newTestHubWithBus(t, mr, "instance-2")

	got2 := registerTestClient(t, hub2, "room")
	require.Eventually(t, func() bool {
		return hub2.RoomClientCount("room") == 1
	}, 3*time.Second, 10*time.Millisecond)

	// Инстанс 1 публикует — клиент на инстансе 2 должен получить.
	publishWithRetry(hub1, "room", []byte("hello from instance 1"))

	select {
	case msg := <-got2:
		assert.Equal(t, "hello from instance 1", string(msg))
	case <-time.After(5 * time.Second):
		t.Fatal("client on instance 2 did not receive cross-instance message")
	}
}

func TestRoomHub_PubSub_NoEcho(t *testing.T) {
	mr := miniredis.RunT(t)

	hub1 := newTestHubWithBus(t, mr, "instance-1")
	hub2 := newTestHubWithBus(t, mr, "instance-2")

	got1 := registerTestClient(t, hub1, "room")
	got2 := registerTestClient(t, hub2, "room")
	require.Eventually(t, func() bool {
		return hub1.RoomClientCount("room") == 1 && hub2.RoomClientCount("room") == 1
	}, 3*time.Second, 10*time.Millisecond)

	// Подписка Redis pub/sub устанавливается асинхронно: пока она не готова,
	// первые публикации теряются (флейк под -race/CI). Прогреваем подписку —
	// публикуем «ready» с ретраями, пока ОБА клиента не получат хотя бы одно.
	publishWithRetry(hub1, "room", []byte("ready"))
	waitReady := func(ch <-chan []byte, label string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not receive ready message", label)
		}
	}
	waitReady(got1, "local client")
	waitReady(got2, "remote client")

	// Вычитываем из каналов возможные лишние «ready» (ретраи доставили
	// несколько), чтобы каналы были пустыми перед контрольной публикацией.
	drain := func(ch <-chan []byte) {
		t.Helper()
		for {
			select {
			case <-ch:
			case <-time.After(200 * time.Millisecond):
				return
			}
		}
	}
	drain(got1)
	drain(got2)

	// Подписка гарантированно готова. Публикуем ОДНО контрольное сообщение:
	// каждый клиент должен получить его РОВНО один раз (доставка без эха —
	// собственное сообщение отправитель пропускает, anti-echo).
	hub1.BroadcastToRoom("room", []byte("control"))
	receiveControl := func(ch <-chan []byte, label string) {
		t.Helper()
		select {
		case msg := <-ch:
			assert.Equal(t, "control", string(msg), "%s client got wrong message", label)
		case <-time.After(5 * time.Second):
			t.Fatalf("%s client did not receive control message", label)
		}
	}
	receiveControl(got1, "local")
	receiveControl(got2, "remote")

	// После контрольного сообщения лишних (эхо/дубликатов) быть не должно.
	for _, c := range []struct {
		ch    <-chan []byte
		label string
	}{{got1, "local"}, {got2, "remote"}} {
		select {
		case msg := <-c.ch:
			t.Fatalf("%s client received extra message: %s", c.label, msg)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func TestRoomHub_PubSub_WithoutBus(t *testing.T) {
	// Без Valkey (SetPubSub не вызван) — локальная рассылка как раньше.
	hub := NewRoomHub()
	hub.Run()
	defer hub.Stop()

	got := registerTestClient(t, hub, "room")
	require.Eventually(t, func() bool {
		return hub.RoomClientCount("room") == 1
	}, 3*time.Second, 10*time.Millisecond)

	hub.BroadcastToRoom("room", []byte("local"))
	select {
	case msg := <-got:
		assert.Equal(t, "local", string(msg))
	case <-time.After(5 * time.Second):
		t.Fatal("local broadcast without bus failed")
	}
}
