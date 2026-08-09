// internal/domain/monitor/service.go
package monitor

import (
	"context"
	"errors"
	"sort"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/i18n"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------- BlackboxVoteService ----------

// Sentinel-ошибки голосования (HIGH-4 / MED-4): хендлеры различают
// «нет прав» от остальных случаев через errors.Is, а не строки.
var (
	ErrNotTeamMember   = errors.New("вы не являетесь участником этой команды")
	ErrVotingClosed    = errors.New("голосование закрыто")
	ErrInvalidOption   = errors.New("недопустимый вариант ответа")
	ErrVoteAlreadyCast = errors.New("ваш голос уже учтён")
	ErrAccessDenied    = errors.New("доступ запрещён")
)

type BlackboxVoteService struct {
	blackboxRepo BlackboxRepository
	coAuthorSvc  *game.CoAuthorService
	db           *gorm.DB
	cfg          *config.Config
}

// NewBlackboxVoteService создаёт сервис голосования «чёрного ящика».
// A-1 (pass 35): вместо мёртвого gameRepo используется CoAuthorService —
// проверка прав автора/соавтора консистентна с game-доменом.
func NewBlackboxVoteService(
	blackboxRepo BlackboxRepository,
	coAuthorSvc *game.CoAuthorService,
	db *gorm.DB,
	cfg *config.Config,
) *BlackboxVoteService {
	return &BlackboxVoteService{
		blackboxRepo: blackboxRepo,
		coAuthorSvc:  coAuthorSvc,
		db:           db,
		cfg:          cfg,
	}
}

// StartVoting открывает новую сессию голосования и оповещает участников.
func (s *BlackboxVoteService) StartVoting(ctx context.Context, gamePassingID, levelID, userID uint) error {
	// JOIN-оптимизация: passing + game в 1 SQL-запросе.
	// A-1 (pass 37): через репозиторий — раньше read-path шёл через s.db,
	// дублируя готовый GetPassingWithGameByGamePassingID (pass 36).
	passing, err := s.blackboxRepo.GetPassingWithGameByGamePassingID(ctx, gamePassingID)
	if err != nil {
		return err
	}
	g := passing.Game
	// C4: автор или соавтор (консистентно с доступом к странице мониторинга).
	ok, err := s.coAuthorSvc.IsUserManager(ctx, g.ID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("только автор или модератор может запустить голосование")
	}

	session, err := s.blackboxRepo.GetSessionByPassingAndLevel(ctx, gamePassingID, levelID)
	if err == nil {
		if session.IsOpen {
			return errors.New("голосование уже активно")
		}
		return errors.New("голосование уже было проведено")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	session = &BlackboxVotingSession{
		GamePassingID: gamePassingID,
		LevelID:       levelID,
		IsOpen:        true,
	}
	if err := s.blackboxRepo.CreateSession(ctx, session); err != nil {
		// Конкурентный StartVoting уже создал сессию (unique violation по
		// idx_passing_level) — перечитываем и отвечаем бизнес-сообщением
		// вместо 500 (B8: check-then-insert гонка закрыта на уровне БД).
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			existing, rErr := s.blackboxRepo.GetSessionByPassingAndLevel(ctx, gamePassingID, levelID)
			if rErr == nil {
				if existing.IsOpen {
					return errors.New("голосование уже активно")
				}
				return errors.New("голосование уже было проведено")
			}
		}
		return err
	}

	if s.cfg != nil && s.cfg.SMTP.Enabled {
		captains, err := s.blackboxRepo.GetCaptainEmailsByGame(ctx, g.ID)
		if err != nil {
			log.Error().Err(err).Uint("game_id", g.ID).Msg("failed to load captains for voting start email")
		}

		for _, emailAddr := range captains {
			if err := email.Enqueue(
				emailAddr,
				i18n.T("monitor.vote_started_subject"),
				i18n.TF("monitor.vote_started_body", g.Name),
			); err != nil {
				log.Error().Err(err).Str("game", g.Name).Msg("failed to enqueue voting start email")
			}
		}
	}
	return nil
}

// Vote регистрирует голос команды за выбранный вариант.
func (s *BlackboxVoteService) Vote(ctx context.Context, sessionID, voterTeamID, userID uint, option string) error {
	session, err := s.blackboxRepo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if !session.IsOpen {
		return errors.New("голосование закрыто")
	}

	// Проверка членства: пользователь должен быть участником команды voterTeamID
	// или автором игры. Иначе любой может голосовать за чужую команду.
	isMember, err := s.blackboxRepo.IsTeamMember(ctx, voterTeamID, userID)
	if err != nil {
		return err
	}
	if !isMember {
		// Менеджер/автор игры может голосовать от любой команды
		isManager, mgrErr := s.isGameManager(ctx, session, userID)
		if mgrErr != nil || !isManager {
			return ErrNotTeamMember
		}
	}

	// Валидация опциона внутри транзакции для избежания race condition
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Блокируем сессию для сериализации
		var lockedSession BlackboxVotingSession
		if lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedSession, sessionID).Error; lockErr != nil {
			return lockErr
		}
		// C-10: сессия могла закрыться между первичным чтением и блокировкой —
		// пере-проверяем IsOpen под локом.
		if !lockedSession.IsOpen {
			return ErrVotingClosed
		}

		// N-1 (pass 41): EXISTS вместо загрузки ВСЕХ попыток уровня в память
		// (ранее Find + фильтр в Go — на активной игре тысячи строк кодов).
		var exists int64
		if findErr := tx.Model(&game.Attempt{}).
			Where("level_progress_id IN (SELECT id FROM level_progresses WHERE game_passing_id = ? AND level_id = ?)",
				lockedSession.GamePassingID, lockedSession.LevelID).
			Where("(is_file = true AND file_path = ?) OR (is_file = false AND code = ?)", option, option).
			Limit(1).
			Count(&exists).Error; findErr != nil {
			return findErr
		}
		if exists == 0 {
			return ErrInvalidOption
		}

		// Проверяем существование голоса внутри транзакции
		var existingVote BlackboxVote
		getVoteErr := tx.Where("session_id = ? AND voter_id = ?", sessionID, voterTeamID).First(&existingVote).Error
		if getVoteErr == nil {
			return ErrVoteAlreadyCast
		}
		if !errors.Is(getVoteErr, gorm.ErrRecordNotFound) {
			return getVoteErr
		}

		vote := &BlackboxVote{
			SessionID: sessionID,
			VoterID:   voterTeamID,
			Option:    option,
		}
		return tx.Create(vote).Error
	})
}

