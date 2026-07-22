// internal/domain/game/handler_interfaces.go
package game

import (
	"context"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
)

type GameServiceInterface interface {
	GetByID(ctx context.Context, id, userID uint) (*Game, error)
	CreateGameWithCover(ctx context.Context, dto *CreateGameDTO, authorID uint) (*Game, error)
	UpdateGameWithCover(ctx context.Context, gameID uint, dto *UpdateGameDTO, userID uint) error
	ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error)
	Delete(ctx context.Context, id uint, userID uint) error
	Publish(ctx context.Context, id uint, userID uint) error
	ListReviews(ctx context.Context, gameID uint) ([]Review, error)
	GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error)
	IsUserManager(ctx context.Context, gameID, userID uint) (bool, error)
	GetSettingsWithDefaults(ctx context.Context, gameID uint) (*GameSetting, error)
	SaveSettings(ctx context.Context, gameID uint, settings GameSetting) (*GameSetting, error)
}

type CoAuthorServiceInterface interface {
	IsUserManager(ctx context.Context, gameID, userID uint) (bool, error)
	HasPermission(ctx context.Context, gameID, userID uint, requiredRole string) (bool, error)
	CanModerateGame(ctx context.Context, gameID, userID uint) (bool, error)
	CanEditContent(ctx context.Context, gameID, userID uint) (bool, error)
	Add(gameID, newCoAuthorID, ownerID uint) error
	Remove(gameID, coAuthorUserID, ownerID uint) error
	List(gameID uint) ([]CoAuthor, error)
}

type AuditServiceInterface interface {
	Log(userID uint, action, objectType string, objectID uint, details string)
}

type GamePassingServiceInterface interface {
	Apply(ctx context.Context, gameID, teamID, userID uint) error
	ListByGame(ctx context.Context, gameID uint) ([]GamePassing, error)
	ListTestPassings(ctx context.Context, gameID uint, result *[]GamePassing) error
	UpdateStatus(ctx context.Context, passingID uint, status GamePassingStatus, userID uint) error
	StartGame(ctx context.Context, passingID, userID uint) error
	GetTeamsByCaptain(ctx context.Context, userID uint) ([]team.Team, error)
}

type GamePlayServiceInterface interface {
	SubmitCode(ctx context.Context, passingID, userID uint, code string) (*SubmitResult, error)
	SubmitFile(ctx context.Context, passingID, userID uint, filePath string) (*Attempt, error)
	UseHint(ctx context.Context, passingID, userID uint) (string, error)
	AcceptBlackboxAnswer(ctx context.Context, passingID, userID uint) error
	StartTesting(ctx context.Context, gameID, userID uint) (*GamePassing, error)
	SubmitTestCode(ctx context.Context, passingID, userID uint, code string) (*Attempt, error)
	SkipLevelTest(ctx context.Context, passingID, userID uint) error
	GetGameplayData(ctx context.Context, passingID uint) (*GameplayData, error)
	GetPassingWithGame(ctx context.Context, passingID uint) (*GamePassing, error)
	IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error)
}

type GameplayData struct {
	Passing      GamePassing
	Level        level.Level
	Settings     GameSetting
	Attempts     []Attempt
	VotingActive bool
	TimeLimitSec int
}

type GameAdminServiceInterface interface {
	ForceFinishGame(ctx context.Context, gameID, userID uint) error
	DisqualifyTeam(ctx context.Context, gameID, teamID, userID uint) error
	DeleteLevelFromActiveGame(ctx context.Context, gameID, levelID, userID uint) error
}
