// internal/domain/monitor/model.go
package monitor

import (
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

type ChatRoom struct {
	gorm.Model
	GameID    *uint       `gorm:"index"`
	Game      game.Game   `gorm:"foreignKey:GameID"`
	TeamID    *uint       `gorm:"index"`
	PassingID *uint       `gorm:"index"`
	Name      string
	Messages  []ChatMessage `gorm:"foreignKey:RoomID"`
}

type ChatMessage struct {
	gorm.Model
	RoomID  uint      `gorm:"not null;index"`
	Room    ChatRoom  `gorm:"foreignKey:RoomID"`
	UserID  uint      `gorm:"not null"`
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
	SessionID uint                   `gorm:"not null;uniqueIndex:idx_session_voter"`
	Session   BlackboxVotingSession  `gorm:"foreignKey:SessionID"`
	VoterID   uint                   `gorm:"not null;uniqueIndex:idx_session_voter"`
	Option    string                 `gorm:"not null"`
}