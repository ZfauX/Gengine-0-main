// internal/domain/social/model.go
package social

import (
	"time"

	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

// PlayerRating — рейтинг игрока (накопленные очки).
type PlayerRating struct {
	UserID    uint      `gorm:"primaryKey"`
	User      user.User `gorm:"foreignKey:UserID"`
	Score     int       `gorm:"default:0"`
	UpdatedAt time.Time
}

// Follow — подписка одного пользователя на другого (автора).
type Follow struct {
	gorm.Model
	FollowerID uint      `gorm:"not null;uniqueIndex:idx_follow"`
	AuthorID   uint      `gorm:"not null;uniqueIndex:idx_follow"`
	Follower   user.User `gorm:"foreignKey:FollowerID;constraint:OnDelete:CASCADE"`
	Author     user.User `gorm:"foreignKey:AuthorID;constraint:OnDelete:CASCADE"`
}