// isGameManager проверяет, что пользователь является автором ИЛИ соавтором игры
// (единый механизм прав, согласован с game.CoAuthorService.IsUserManager — C4).
func (s *BlackboxVoteService) isGameManager(ctx context.Context, session *BlackboxVotingSession, userID uint) (bool, error) {
	passing, err := s.blackboxRepo.GetPassingByGamePassingID(ctx, session.GamePassingID)
	if err != nil {
		return false, err
	}
	return s.coAuthorSvc.IsUserManager(ctx, passing.GameID, userID)
}

// GetVotingResults возвращает пары «вариант — количество голосов».
// Доступ разрешён автору/соавтору игры или участнику команды, за которую
// было открыто голосование (защита от IDOR по чужой сессии).
func (s *BlackboxVoteService) GetVotingResults(ctx context.Context, sessionID, userID uint) (map[string]int, error) {
	session, err := s.blackboxRepo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	isManager, err := s.isGameManager(ctx, session, userID)
	if err != nil {
		return nil, err
	}
	if !isManager {
		passing, findErr := s.blackboxRepo.GetPassingByGamePassingID(ctx, session.GamePassingID)
		if findErr != nil {
			return nil, findErr
		}
		isMember, countErr := s.blackboxRepo.IsTeamMember(ctx, passing.TeamID, userID)
		if countErr != nil {
			return nil, countErr
		}
		if !isMember {
			return nil, ErrAccessDenied
		}
	}

	votes, err := s.blackboxRepo.GetVotesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	results := make(map[string]int)
	for _, v := range votes {
		results[v.Option]++
	}
	return results, nil
}

