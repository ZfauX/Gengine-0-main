// internal/app/init.go
package app

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/tournament"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"gorm.io/gorm"
)

//go:generate go run -mod=mod github.com/google/wire/cmd/wire

type repositories struct {
	User         user.UserRepository
	Achiev       user.AchievementRepository
	PassReset    user.PasswordResetRepository
	EmailVerif   user.EmailVerificationRepository
	ExtLogin     user.ExternalLoginRepository
	RefreshToken user.RefreshTokenRepository
	Game         game.GameRepository
	GamePassing  game.GamePassingRepository
	Level        level.LevelRepository
	Question     level.QuestionRepository
	Answer       level.AnswerRepository
	Team         team.TeamRepository
	Invitation   team.InvitationRepository
	Tournament   tournament.TournamentRepository
	TournGame    tournament.TournamentGameRepository
	TournTeam    tournament.TournamentTeamRepository
	TournResult  tournament.TournamentResultRepository
}

func initRepositories(db *gorm.DB) *repositories {
	return initializeRepositories(db)
}

type services struct {
	Auth            *user.AuthService
	User            *user.UserService
	Achiev          *user.AchievementService
	OAuth           *user.OAuthService
	PasswordReset   *user.PasswordResetService
	EmailVerif      *user.EmailVerificationService
	Email           *email.EmailService
	Game            *game.GameService
	GamePlay        *game.GamePlayService
	GameAdmin       *game.GameAdminService
	GameplayHandler *game.GameplayHandler
	CoAuthor        *game.CoAuthorService
	Review          *game.ReviewService
	PhotoService    *game.PhotoService
	Attempt         *game.AttemptService
	Progress        *game.LevelProgressService
	Monitor         *game.MonitorService
	Rating          *game.RatingService
	SSEMgr          *game.SSEManager
	Level           *level.LevelService
	Question        *level.QuestionService
	Answer          *level.AnswerService
	Team            *team.TeamService
	Invitation      *team.InvitationService
	Tournament      *tournament.TournamentService
}

func initServices(db *gorm.DB, repos *repositories, cfg *config.Config, hub *ws.RoomHub, localStorage storage.FileStorage, appCache cache.CacheStore) *services {
	return initializeServices(db, repos, cfg, hub, localStorage, appCache)
}
