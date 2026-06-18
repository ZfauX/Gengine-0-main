// internal/domain/tournament/model.go
package tournament

import (
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gorm.io/gorm"
)

type Tournament struct {
	gorm.Model
	Name                 string    `form:"name" binding:"required,min=2,max=200"`
	Description          string    `form:"description" binding:"max=5000"`
	AuthorID             uint      `gorm:"not null;index"`
	Author               user.User `gorm:"foreignKey:AuthorID"`
	PointsForFirst       int       `gorm:"default:10"`
	PointsForSecond      int       `gorm:"default:7"`
	PointsForThird       int       `gorm:"default:5"`
	PointsForParticipation int     `gorm:"default:2"`
	Games                []TournamentGame   `gorm:"foreignKey:TournamentID"`
	Teams                []TournamentTeam   `gorm:"foreignKey:TournamentID"`
	Results              []TournamentResult `gorm:"foreignKey:TournamentID"`
}

type TournamentGame struct {
	gorm.Model
	TournamentID uint       `gorm:"not null;uniqueIndex:idx_tournament_game"`
	GameID       uint       `gorm:"not null;uniqueIndex:idx_tournament_game"`
	OrderIndex   int        `gorm:"default:0"`
	Tournament   Tournament `gorm:"foreignKey:TournamentID"`
	Game         game.Game  `gorm:"foreignKey:GameID"`
}

type TournamentTeam struct {
	gorm.Model
	TournamentID uint       `gorm:"not null;uniqueIndex:idx_tournament_team"`
	TeamID       uint       `gorm:"not null;uniqueIndex:idx_tournament_team"`
	Tournament   Tournament `gorm:"foreignKey:TournamentID"`
	Team         team.Team  `gorm:"foreignKey:TeamID"`
}

type TournamentResult struct {
	gorm.Model
	TournamentID uint       `gorm:"not null;uniqueIndex:idx_tournament_team_result"`
	TeamID       uint       `gorm:"not null;uniqueIndex:idx_tournament_team_result"`
	Score        int        `gorm:"default:0"`
	GamesPlayed  int        `gorm:"default:0"`
	Tournament   Tournament `gorm:"foreignKey:TournamentID"`
	Team         team.Team  `gorm:"foreignKey:TeamID"`
}