// internal/app/init.go
package app

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/admin"
	"gengine-0/internal/domain/calendar"
	"gengine-0/internal/domain/export"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/monitor"
	"gengine-0/internal/domain/notification"
	"gengine-0/internal/domain/social"
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
	WebAuthn     user.WebAuthnRepository
	PushSub      user.PushSubscriptionRepository
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
	Follow       social.FollowRepository
	Export       export.ExportRepository
	Chat         monitor.ChatRepository
	Blackbox     monitor.BlackboxRepository
	Notification notification.NotificationRepository
	Backup       admin.BackupRepository
}

func initRepositories(db *gorm.DB) *repositories {
	return initializeRepositories(db)
}

type services struct {
	Auth            *user.AuthService
	RefreshToken    *user.RefreshTokenService
	User            *user.UserService
	Achiev          *user.AchievementService
	OAuth           *user.OAuthService
	PasswordReset   *user.PasswordResetService
	EmailVerif      *user.EmailVerificationService
	Email           *email.EmailService
	TwoFactor       *user.TwoFactorService
	Game            *game.GameService
	GamePlay        *game.GamePlayService
	GameAdmin       *game.GameAdminService
	GamePassing     *game.GamePassingService
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
	Notification    *notification.NotificationService
	Export          *export.ExportService
	Follow          *social.FollowService
	Chat            *monitor.ChatService
	BlackboxVote    *monitor.BlackboxVoteService
	Backup          *admin.BackupService
	CalendarHandler *calendar.CalendarHandler
	PushHandler     *user.PushHandler
	Profile         *user.ProfileService
}

func initServices(db *gorm.DB, repos *repositories, cfg *config.Config, hub *ws.RoomHub, localStorage storage.FileStorage, appCache cache.CacheStore) (*services, error) {
	return initializeServices(db, repos, cfg, hub, localStorage, appCache)
}
