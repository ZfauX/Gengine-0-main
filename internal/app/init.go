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
	return &repositories{
		User:         user.NewGormUserRepo(db),
		Achiev:       user.NewGormAchievementRepo(db),
		PassReset:    user.NewGormPasswordResetRepo(db),
		EmailVerif:   user.NewGormEmailVerificationRepo(db),
		ExtLogin:     user.NewGormExternalLoginRepo(db),
		RefreshToken: user.NewGormRefreshTokenRepo(db),
		Game:         game.NewGormGameRepo(db),
		GamePassing:  game.NewGormGamePassingRepo(db),
		Level:        level.NewGormLevelRepo(db),
		Question:     level.NewGormQuestionRepo(db),
		Answer:       level.NewGormAnswerRepo(db),
		Team:         team.NewGormTeamRepo(db),
		Invitation:   team.NewGormInvitationRepo(db),
		Tournament:   tournament.NewGormTournamentRepo(db),
		TournGame:    tournament.NewGormTournamentGameRepo(db),
		TournTeam:    tournament.NewGormTournamentTeamRepo(db),
		TournResult:  tournament.NewGormTournamentResultRepo(db),
	}
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
	coAuthorSvc := game.NewCoAuthorService(db)
	reviewSvc := game.NewReviewService(db)
	attemptSvc := game.NewAttemptService(db)
	progressSvc := game.NewLevelProgressService(db)
	monitorSvc := game.NewMonitorService(db)
	ratingSvc := game.NewRatingService(db, appCache)
	sseMgr := game.NewSSEManager()

	authSvc := user.NewAuthService(
		repos.User,
		repos.Achiev,
		repos.EmailVerif,
		repos.RefreshToken,
		cfg,
	)
	userSvc := user.NewUserService(repos.User)
	achievSvc := user.NewAchievementService(repos.Achiev)
	oauthSvc := user.NewOAuthService(repos.User, repos.ExtLogin, cfg)
	passResetSvc := user.NewPasswordResetService(repos.User, repos.PassReset, cfg)
	emailVerifSvc := user.NewEmailVerificationService(repos.User, repos.EmailVerif, cfg)
	emailSvc := email.NewEmailService(cfg, db)

	photoSvc := game.NewPhotoService(db)

	gameSvc := game.NewGameService(
		db,
		repos.Game,
		repos.GamePassing,
		coAuthorSvc,
		reviewSvc,
		monitorSvc,
		photoSvc,
		hub,
		cfg,
		localStorage,
		appCache,
		repos.User,
		ratingSvc,
	)

	gamePlaySvc := game.NewGamePlayService(
		db,
		attemptSvc,
		progressSvc,
		monitorSvc,
		hub,
		coAuthorSvc,
		cfg,
	)

	gameAdminSvc := game.NewGameAdminService(
		db,
		coAuthorSvc,
		cfg,
	)

	levelSvc := level.NewLevelService(repos.Level, repos.Question, repos.Answer, coAuthorSvc, gameAdminSvc)
	questionSvc := level.NewQuestionService(repos.Question, repos.Level, coAuthorSvc)
	answerSvc := level.NewAnswerService(repos.Answer, repos.Question, repos.Level, coAuthorSvc)

	teamSvc := team.NewTeamService(repos.Team, coAuthorSvc)
	invitationSvc := team.NewInvitationService(repos.Invitation, repos.Team, coAuthorSvc, cfg)

	tournamentSvc := tournament.NewTournamentService(
		repos.Tournament,
		repos.TournGame,
		repos.TournTeam,
		repos.TournResult,
		teamSvc,
		cfg,
	)

	gameplayHandler := game.NewGameplayHandler(
		gameSvc,
		gamePlaySvc,
		attemptSvc,
		progressSvc,
		monitorSvc,
		hub,
		localStorage,
	)

	return &services{
		Auth: authSvc, User: userSvc, Achiev: achievSvc,
		OAuth: oauthSvc, PasswordReset: passResetSvc, EmailVerif: emailVerifSvc,
		Email: emailSvc, Game: gameSvc, GamePlay: gamePlaySvc,
		GameAdmin: gameAdminSvc, GameplayHandler: gameplayHandler,
		CoAuthor: coAuthorSvc, Review: reviewSvc, PhotoService: photoSvc,
		Attempt: attemptSvc, Progress: progressSvc, Monitor: monitorSvc,
		Rating: ratingSvc, Level: levelSvc, Question: questionSvc,
		Answer: answerSvc, Team: teamSvc, Invitation: invitationSvc,
		Tournament: tournamentSvc, SSEMgr: sseMgr,
	}
}
