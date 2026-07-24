// internal/domain/game/model.go
package game

import (
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

// ---------- локальный тип для голосований (чтобы избежать циклического импорта с monitor) ----------

type gameBlackboxVotingSession struct {
	gorm.Model
	GamePassingID uint
	LevelID       uint
	IsOpen        bool
	WinnerOption  string
}

func (gameBlackboxVotingSession) TableName() string { return "blackbox_voting_sessions" }

// ---------- основные модели ----------

type Game struct {
	gorm.Model
	Name                 string     `form:"name" binding:"required,min=2,max=100"`
	Description          string     `form:"description" binding:"max=2000"`
	AuthorID             uint       `gorm:"not null;index:idx_games_author"`
	Author               user.User  `gorm:"foreignKey:AuthorID"`
	IsDraft              bool       `gorm:"index:idx_games_author_status"`
	Visibility           string     `gorm:"default:'public';index:idx_games_visibility"`
	StartsAt             *time.Time `form:"starts_at" time_format:"2006-01-02T15:04"`
	RegistrationDeadline *time.Time `form:"registration_deadline" time_format:"2006-01-02T15:04"`
	MaxTeamNumber        int        `gorm:"default:10" form:"max_team_number"`
	CoverPath            string
	Levels               []level.Level `gorm:"foreignKey:GameID"`
	GameSetting          GameSetting   `gorm:"foreignKey:GameID"`
	Passings             []GamePassing `gorm:"foreignKey:GameID"`
	Reviews              []Review      `gorm:"foreignKey:GameID"`
	CoAuthors            []CoAuthor    `gorm:"foreignKey:GameID"`
	Notes                []Note        `gorm:"foreignKey:GameID"`
}

type GameSetting struct {
	gorm.Model
	GameID                   uint `gorm:"uniqueIndex"`
	AllowHints               bool `gorm:"default:true"`
	HintPenaltySeconds       int  `gorm:"default:300"`
	MaxHints                 int  `gorm:"default:3"`
	PerLevelTimeLimit        int  `gorm:"default:0"`
	HideAnswersUntilFinished bool `gorm:"default:false"`
	AutoStart                bool `gorm:"default:false"`
}

type GamePassing struct {
	gorm.Model
	GameID         uint              `gorm:"not null;index:idx_game_passings_game"`
	TeamID         uint              `gorm:"not null;index:idx_game_passings_team"`
	Status         GamePassingStatus `gorm:"default:'pending';index:idx_game_passings_status"`
	ResultDuration *time.Duration    `gorm:"type:bigint"`
	Place          *int
	Game           Game                        `gorm:"foreignKey:GameID"`
	Team           team.Team                   `gorm:"foreignKey:TeamID"`
	Progresses     []LevelProgress             `gorm:"foreignKey:GamePassingID"`
	Logs           []Log                       `gorm:"foreignKey:GamePassingID"`
	VotingSessions []gameBlackboxVotingSession `gorm:"foreignKey:GamePassingID"`
}

type LevelProgress struct {
	gorm.Model
	GamePassingID  uint `gorm:"not null;index:idx_level_progress_passing"`
	LevelID        uint `gorm:"not null;index:idx_level_progress_level"`
	StartedAt      time.Time
	FinishedAt     *time.Time
	HintsUsed      int
	PenaltySeconds int
	GamePassing    GamePassing `gorm:"foreignKey:GamePassingID"`
	Level          level.Level `gorm:"foreignKey:LevelID"`
	Attempts       []Attempt   `gorm:"foreignKey:LevelProgressID"`
}

type Attempt struct {
	gorm.Model
	LevelProgressID uint `gorm:"not null;index:idx_attempts_progress"`
	Code            string
	FilePath        string
	IsFile          bool
	Success         bool
	LevelProgress   LevelProgress `gorm:"foreignKey:LevelProgressID"`
}

// SubmitResult содержит результат успешной отправки кода/файла.
// GameID заполняется после транзакции для вызова CalculateResults.
type SubmitResult struct {
	Attempt *Attempt
	GameID  uint
}

type CoAuthor struct {
	gorm.Model
	GameID uint      `gorm:"not null;uniqueIndex:idx_game_user"`
	UserID uint      `gorm:"not null;uniqueIndex:idx_game_user"`
	Role   string    `gorm:"default:'content_editor'"`
	Game   Game      `gorm:"foreignKey:GameID"`
	User   user.User `gorm:"foreignKey:UserID"`
}

type Note struct {
	gorm.Model
	GameID  uint `gorm:"not null;index"`
	UserID  uint `gorm:"not null;index"`
	LevelID *uint
	Text    string
	Game    Game        `gorm:"foreignKey:GameID"`
	User    user.User   `gorm:"foreignKey:UserID"`
	Level   level.Level `gorm:"foreignKey:LevelID"`
}

type Review struct {
	gorm.Model
	GameID  uint `gorm:"not null;index"`
	UserID  uint `gorm:"not null;index"`
	Rating  int  `gorm:"not null"`
	Comment string
	Game    Game      `gorm:"foreignKey:GameID"`
	User    user.User `gorm:"foreignKey:UserID"`
}

type Photo struct {
	gorm.Model
	GameID  uint `gorm:"not null;index"`
	UserID  uint `gorm:"not null;index"`
	LevelID *uint
	Path    string
	Game    Game        `gorm:"foreignKey:GameID"`
	User    user.User   `gorm:"foreignKey:UserID"`
	Level   level.Level `gorm:"foreignKey:LevelID"`
}

type PlayerRating struct {
	UserID    uint      `gorm:"primaryKey"`
	Score     int       `gorm:"default:0"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

type Log struct {
	gorm.Model
	GamePassingID uint `gorm:"not null;index:idx_logs_passing"`
	LevelID       uint `gorm:"index:idx_logs_level"`
	Message       string
	GamePassing   GamePassing `gorm:"foreignKey:GamePassingID"`
	Level         level.Level `gorm:"foreignKey:LevelID"`
}

// ---------- типы и константы ----------

type GameFilter struct {
	Status   string
	Search   string
	DateFrom string
	DateTo   string
	ViewerID uint
	AuthorID *uint
}

type GameSort struct {
	Field string
	Order SortOrder
}

type GamePassingStatus string

const (
	StatusPending      GamePassingStatus = "pending"
	StatusAccepted     GamePassingStatus = "accepted"
	StatusRejected     GamePassingStatus = "rejected"
	StatusStarted      GamePassingStatus = "started"
	StatusFinished     GamePassingStatus = "finished"
	StatusDisqualified GamePassingStatus = "disqualified"
	StatusTesting      GamePassingStatus = "testing"
)

type SortOrder string

const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)
