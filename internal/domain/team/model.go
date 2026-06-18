// internal/domain/team/model.go
package team

import (
	"time"

	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

// InvitationStatus определяет текущее состояние приглашения.
type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationDeclined InvitationStatus = "declined"
)

// Team представляет команду, участвующую в играх.
type Team struct {
	gorm.Model
	Name        string       `gorm:"not null" form:"name"`
	CaptainID   uint         `gorm:"not null"`
	Captain     user.User    `gorm:"foreignKey:CaptainID"`
	Members     []user.User  `gorm:"many2many:team_members;"`
	Invitations []Invitation `gorm:"foreignKey:TeamID"`
}

// Invitation — приглашение пользователя в команду.
type Invitation struct {
	gorm.Model
	TeamID    uint             `gorm:"not null;index"`
	Team      Team             `gorm:"foreignKey:TeamID"`
	UserID    uint             `gorm:"not null;index"`
	User      user.User        `gorm:"foreignKey:UserID"`
	Status    InvitationStatus `gorm:"default:pending"`
	ExpiresAt *time.Time       // опциональное поле истечения срока
}