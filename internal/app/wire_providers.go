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

func wrapGamePlayService(db *gorm.DB, attemptSvc *game.AttemptService, progressSvc *game.LevelProgressService, monitorSvc *game.MonitorService, hub *ws.RoomHub, coAuthorSvc *game.CoAuthorService, cfg *config.Config, sseMgr *game.SSEManager) *game.GamePlayService {
	return game.NewGamePlayService(db, attemptSvc, progressSvc, monitorSvc, hub, coAuthorSvc, cfg).WithSSEManager(sseMgr)
}

func wrapGameAdminService(db *gorm.DB, coAuthorSvc *game.CoAuthorService, cfg *config.Config, sseMgr *game.SSEManager) *game.GameAdminService {
	return game.NewGameAdminService(db, coAuthorSvc, cfg).WithSSEManager(sseMgr)
}

func wrapGameplayHandler(gameService game.GameServiceInterface, gamePlaySvc game.GamePlayServiceInterface, attemptSvc *game.AttemptService, progressSvc *game.LevelProgressService, monitorSvc *game.MonitorService, hub *ws.RoomHub, store storage.FileStorage) *game.GameplayHandler {
	return game.NewGameplayHandler(gameService, gamePlaySvc, attemptSvc, progressSvc, monitorSvc, hub, store)
}

func wrapGameService(db *gorm.DB, gameRepo game.GameRepository, passingRepo game.GamePassingRepository, ca *game.CoAuthorService, rs *game.ReviewService, ms *game.MonitorService, ps *game.PhotoService, hub *ws.RoomHub, cfg *config.Config, storage storage.FileStorage, cacheStore cache.CacheStore, userRepo user.UserRepository, ratingSvc *game.RatingService) *game.GameService {
	return game.NewGameService(db, gameRepo, passingRepo, ca, rs, ms, ps, hub, cfg, storage, cacheStore, userRepo, ratingSvc)
}

func wrapLevelProgressService(db *gorm.DB, sseMgr *game.SSEManager, gameService *game.GameService) *game.LevelProgressService {
	return game.NewLevelProgressService(db).WithSSEManager(sseMgr).WithGameService(gameService)
}

func wrapTournamentService(tournamentRepo tournament.TournamentRepository, tournamentGameRepo tournament.TournamentGameRepository, tournamentTeamRepo tournament.TournamentTeamRepository, tournamentResultRepo tournament.TournamentResultRepository, teamService *team.TeamService, cfg *config.Config) *tournament.TournamentService {
	return tournament.NewTournamentService(tournamentRepo, tournamentGameRepo, tournamentTeamRepo, tournamentResultRepo, teamService, cfg)
}

func wrapTeamService(teamRepo team.TeamRepository, coAuthorSvc *game.CoAuthorService) *team.TeamService {
	return team.NewTeamService(teamRepo, coAuthorSvc)
}

func wrapInvitationService(invRepo team.InvitationRepository, teamRepo team.TeamRepository, coAuthorSvc *game.CoAuthorService, cfg *config.Config) *team.InvitationService {
	return team.NewInvitationService(invRepo, teamRepo, coAuthorSvc, cfg)
}

func wrapLevelService(levelRepo level.LevelRepository, questionRepo level.QuestionRepository, answerRepo level.AnswerRepository, coAuthorSvc *game.CoAuthorService, gameAdminSvc *game.GameAdminService) *level.LevelService {
	return level.NewLevelService(levelRepo, questionRepo, answerRepo, coAuthorSvc, gameAdminSvc)
}

func wrapQuestionService(questionRepo level.QuestionRepository, levelRepo level.LevelRepository, coAuthorSvc *game.CoAuthorService) *level.QuestionService {
	return level.NewQuestionService(questionRepo, levelRepo, coAuthorSvc)
}

func wrapAnswerService(answerRepo level.AnswerRepository, questionRepo level.QuestionRepository, levelRepo level.LevelRepository, coAuthorSvc *game.CoAuthorService) *level.AnswerService {
	return level.NewAnswerService(answerRepo, questionRepo, levelRepo, coAuthorSvc)
}

func wrapEmailService(cfg *config.Config, db *gorm.DB) *email.EmailService {
	return email.NewEmailService(cfg, db)
}

func wrapAuthService(userRepo user.UserRepository, achievRepo user.AchievementRepository, emailVerifRepo user.EmailVerificationRepository, refreshTokenRepo user.RefreshTokenRepository, cfg *config.Config) *user.AuthService {
	return user.NewAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg)
}

func wrapUserService(userRepo user.UserRepository) *user.UserService {
	return user.NewUserService(userRepo)
}

func wrapAchievementService(achievRepo user.AchievementRepository) *user.AchievementService {
	return user.NewAchievementService(achievRepo)
}

func wrapOAuthService(userRepo user.UserRepository, extLoginRepo user.ExternalLoginRepository, cfg *config.Config) *user.OAuthService {
	return user.NewOAuthService(userRepo, extLoginRepo, cfg)
}

func wrapPasswordResetService(userRepo user.UserRepository, passResetRepo user.PasswordResetRepository, cfg *config.Config) *user.PasswordResetService {
	return user.NewPasswordResetService(userRepo, passResetRepo, cfg)
}

func wrapEmailVerificationService(userRepo user.UserRepository, emailVerifRepo user.EmailVerificationRepository, cfg *config.Config) *user.EmailVerificationService {
	return user.NewEmailVerificationService(userRepo, emailVerifRepo, cfg)
}
