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
	"gengine-0/internal/domain/payment"
	"gengine-0/internal/domain/social"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/tournament"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/assets/fonts"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/middleware"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"

	"gorm.io/gorm"
)

func wrapGamePlayService(db *gorm.DB, gameRepo game.GameRepository, passingRepo game.GamePassingRepository, attemptSvc *game.AttemptService, monitorSvc *game.MonitorService, hub *ws.RoomHub, coAuthorSvc *game.CoAuthorService, sseMgr *game.SSEManager, appCache cache.CacheStore) *game.GamePlayService {
	// DEEP-REVIEW P5 (pass 46): кэш настроек игры.
	return game.NewGamePlayService(db, attemptSvc, monitorSvc, hub, coAuthorSvc).WithRepository(gameRepo).WithPassingRepository(passingRepo).WithSSEManager(sseMgr).WithCache(appCache)
}

func wrapGameAdminService(db *gorm.DB, teamRepo team.TeamRepository, userRepo user.UserRepository, coAuthorSvc *game.CoAuthorService, cfg *config.Config, sseMgr *game.SSEManager) *game.GameAdminService {
	return game.NewGameAdminService(db, coAuthorSvc, cfg).WithRepositories(teamRepo, userRepo).WithSSEManager(sseMgr)
}

func wrapGameplayHandler(gameService game.GameServiceInterface, gamePlaySvc game.GamePlayServiceInterface, progressSvc *game.LevelProgressService, monitorSvc *game.MonitorService, hub *ws.RoomHub, store storage.FileStorage) *game.GameplayHandler {
	return game.NewGameplayHandler(gameService, gamePlaySvc, progressSvc, monitorSvc, hub, store)
}

func wrapGameService(gameRepo game.GameRepository, passingRepo game.GamePassingRepository, ca *game.CoAuthorService, rs *game.ReviewService, ms *game.MonitorService, ps *game.PhotoService, hub *ws.RoomHub, cfg *config.Config, storage storage.FileStorage, cacheStore cache.CacheStore, userRepo user.UserRepository, ratingSvc *game.RatingService) *game.GameService {
	return game.NewGameService(gameRepo, passingRepo, ca, rs, ms, ps, hub, cfg, storage, cacheStore, userRepo, ratingSvc)
}

// wrapCoAuthorService — соавторы: нетранзакционные методы через репозиторий
// (A-1, pass 32 — устранено дублирование запросов между сервисом и репозиторием).
// A-1 (pass 39): db не нужен — сервис stateless (все операции через repo/tx).
// P0-3 (pass 45): userRepo — супер-админ bypass прав.
// DEEP-REVIEW PASS-3 M9: общий rolecache с middleware — единая инвалидация.
func wrapCoAuthorService(coAuthRepo game.CoAuthorRepository, userRepo user.UserRepository) *game.CoAuthorService {
	return game.NewCoAuthorService().
		WithRepository(coAuthRepo).
		WithUserRepository(userRepo).
		WithRoleCache(middleware.RoleCache)
}

func wrapReviewService(reviewRepo game.ReviewRepository, cacheStore cache.CacheStore) *game.ReviewService {
	return game.NewReviewService(reviewRepo).WithCache(cacheStore)
}

// wrapNoteService — заметки через репозиторий (A-2, pass 31).
func wrapNoteService(noteRepo game.NoteRepository, ca *game.CoAuthorService) *game.NoteService {
	return game.NewNoteService(noteRepo, ca)
}

// wrapPhotoService — фото через репозиторий (A-2, pass 31).
func wrapPhotoService(photoRepo game.PhotoRepository, coAuthRepo game.CoAuthorRepository) *game.PhotoService {
	return game.NewPhotoService(photoRepo, coAuthRepo)
}

// wrapRatingService — рейтинг: read-пути через репозиторий (A-2, pass 31).
func wrapRatingService(db *gorm.DB, ratingRepo game.RatingRepository, cacheStore cache.CacheStore) *game.RatingService {
	return game.NewRatingService(db, cacheStore).WithRepository(ratingRepo)
}

// wrapMonitorService — мониторинг: read-пути через репозиторий (A-2, pass 31).
func wrapMonitorService(db *gorm.DB, monitorRepo game.MonitorRepository) *game.MonitorService {
	return game.NewMonitorService(db).WithRepository(monitorRepo)
}

func wrapLevelProgressService(db *gorm.DB, progressRepo game.LevelProgressRepository, sseMgr *game.SSEManager, gameService *game.GameService) *game.LevelProgressService {
	return game.NewLevelProgressService(db).WithRepository(progressRepo).WithSSEManager(sseMgr).WithGameService(gameService)
}

// wrapGamePassingService собирает GamePassingService с method-chaining
// (D4): раньше создавался вручную в router.go и не был в DI-манифесте.
func wrapGamePassingService(db *gorm.DB, passingRepo game.GamePassingRepository, ts *team.TeamService, ca *game.CoAuthorService, progressSvc *game.LevelProgressService, hub *ws.RoomHub, monitorSvc *game.MonitorService, sseMgr *game.SSEManager) *game.GamePassingService {
	return game.NewGamePassingService(db, ts, ca, progressSvc).
		WithRepository(passingRepo).
		WithHub(hub).
		WithMonitorService(monitorSvc).
		WithSSEManager(sseMgr)
}

// wrapTwoFactorService возвращает сервис 2FA (stateless, без зависимостей).
func wrapTwoFactorService() *user.TwoFactorService {
	return user.NewTwoFactorService()
}

