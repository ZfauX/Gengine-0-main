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

type GameBlackboxVotingSession struct {
	gorm.Model
	GamePassingID uint
	LevelID       uint
	IsOpen        bool
	WinnerOption  string
}

func (GameBlackboxVotingSession) TableName() string { return "blackbox_voting_sessions" }

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
	// Прекомпьютинг агрегатов (P3): обновляются триггерами reviews/game_passings.
	RatingValue      float64 `gorm:"column:rating_value;default:0"`
	ParticipantCount int     `gorm:"column:participant_count;default:0"`
	// RatingScored — очки рейтинга за эту игру уже начислены
	// (идемпотентность UpdateRatingsForGame, B3).
	RatingScored bool          `gorm:"column:rating_scored;default:false"`
	Levels       []level.Level `gorm:"foreignKey:GameID"`
	GameSetting  GameSetting   `gorm:"foreignKey:GameID"`
	Passings     []GamePassing `gorm:"foreignKey:GameID"`
	Reviews      []Review      `gorm:"foreignKey:GameID"`
	CoAuthors    []CoAuthor    `gorm:"foreignKey:GameID"`
	Notes        []Note        `gorm:"foreignKey:GameID"`
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

// defaultGameSetting возвращает значения настроек по умолчанию.
// Единый источник правды для UseHint, GetGameplayData и GetSettingsWithDefaults
// (B5 — раньше GetGameplayData падал на zero-value с AllowHints=false).
func defaultGameSetting(gameID uint) *GameSetting {
	return &GameSetting{
		GameID:                   gameID,
		AllowHints:               true,
		HintPenaltySeconds:       300,
		MaxHints:                 3,
		PerLevelTimeLimit:        0,
		HideAnswersUntilFinished: false,
		AutoStart:                false,
	}
}

type GamePassing struct {
	gorm.Model
	GameID         uint              `gorm:"not null;index:idx_game_passings_game"`
	TeamID         uint              `gorm:"not null;index:idx_game_passings_team"`
	Status         GamePassingStatus `gorm:"default:'pending';index:idx_game_passings_status"`
	ResultDuration *time.Duration    `gorm:"type:bigint"`
	Place          *int
	// TournamentScored — очки турнира за это прохождение уже начислены
	// (защита от двойного начисления при повторном вызове UpdateScoresForGame).
	TournamentScored bool                        `gorm:"column:tournament_scored;default:false"`
	Game             Game                        `gorm:"foreignKey:GameID"`
	Team             team.Team                   `gorm:"foreignKey:TeamID"`
	Progresses       []LevelProgress             `gorm:"foreignKey:GamePassingID"`
	Logs             []Log                       `gorm:"foreignKey:GamePassingID"`
	VotingSessions   []GameBlackboxVotingSession `gorm:"foreignKey:GamePassingID"`
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
