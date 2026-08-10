// internal/domain/monitor/handler_test.go
package monitor

import (
	"context"
	"errors"
	"testing"

	"gengine-0/internal/domain/game"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// S-46-2 (pass 46): правила доступа к комнатам чата вынесены в canJoinRoom —
// unit-тестируются через интерфейсные заглушки без полного графа сервисов.

func ptr[T any](v T) *T { return &v }

func TestCanJoinRoom_Personal(t *testing.T) {
	u1, u2 := uint(1), uint(2)
	room := &ChatRoom{RoomType: RoomTypePersonal, User1ID: &u1, User2ID: &u2}
	deps := chatAccessDeps{}

	// Участник — допускается.
	ok, err := canJoinRoom(room, u1, deps)
	require.NoError(t, err)
	assert.True(t, ok)

	// Чужой — нет.
	ok, err = canJoinRoom(room, 99, deps)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCanJoinRoom_TeamRoom(t *testing.T) {
	teamID := uint(10)
	room := &ChatRoom{RoomType: RoomTypeTeamGeneral, TeamID: &teamID}

	// Участник команды.
	ok, err := canJoinRoom(room, 5, chatAccessDeps{
		IsTeamMemberOrCaptain: func(_ context.Context, tid, uid uint) (bool, error) {
			return tid == 10 && uid == 5, nil
		},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	// Не участник.
	ok, err = canJoinRoom(room, 6, chatAccessDeps{
		IsTeamMemberOrCaptain: func(_ context.Context, _, _ uint) (bool, error) { return false, nil },
	})
	require.NoError(t, err)
	assert.False(t, ok)

	// Ошибка проверки — пробрасывается.
	_, err = canJoinRoom(room, 5, chatAccessDeps{
		IsTeamMemberOrCaptain: func(_ context.Context, _, _ uint) (bool, error) {
			return false, errors.New("db down")
		},
	})
	require.Error(t, err)
}

func TestCanJoinRoom_CaptainsRoom(t *testing.T) {
	gameID := uint(100)
	room := &ChatRoom{RoomType: RoomTypeGameCaptains, GameID: &gameID}

	deps := chatAccessDeps{
		GetPassingByUser: func(_ context.Context, gid, _ uint) (*game.GamePassing, error) {
			if gid != 100 {
				return nil, errors.New("not found")
			}
			return &game.GamePassing{TeamID: 7}, nil
		},
		IsTeamCaptain: func(_ context.Context, tid, uid uint) (bool, error) {
			return tid == 7 && uid == 3, nil
		},
	}

	// Капитан — да.
	ok, err := canJoinRoom(room, 3, deps)
	require.NoError(t, err)
	assert.True(t, ok)

	// Не капитан — нет.
	ok, err = canJoinRoom(room, 4, deps)
	require.NoError(t, err)
	assert.False(t, ok)

	// Не участник игры — нет.
	ok, err = canJoinRoom(room, 4, chatAccessDeps{
		GetPassingByUser: func(_ context.Context, _, _ uint) (*game.GamePassing, error) {
			return nil, errors.New("not found")
		},
	})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCanJoinRoom_GeneralGameRoom(t *testing.T) {
	gameID := uint(200)
	room := &ChatRoom{RoomType: RoomTypeGameGeneral, GameID: &gameID}

	// Менеджер — да.
	ok, err := canJoinRoom(room, 1, chatAccessDeps{
		IsUserManager: func(_ context.Context, _, uid uint) (bool, error) { return uid == 1, nil },
	})
	require.NoError(t, err)
	assert.True(t, ok)

	// Участник прохождения — да.
	ok, err = canJoinRoom(room, 2, chatAccessDeps{
		IsUserManager: func(_ context.Context, _, _ uint) (bool, error) { return false, nil },
		GetPassingByUser: func(_ context.Context, _, _ uint) (*game.GamePassing, error) {
			return &game.GamePassing{}, nil
		},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	// Посторонний — нет.
	ok, err = canJoinRoom(room, 3, chatAccessDeps{
		IsUserManager: func(_ context.Context, _, _ uint) (bool, error) { return false, nil },
		GetPassingByUser: func(_ context.Context, _, _ uint) (*game.GamePassing, error) {
			return nil, errors.New("not found")
		},
	})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestCanJoinRoom_ServerRoom(t *testing.T) {
	room := &ChatRoom{RoomType: RoomTypeServer}
	ok, err := canJoinRoom(room, 42, chatAccessDeps{})
	require.NoError(t, err)
	assert.True(t, ok, "серверный чат доступен любому аутентифицированному")
}
