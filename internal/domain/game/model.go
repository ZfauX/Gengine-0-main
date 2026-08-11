// internal/domain/game/model.go
package game

import (
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"

	"github.com/lib/pq"
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
	// A-4 (pass 45): ограничения состава команды.
	MaxHQPlayers    int `gorm:"default:0"` // 0 = без ограничения (штаб)
	MaxFieldPlayers int `gorm:"default:0"` // 0 = без ограничения (поле)
	MaxCarsPerTeam  int `gorm:"default:0"` // 0 = без ограничения (машины)
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
		MaxHQPlayers:             0,
		MaxFieldPlayers:          0,
		MaxCarsPerTeam:           0,
	}
}

type GamePassing struct {
	gorm.Model
	GameID         uint              `gorm:"not null;index:idx_game_passings_game"`
	TeamID         uint              `gorm:"not null;index:idx_game_passings_team"`
	Status         GamePassingStatus `gorm:"default:'pending';index:idx_game_passings_status"`
	ResultDuration *time.Duration    `gorm:"type:bigint"`
	Place          *int
	// DEEP-REVIEW (pass 46): мультитурнирный скоринг. Вместо булева флага
	// храним массив tournament_id, которым уже начислены очки за это
	// прохождение. Игра в 2+ турнирах больше не теряет очки во втором.
	TournamentScoredIDs pq.Int64Array `gorm:"column:tournament_scored_ids;type:bigint[];default:'{}'"`
	// TournamentPoints — точное значение начисленных турнирных очков (C-M2):
	// RemoveGame списывает именно его, а не пересчитывает по текущему месту.
	TournamentPoints int `gorm:"column:tournament_points;default:0"`
	// StartTime — C-3 (pass 45): индивидуальное время старта команды (NULL = общее).
	StartTime      *time.Time
	Game           Game                        `gorm:"foreignKey:GameID"`
	Team           team.Team                   `gorm:"foreignKey:TeamID"`
	Progresses     []LevelProgress             `gorm:"foreignKey:GamePassingID"`
	Logs           []Log                       `gorm:"foreignKey:GamePassingID"`
	VotingSessions []GameBlackboxVotingSession `gorm:"foreignKey:GamePassingID"`
}

// GamePassingLevel — C-1/C-2 (pass 45): маршрут команды (порядок уровней).
type GamePassingLevel struct {
	ID            uint `gorm:"primaryKey"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	GamePassingID uint        `gorm:"not null;uniqueIndex:idx_gpl_passing_level"`
	LevelID       uint        `gorm:"not null;uniqueIndex:idx_gpl_passing_level"`
	OrderIndex    int         `gorm:"not null;default:0"`
	GamePassing   GamePassing `gorm:"foreignKey:GamePassingID"`
	Level         level.Level `gorm:"foreignKey:LevelID"`
}

func (GamePassingLevel) TableName() string { return "game_passing_levels" }

// LevelTeamAnswer — C-4 (pass 45): персональный ответ уровня для команды.
type LevelTeamAnswer struct {
	ID        uint `gorm:"primaryKey"`
	CreatedAt time.Time
	UpdatedAt time.Time
	LevelID   uint   `gorm:"not null;uniqueIndex:idx_lta_level_team"`
	TeamID    uint   `gorm:"not null;uniqueIndex:idx_lta_level_team"`
	Code      string `gorm:"not null"`
	Hint      string `gorm:"default:''"`
}

func (LevelTeamAnswer) TableName() string { return "level_team_answers" }

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
	// UserID — C-5 (pass 45): кто из команды отправил (для итогов «на человека»).
	UserID        *uint `gorm:"index:idx_attempts_user"`
	Code          string
	FilePath      string
	IsFile        bool
	Success       bool
	LevelProgress LevelProgress `gorm:"foreignKey:LevelProgressID"`
	User          user.User     `gorm:"foreignKey:UserID"`
}

// PlayerLocation — G-1..G-4 (pass 45): последняя известная позиция игрока
// (водителя) во время игры. Одна запись на (game_passing, user).
type PlayerLocation struct {
	ID            uint `gorm:"primaryKey"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	GamePassingID uint    `gorm:"not null;uniqueIndex:idx_player_loc_passing_user"`
	TeamID        uint    `gorm:"not null;index:idx_player_loc_team"`
	UserID        uint    `gorm:"not null;uniqueIndex:idx_player_loc_passing_user"`
	Latitude      float64 `gorm:"not null"`
	Longitude     float64 `gorm:"not null"`
	// Accuracy — точность определения (метры), для пометки маркера.
	Accuracy float64 `gorm:"default:0"`
}

func (PlayerLocation) TableName() string { return "player_locations" }

// SubmitResult содержит результат успешной отправки кода/файла.
// GameID заполняется после транзакции для вызова CalculateResults.
type SubmitResult struct {
	Attempt *Attempt
	GameID  uint
}

type CoAuthor struct {
	gorm.Model
	GameID uint   `gorm:"not null;uniqueIndex:idx_game_user"`
	UserID uint   `gorm:"not null;uniqueIndex:idx_game_user"`
	Role   string `gorm:"default:'content_editor'"`
	// Permissions — A-1 (pass 45): выборочные права соавтора (jsonb).
	// Набор строк из Perm*; Role остаётся пресетом для совместимости.
	Permissions PermissionSlice `gorm:"type:jsonb;default:'[\"read\"]'"`
	Game        Game            `gorm:"foreignKey:GameID"`
	User        user.User       `gorm:"foreignKey:UserID"`
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
	// P-5 (pass 39): денормализованный game_id — GetLogsByGameID и
	// GetLogsByGameIDPaginated фильтруют по нему без JOIN game_passings,
	// сортировка по (game_id, created_at DESC) покрывается индексом.
	GameID      uint `gorm:"not null;index:idx_logs_game_created,priority:1"`
	LevelID     uint `gorm:"index:idx_logs_level"`
	Message     string
	GamePassing GamePassing `gorm:"foreignKey:GamePassingID"`
	Level       level.Level `gorm:"foreignKey:LevelID"`
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
