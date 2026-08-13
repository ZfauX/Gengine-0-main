// internal/domain/game/handler_interfaces.go
package game

import (
	"context"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
)

type GameServiceInterface interface {
	GetByID(ctx context.Context, id, userID uint, isAdmin bool) (*Game, error)
	CreateGameWithCover(ctx context.Context, dto *CreateGameDTO, authorID uint) (*Game, error)
	UpdateGameWithCover(ctx context.Context, gameID uint, dto *UpdateGameDTO, userID uint, isAdmin bool) error
	ListFilteredPaginated(ctx context.Context, filter GameFilter, sort *GameSort, page, perPage int) ([]Game, int64, error)
	Delete(ctx context.Context, id uint, userID uint) error
	Publish(ctx context.Context, id uint, userID uint) error
	ListReviews(ctx context.Context, gameID uint) ([]Review, error)
	GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error)
	ShowGame(ctx context.Context, gameID, viewerID uint, isAdmin bool) (*Game, []Review, float64, int64, error)
	AutocompleteSearch(ctx context.Context, query string, limit int) ([]AutocompleteItem, error)
	IsUserManager(ctx context.Context, gameID, userID uint) (bool, error)
	GetSettingsWithDefaults(ctx context.Context, gameID uint) (*GameSetting, error)
	SaveSettings(ctx context.Context, gameID uint, settings GameSetting) (*GameSetting, error)
	GetUserGamesView(ctx context.Context, userID uint) string
	// CountGamesByAuthor (IDEA-13): количество игр автора — онбординг первой игры.
	CountGamesByAuthor(ctx context.Context, authorID uint) (int64, error)
}

type CoAuthorServiceInterface interface {
	IsUserManager(ctx context.Context, gameID, userID uint) (bool, error)
	HasPermission(ctx context.Context, gameID, userID uint, requiredRole string) (bool, error)
	CanModerateGame(ctx context.Context, gameID, userID uint) (bool, error)
	CanEditContent(ctx context.Context, gameID, userID uint) (bool, error)
	CanUploadMedia(ctx context.Context, gameID, userID uint) (bool, error)
	// A-1 (pass 45): Add принимает пресет роли и выборочные permissions.
	Add(ctx context.Context, gameID, newCoAuthorID, ownerID uint, role string, permissions []string) error
	Remove(ctx context.Context, gameID, coAuthorUserID, ownerID uint) error
	List(ctx context.Context, gameID uint) ([]CoAuthor, error)
	// P-5 (PASS-13): сброс кэша менеджерских прав после изменения состава авторов.
	InvalidateManagerCache(gameID uint)
}

type AuditServiceInterface interface {
	Log(userID uint, action, objectType string, objectID uint, details string)
}

type GamePassingServiceInterface interface {
	Apply(ctx context.Context, gameID, teamID, userID uint) error
	ListByGamePaginated(ctx context.Context, gameID uint, page, perPage int) ([]GamePassing, int64, error)
	ListTestPassings(ctx context.Context, gameID uint, result *[]GamePassing) error
	UpdateStatus(ctx context.Context, passingID uint, status GamePassingStatus, userID uint) error
	StartGame(ctx context.Context, passingID, userID uint) error
	GetTeamsByCaptain(ctx context.Context, userID uint) ([]team.Team, error)
	// Фаза 3 (C-1..C-5, pass 45). DEEP-REVIEW PASS-5 H3: gameID передаётся
	// для проверки принадлежности passing/level игре (cross-game IDOR).
	SetTeamRoute(ctx context.Context, gameID, passingID uint, levelIDs []uint) error
	// GetTeamRoute (HIGH #2, PASS-8): gameID для сверки passing.GameID — иначе
	// менеджер игры A читал маршрут команды игры B (cross-game утечка контента).
	GetTeamRoute(ctx context.Context, gameID, passingID uint) ([]GamePassingLevel, error)
	SetTeamStartTime(ctx context.Context, gameID, passingID uint, startTime *time.Time) error
	SetTeamAnswer(ctx context.Context, gameID, levelID, teamID uint, code, hint string) error
	GetTeamAnswer(ctx context.Context, levelID, teamID uint) (*LevelTeamAnswer, error)
	GetAttemptsPerUser(ctx context.Context, gameID uint) ([]AttemptPerUser, error)
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