func wrapTournamentService(db *gorm.DB, tournamentRepo tournament.TournamentRepository, tournamentGameRepo tournament.TournamentGameRepository, tournamentTeamRepo tournament.TournamentTeamRepository, tournamentResultRepo tournament.TournamentResultRepository, teamService *team.TeamService, cfg *config.Config, cacheStore cache.CacheStore) *tournament.TournamentService {
	return tournament.NewTournamentService(db, tournamentRepo, tournamentGameRepo, tournamentTeamRepo, tournamentResultRepo, teamService, cfg).WithCache(cacheStore)
}

func wrapTeamService(teamRepo team.TeamRepository, userRepo user.UserRepository, chatRepo monitor.ChatRepository) *team.TeamService {
	return team.NewTeamService(teamRepo).WithUserRepository(userRepo).WithPermCacheInvalidator(chatRepo)
}

func wrapInvitationService(invRepo team.InvitationRepository, teamRepo team.TeamRepository, cfg *config.Config) *team.InvitationService {
	return team.NewInvitationService(invRepo, teamRepo, cfg)
}

func wrapLevelService(levelRepo level.LevelRepository, questionRepo level.QuestionRepository, answerRepo level.AnswerRepository, coAuthorSvc *game.CoAuthorService, gameAdminSvc *game.GameAdminService) *level.LevelService {
	return level.NewLevelService(levelRepo, questionRepo, answerRepo, coAuthorSvc, gameAdminSvc)
}

// wrapImportService — F-1 (pass 45): импорт уровней из JSON.
func wrapImportService(db *gorm.DB, coAuthorSvc *game.CoAuthorService) *level.ImportService {
	return level.NewImportService(db, coAuthorSvc)
}

// wrapGeolocationService — G-1..G-4 (pass 45): позиции игроков.
func wrapGeolocationService(geoRepo game.GeolocationRepository) *game.GeolocationService {
	return game.NewGeolocationService(geoRepo)
}

// wrapGeolocationHandler — G-2/G-3 (pass 45): API геолокации.
func wrapGeolocationHandler(geoService *game.GeolocationService, passingRepo game.GamePassingRepository, gameRepo game.GameRepository) *game.GeolocationHandler {
	return game.NewGeolocationHandler(geoService, passingRepo, gameRepo)
}

// wrapPaymentService — G-1..G-3 (pass 45): платежи ЮKassa.
// IDEA-7: внедряем notificationService для уведомления о подтверждении платежа.
func wrapPaymentService(cfg *config.Config, paymentRepo payment.PaymentRepository, notificationSvc *notification.NotificationService) *payment.PaymentService {
	return payment.NewPaymentService(cfg.Payments, paymentRepo).WithNotificationService(notificationSvc)
}

// wrapPaymentHandler — G-1..G-3 (pass 45): HTTP-обработчики платежей.
func wrapPaymentHandler(svc *payment.PaymentService) *payment.PaymentHandler {
	return payment.NewPaymentHandler(svc)
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

func wrapAuthService(userRepo user.UserRepository, achievRepo user.AchievementRepository, emailVerifRepo user.EmailVerificationRepository, refreshTokenRepo user.RefreshTokenRepository, cfg *config.Config, cacheStore cache.CacheStore, emailVerifSvc *user.EmailVerificationService) *user.AuthService {
	// D2: RefreshTokenService подключается к AuthService.
	// accessGen = AuthService (реализует AccessTokenGenerator через GenerateJWT).
	return user.NewAuthService(userRepo, achievRepo, emailVerifRepo, refreshTokenRepo, cfg).WithCache(cacheStore).WithEmailVerificationService(emailVerifSvc)
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

func wrapBlackboxVoteService(blackboxRepo monitor.BlackboxRepository, coAuthorSvc *game.CoAuthorService, db *gorm.DB, cfg *config.Config) *monitor.BlackboxVoteService {
	return monitor.NewBlackboxVoteService(blackboxRepo, coAuthorSvc, db, cfg)
}

func wrapBackupService(backupRepo admin.BackupRepository, cfg *config.Config) *admin.BackupService {
	return admin.NewBackupService(backupRepo, "backups", cfg.Server.MaxBackups, cfg.Database, cfg.Server.BackupEncryptionKey)
}

func wrapCalendarHandler(gameRepo game.GameRepository, cfg *config.Config) *calendar.CalendarHandler {
	return calendar.NewCalendarHandler(gameRepo).WithBaseURL(cfg.Server.BaseURL)
}

func wrapPushHandler(pushRepo user.PushSubscriptionRepository, cfg *config.Config) *user.PushHandler {
	return user.NewPushHandler(pushRepo, cfg.VAPID)
}

// wrapProfileService — профиль через ProfileRepository (A-2, pass 35:
// раньше был чистый *gorm.DB без репозитория).
func wrapProfileService(db *gorm.DB) *user.ProfileService {
	return user.NewProfileService(user.NewGormProfileRepo(db))
}

// wrapUserDashboardService — дашборд пользователя (M16, pass 30: раньше
// создавался локально в routes с ручным NewGormUserRepo, что обходило DI).
func wrapUserDashboardService(userRepo user.UserRepository) *user.UserDashboardService {
	return user.NewUserDashboardService(userRepo)
}

// wrapSimulateService — симуляция прохождения (M17, pass 30: раньше
// создавался локально в game/routes.go, не входил в DI-граф).
func wrapSimulateService(gameRepo game.GameRepository, coAuthorSvc *game.CoAuthorService) *game.SimulateService {
	return game.NewSimulateService(gameRepo, coAuthorSvc)
}
