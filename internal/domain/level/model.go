// Package level реализует управление уровнями, вопросами и ответами игр.
package level

import (
	"gorm.io/gorm"
)

// LevelType определяет тип уровня.
const (
	TypeSingle        = "single"
	TypeCheckpoint    = "checkpoint"
	TypeParallelGroup = "parallel_group"
	TypeBlackbox      = "blackbox"
	TypeFileUpload    = "file_upload"
)

// Level представляет собой один уровень (задание) в игре.
type Level struct {
	gorm.Model
	GameID               uint    `gorm:"not null;uniqueIndex:idx_game_position"`
	Name                 string  `gorm:"not null" form:"name"`
	Description          string  `form:"description"`
	Position             int     `gorm:"default:0;uniqueIndex:idx_game_position" form:"position"`
	Type                 string  `gorm:"default:single" form:"type"`
	ParentID             *uint   `gorm:"index"`
	GroupID              *uint   `gorm:"index"`
	MinChildren          int     `gorm:"default:0"`
	RequiresConfirmation bool    `gorm:"default:false" form:"requires_confirmation"`
	Latitude             float64 `form:"latitude"`
	Longitude            float64 `form:"longitude"`

	Questions    []Question    `gorm:"foreignKey:LevelID"`
	MiniGame     *MiniGame     `gorm:"foreignKey:LevelID"`
	Children     []Level       `gorm:"foreignKey:ParentID"`
	GroupMembers []Level       `gorm:"foreignKey:GroupID"`
}

// Question — вопрос внутри уровня.
type Question struct {
	gorm.Model
	LevelID uint   `gorm:"not null;index"`
	Text    string `gorm:"not null" form:"text"`
	Hint    string `form:"hint"`

	Answers []Answer `gorm:"foreignKey:QuestionID"`
}

// Answer — правильный ответ (код) для вопроса.
type Answer struct {
	gorm.Model
	QuestionID uint   `gorm:"not null;index"`
	Code       string `gorm:"not null" form:"code"`
}

// MiniGame — мини-игра, привязанная к уровню.
type MiniGame struct {
	gorm.Model
	LevelID uint   `gorm:"uniqueIndex;not null"`
	Type    string `gorm:"not null"` // cipher, puzzle, quiz
	Answer  string `gorm:"not null"`
	Config  string `gorm:"type:text"` // JSON-строка с дополнительными параметрами
}