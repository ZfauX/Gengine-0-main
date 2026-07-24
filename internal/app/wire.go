//go:build wireinject
// +build wireinject

package app

import (
	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/level"
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
		level.NewGormLevelRepo,
		level.NewGormQuestionRepo,
		level.NewGormAnswerRepo,
		team.NewGormTeamRepo,
		team.NewGormInvitationRepo,
		tournament.NewGormTournamentRepo,
		tournament.NewGormTournamentGameRepo,
		tournament.NewGormTournamentTeamRepo,
		tournament.NewGormTournamentResultRepo,
	)
	return nil
}

func initializeServices(db *gorm.DB, repos *repositories, cfg *config.Config, hub *ws.RoomHub, localStorage storage.FileStorage, appCache cache.CacheStore) *services {
	wire.Build(
		wire.Struct(new(services), "*"),
		wire.FieldsOf(new(*repositories),
			"User", "Achiev", "PassReset", "EmailVerif", "ExtLogin", "RefreshToken",
			"Game", "GamePassing",
			"Level", "Question", "Answer",
			"Team", "Invitation",
			"Tournament", "TournGame", "TournTeam", "TournResult",
		),
		game.NewCoAuthorService,
		game.NewReviewService,
		game.NewAttemptService,
		game.NewMonitorService,
		game.NewPhotoService,
		game.NewRatingService,
		game.NewSSEManager,
		wrapGameService,
		wrapGamePlayService,
		wrapGameAdminService,
		wrapLevelProgressService,
		wrapTournamentService,
		wrapTeamService,
		wrapInvitationService,
		wrapLevelService,
		wrapQuestionService,
		wrapAnswerService,
		wrapAuthService,
		wrapUserService,
		wrapAchievementService,
		wrapOAuthService,
		wrapPasswordResetService,
		wrapEmailVerificationService,
		wrapEmailService,
		wrapGameplayHandler,
		wire.Bind(new(game.GameServiceInterface), new(*game.GameService)),
		wire.Bind(new(game.GamePlayServiceInterface), new(*game.GamePlayService)),
	)
	return nil
}
