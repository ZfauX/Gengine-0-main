//go:build wireinject
// +build wireinject

package app

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/admin"
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
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"github.com/google/wire"
	"gorm.io/gorm"
)

func initializeRepositories(db *gorm.DB) *repositories {
	wire.Build(
		wire.Struct(new(repositories), "*"),
		user.NewGormUserRepo,
		user.NewGormAchievementRepo,
		user.NewGormPasswordResetRepo,
		user.NewGormEmailVerificationRepo,
		user.NewGormExternalLoginRepo,
		user.NewGormRefreshTokenRepo,
		user.NewGormWebAuthnRepo,
		game.NewGormGameRepo,
		game.NewGormGamePassingRepo,
		game.NewGormLevelProgressRepo,
		game.NewGormNoteRepo,
		game.NewGormPhotoRepo,
		game.NewGormReviewRepo,
		game.NewGormRatingRepo,
		game.NewGormCoAuthorRepo,
		game.NewGormMonitorRepo,
		game.NewGormGeolocationRepo,
		level.NewGormLevelRepo,
		level.NewGormQuestionRepo,
		level.NewGormAnswerRepo,
		team.NewGormTeamRepo,
		team.NewGormInvitationRepo,
		tournament.NewGormTournamentRepo,
		tournament.NewGormTournamentGameRepo,
		tournament.NewGormTournamentTeamRepo,
		tournament.NewGormTournamentResultRepo,
		user.NewGormPushSubscriptionRepo,
		social.NewGormFollowRepo,
		export.NewGormExportRepo,
		monitor.NewGormChatRepo,
		monitor.NewGormBlackboxRepo,
		notification.NewNotificationRepository,
		admin.NewGormBackupRepo,
	)
	return nil
}

func initializeServices(db *gorm.DB, repos *repositories, cfg *config.Config, hub *ws.RoomHub, localStorage storage.FileStorage, appCache cache.CacheStore) (*services, error) {
	wire.Build(
		wire.Struct(new(services), "*"),
		wire.FieldsOf(new(*repositories),
			"User", "Achiev", "PassReset", "EmailVerif", "ExtLogin", "RefreshToken",
			"Game", "GamePassing", "LevelProgress", "Note", "Photo", "Review", "Rating", "CoAuthor", "Monitor",
			"Level", "Question", "Answer",
			"Team", "Invitation",
			"Tournament", "TournGame", "TournTeam", "TournResult",
			"PushSub", "Follow", "Export", "Chat", "Blackbox", "Notification", "Backup", "Geolocation",
		),
		wrapCoAuthorService,
		wrapReviewService,
		wrapNoteService,
		wrapPhotoService,
		wrapRatingService,
		wrapMonitorService,
		game.NewAttemptService,
		game.NewSSEManager,
		wrapGameService,
		wrapGamePlayService,
		wrapGameAdminService,
		wrapGamePassingService,
		wrapTwoFactorService,
		wrapLevelProgressService,
		wrapTournamentService,
		wrapTeamService,
		wrapInvitationService,
		wrapLevelService,
		wrapQuestionService,
		wrapAnswerService,
		wrapImportService,
		wrapGeolocationService,
		wrapGeolocationHandler,
		wrapAuthService,
		wrapRefreshTokenService,
		wrapUserService,
		wrapAchievementService,
		wrapOAuthService,
		wrapPasswordResetService,
		wrapEmailVerificationService,
		wrapEmailService,
		wrapGameplayHandler,
		wrapNotificationService,
		wrapExportService,
		wrapFollowService,
		wrapChatService,
		wrapBlackboxVoteService,
		wrapBackupService,
		wrapCalendarHandler,
		wrapPushHandler,
		wrapProfileService,
		wrapUserDashboardService,
		wrapSimulateService,
		wire.Bind(new(game.GameServiceInterface), new(*game.GameService)),
		wire.Bind(new(game.GamePlayServiceInterface), new(*game.GamePlayService)),
	)
	return nil, nil
}
