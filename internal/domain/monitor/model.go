// internal/domain/monitor/model.go
package monitor

import (
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

// Типы комнат чата (B-1, pass 45).
const (
	RoomTypeGameGeneral  = "game_general"  // общий чат игры (все участники)
	RoomTypeGameCaptains = "game_captains" // только капитаны команд
	RoomTypeTeamGeneral  = "team_general"  // общий чат команды
	RoomTypeTeamFlood    = "team_flood"    // флудилка команды
	RoomTypePersonal     = "personal"      // личный чат 1-на-1
	RoomTypeServer       = "server"        // общий чат всех игроков сервера
)

// Права участника комнаты (B-5, pass 45).
const (
	PermRead   = "read"   // читать сообщения
	PermWrite  = "write"  // писать сообщения
	PermAttach = "attach" // присылать вложения
)

type ChatRoom struct {
	gorm.Model
	GameID    *uint     `gorm:"uniqueIndex:idx_chat_room_unique"`
	Game      game.Game `gorm:"foreignKey:GameID"`
	TeamID    *uint     `gorm:"uniqueIndex:idx_chat_room_unique"`
	PassingID *uint     `gorm:"uniqueIndex:idx_chat_room_unique"`
	Name      string
	// B-1 (pass 45): тип комнаты (game_general/game_captains/team_general/...)
	RoomType string `gorm:"default:'game_general';index:idx_chat_rooms_type"`
	// B-1 (pass 45): владелец комнаты (автор игры/команды или создатель личной).
	OwnerID *uint `gorm:"index:idx_chat_rooms_owner"`
	// B-7 (pass 45): личный чат 1-на-1 (User1ID < User2ID).
	User1ID  *uint            `gorm:"index:idx_chat_rooms_users"`
	User2ID  *uint            `gorm:"index:idx_chat_rooms_users"`
	Messages []ChatMessage    `gorm:"foreignKey:RoomID"`
	Members  []ChatRoomMember `gorm:"foreignKey:RoomID"`
}

// ChatRoomMember — членство в комнате с правами (B-1/B-5, pass 45).
type ChatRoomMember struct {
	RoomID uint `gorm:"primaryKey;index:idx_room_member_room"`
	UserID uint `gorm:"primaryKey;index:idx_room_member_user"`
	// Права: значения из Perm* (read/write/attach).
	CanRead   bool `gorm:"default:true"`
	CanWrite  bool `gorm:"default:true"`
	CanAttach bool `gorm:"default:false"`
}

type ChatMessage struct {
	gorm.Model
	RoomID  uint      `gorm:"not null;index:idx_chat_messages_room"`
	Room    ChatRoom  `gorm:"foreignKey:RoomID"`
	UserID  uint      `gorm:"not null;index:idx_chat_messages_user"`
	User    user.User `gorm:"foreignKey:UserID"`
	Content string    `gorm:"not null"`
}

type BlackboxVotingSession struct {
	gorm.Model
	GamePassingID uint             `gorm:"uniqueIndex:idx_passing_level"`
	GamePassing   game.GamePassing `gorm:"foreignKey:GamePassingID"`
	LevelID       uint             `gorm:"not null;uniqueIndex:idx_passing_level"`
	Level         level.Level      `gorm:"foreignKey:LevelID"`
	IsOpen        bool             `gorm:"default:true"`
	WinnerOption  string
}

type BlackboxVote struct {
	gorm.Model
	SessionID uint                  `gorm:"not null;uniqueIndex:idx_session_voter"`
	Session   BlackboxVotingSession `gorm:"foreignKey:SessionID"`
	VoterID   uint                  `gorm:"not null;uniqueIndex:idx_session_voter"`
	Option    string                `gorm:"not null"`
}
