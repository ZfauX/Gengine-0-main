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
	"gengine-0/internal/pkg/assets/fonts"
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

func wrapReviewService(db *gorm.DB, cacheStore cache.CacheStore) *game.ReviewService {
	return game.NewReviewService(db).WithCache(cacheStore)
}

func wrapLevelProgressService(db *gorm.DB, sseMgr *game.SSEManager, gameService *game.GameService) *game.LevelProgressService {
	return game.NewLevelProgressService(db).WithSSEManager(sseMgr).WithGameService(gameService)
}

// wrapGamePassingService собирает GamePassingService с method-chaining
// (D4): раньше создавался вручную в router.go и не был в DI-манифесте.
func wrapGamePassingService(db *gorm.DB, ts *team.TeamService, ca *game.CoAuthorService, progressSvc *game.LevelProgressService, hub *ws.RoomHub, monitorSvc *game.MonitorService, sseMgr *game.SSEManager) *game.GamePassingService {
	return game.NewGamePassingService(db, ts, ca, progressSvc).
		WithHub(hub).
		WithMonitorService(monitorSvc).
		WithSSEManager(sseMgr)
}

// wrapTwoFactorService возвращает сервис 2FA (stateless, без зависимостей).
func wrapTwoFactorService() *user.TwoFactorService {
	return user.NewTwoFactorService()
}

func wrapTournamentService(db *gorm.DB, tournamentRepo tournament.TournamentRepository, tournamentGameRepo tournament.TournamentGameRepository, tournamentTeamRepo tournament.TournamentTeamRepository, tournamentResultRepo tournament.TournamentResultRepository, teamService *team.TeamService, cfg *config.Config) *tournament.TournamentService {
	return tournament.NewTournamentService(db, tournamentRepo, tournamentGameRepo, tournamentTeamRepo, tournamentResultRepo, teamService, cfg)
}

func wrapTeamService(teamRepo team.TeamRepository) *team.TeamService {
	return team.NewTeamService(teamRepo)
}

func wrapInvitationService(invRepo team.InvitationRepository, teamRepo team.TeamRepository, cfg *config.Config) *team.InvitationService {
	return team.NewInvitationService(invRepo, teamRepo, cfg)
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

func wrapAuthService(userRepo user.UserRepository, achievRepo user.AchievementRepository, emailVerifRepo user.EmailVerificationRepository, refreshTokenRepo user.RefreshTokenRepository, cfg *config.Config, cacheStore cache.CacheStore) *user.AuthService {
	// D2: RefreshTokenService создаётся и связывается с AuthService.
	// accessGen = AuthService (реализует AccessTokenGenerator через GenerateJWT).
	return user.NewAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg).WithCache(cacheStore)
}

// wrapRefreshTokenService собирает RefreshTokenService (D2) и связывает его
// с AuthService через WithRefreshService (side-effect: authSvc.refreshSvc
// заполняется). accessGen — AuthService, реализующий AccessTokenGenerator.
func wrapRefreshTokenService(refreshTokenRepo user.RefreshTokenRepository, userRepo user.UserRepository, cfg *config.Config, authSvc *user.AuthService) *user.RefreshTokenService {
	refreshSvc := user.NewRefreshTokenService(refreshTokenRepo, userRepo, cfg, authSvc)
	authSvc.WithRefreshService(refreshSvc)
	return refreshSvc
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

// ---------- H2 (pass 29): сервисы, ранее создававшиеся вручную в routes ----------

func wrapNotificationService(repo notification.NotificationRepository, hub *ws.RoomHub, sseMgr *game.SSEManager, cfg *config.Config) *notification.NotificationService {
	return notification.NewNotificationService(repo, hub).
		WithSSEManager(sseMgr).
		WithVAPID(cfg.VAPID, cfg.Server.BaseURL)
}

func wrapExportService(exportRepo export.ExportRepository, db *gorm.DB) (*export.ExportService, error) {
	return export.NewExportService(exportRepo, db, fonts.DejaVuSans, fonts.DejaVuSansBold)
}

func wrapFollowService(followRepo social.FollowRepository) *social.FollowService {
	return social.NewFollowService(followRepo)
}

func wrapChatService(chatRepo monitor.ChatRepository) *monitor.ChatService {
	return monitor.NewChatService(chatRepo)
}

func wrapBlackboxVoteService(blackboxRepo monitor.BlackboxRepository, gameRepo game.GameRepository, db *gorm.DB, cfg *config.Config) *monitor.BlackboxVoteService {
	return monitor.NewBlackboxVoteService(blackboxRepo, gameRepo, db, cfg)
}

func wrapBackupService(backupRepo admin.BackupRepository, cfg *config.Config) *admin.BackupService {
	return admin.NewBackupService(backupRepo, "backups", cfg.Server.MaxBackups, cfg.Database)
}

func wrapCalendarHandler(gameRepo game.GameRepository, cfg *config.Config) *calendar.CalendarHandler {
	return calendar.NewCalendarHandler(gameRepo).WithBaseURL(cfg.Server.BaseURL)
}

func wrapPushHandler(pushRepo user.PushSubscriptionRepository, cfg *config.Config) *user.PushHandler {
	return user.NewPushHandler(pushRepo, cfg.VAPID)
}

func wrapProfileService(db *gorm.DB) *user.ProfileService {
	return user.NewProfileService(db)
}

// wrapUserDashboardService — дашборд пользователя (M16, pass 30: раньше
// создавался локально в routes с ручным NewGormUserRepo, что обходило DI).
func wrapUserDashboardService(userRepo user.UserRepository) *user.UserDashboardService {
	return user.NewUserDashboardService(userRepo)
}

// wrapSimulateService — симуляция прохождения (M17, pass 30: раньше
// создавался локально в game/routes.go, не входил в DI-граф).
func wrapSimulateService(db *gorm.DB, coAuthorSvc *game.CoAuthorService) *game.SimulateService {
	return game.NewSimulateService(db, coAuthorSvc)
}