// CloseVoting закрывает голосование и определяет победителя.
func (s *BlackboxVoteService) CloseVoting(ctx context.Context, sessionID, userID uint) (string, error) {
	session, getSessionErr := s.blackboxRepo.GetSessionByID(ctx, sessionID)
	if getSessionErr != nil {
		return "", getSessionErr
	}

	// JOIN-оптимизация: passing + game в 1 SQL-запросе
	passing, findErr := s.blackboxRepo.GetPassingWithGameByGamePassingID(ctx, session.GamePassingID)
	if findErr != nil {
		return "", findErr
	}
	g := passing.Game
	// C4: автор или соавтор (консистентно с доступом к странице мониторинга).
	ok, err := s.coAuthorSvc.IsUserManager(ctx, g.ID, userID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("только автор или модератор может завершить голосование")
	}

	// Закрытие + подсчёт голосов в одной транзакции с блокировкой сессии:
	// голос, пришедший между чтением результатов и закрытием, учитывается (B8).
	var winner string
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lockedSession BlackboxVotingSession
		if lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedSession, sessionID).Error; lockErr != nil {
			return lockErr
		}

		var votes []BlackboxVote
		if voteErr := tx.Where("session_id = ?", sessionID).Find(&votes).Error; voteErr != nil {
			return voteErr
		}

		results := make(map[string]int)
		for _, v := range votes {
			results[v.Option]++
		}
		maxVotes := 0
		// Детерминированный тай-брейк (C-M3): при равенстве голосов побеждает
		// лексикографически первый вариант — раньше порядок map был случайным.
		options := make([]string, 0, len(results))
		for option := range results {
			options = append(options, option)
		}
		sort.Strings(options)
		for _, option := range options {
			count := results[option]
			if count >= maxVotes {
				maxVotes = count
				winner = option
			}
		}

		lockedSession.IsOpen = false
		lockedSession.WinnerOption = winner
		if updateErr := tx.Save(&lockedSession).Error; updateErr != nil {
			return updateErr
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	if s.cfg != nil && s.cfg.SMTP.Enabled {
		captains, err := s.blackboxRepo.GetCaptainEmailsByGame(ctx, g.ID)
		if err != nil {
			log.Error().Err(err).Uint("game_id", g.ID).Msg("failed to load captains for voting end email")
		}

		for _, emailAddr := range captains {
			if emailErr := email.Enqueue(
				emailAddr,
				i18n.T("monitor.vote_closed_subject"),
				i18n.TF("monitor.vote_closed_body", g.Name, winner),
			); emailErr != nil {
				log.Error().Err(emailErr).Str("game", g.Name).Str("winner", winner).Msg("failed to enqueue voting end email")
			}
		}
	}
	return winner, nil
}

// ---------- ChatService ----------

type ChatService struct {
	chatRepo ChatRepository
}

func NewChatService(chatRepo ChatRepository) *ChatService {
	return &ChatService{chatRepo: chatRepo}
}

func (s *ChatService) GetOrCreateGameRoom(ctx context.Context, gameID uint) (*ChatRoom, error) {
	return s.chatRepo.GetOrCreateGameRoom(ctx, gameID)
}

func (s *ChatService) GetOrCreateTeamRoom(ctx context.Context, gameID, teamID, passingID uint) (*ChatRoom, error) {
	return s.chatRepo.GetOrCreateTeamRoom(ctx, gameID, teamID, passingID)
}

func (s *ChatService) SaveMessage(ctx context.Context, roomID, userID uint, content string) (*ChatMessage, error) {
	return s.chatRepo.SaveMessage(ctx, roomID, userID, content)
}

func (s *ChatService) GetMessages(ctx context.Context, roomID uint, limit int) ([]ChatMessage, error) {
	return s.chatRepo.GetMessages(ctx, roomID, limit)
}

func (s *ChatService) GetByID(ctx context.Context, roomID uint) (*ChatRoom, error) {
	return s.chatRepo.GetByID(ctx, roomID)
}

func (s *ChatService) IsTeamMemberOrCaptain(ctx context.Context, teamID, userID uint) (bool, error) {
	return s.chatRepo.IsTeamMemberOrCaptain(ctx, teamID, userID)
}

func (s *ChatService) GetMessageByID(ctx context.Context, messageID uint) (*ChatMessage, error) {
	return s.chatRepo.GetMessageByID(ctx, messageID)
}
