// internal/domain/monitor/service.go
package monitor

import (
	"context"
	"errors"
	"sort"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/i18n"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------- BlackboxVoteService ----------

type BlackboxVoteService struct {
	blackboxRepo BlackboxRepository
	gameRepo     game.GameRepository
	db           *gorm.DB
	cfg          *config.Config
}

func NewBlackboxVoteService(
	blackboxRepo BlackboxRepository,
	gameRepo game.GameRepository,
	db *gorm.DB,
	cfg *config.Config,
) *BlackboxVoteService {
	return &BlackboxVoteService{
		blackboxRepo: blackboxRepo,
		gameRepo:     gameRepo,
		db:           db,
		cfg:          cfg,
	}
}

// StartVoting открывает новую сессию голосования и оповещает участников.
func (s *BlackboxVoteService) StartVoting(ctx context.Context, gamePassingID, levelID, userID uint) error {
	// JOIN-оптимизация: получаем passing + game в 1 SQL-запросе
	var passing game.GamePassing
	if err := s.db.WithContext(ctx).Joins("Game").First(&passing, gamePassingID).Error; err != nil {
		return err
	}
	g := passing.Game
	// C4: автор или соавтор (консистентно с доступом к странице мониторинга).
	ok, err := s.isGameManagerForGame(ctx, g.ID, userID)
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
		var captains []string
		if err := s.db.WithContext(ctx).Model(&user.User{}).
			Select("users.email").
			Joins("JOIN teams ON teams.captain_id = users.id").
			Joins("JOIN game_passings ON game_passings.team_id = teams.id").
			Where("game_passings.game_id = ? AND game_passings.status = ?", g.ID, game.StatusStarted).
			Pluck("email", &captains).Error; err != nil {
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
	var memberCount int64
	if err := s.db.WithContext(ctx).Table("team_members").
		Where("team_id = ? AND user_id = ?", voterTeamID, userID).Count(&memberCount).Error; err != nil {
		return err
	}
	if memberCount == 0 {
		// Менеджер/автор игры может голосовать от любой команды
		isManager, mgrErr := s.isGameManager(ctx, session, userID)
		if mgrErr != nil || !isManager {
			return errors.New("вы не являетесь участником этой команды")
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
			return errors.New("голосование закрыто")
		}

		var attempts []game.Attempt
		if findErr := tx.Where("level_progress_id IN (SELECT id FROM level_progresses WHERE game_passing_id = ? AND level_id = ?)",
			lockedSession.GamePassingID, lockedSession.LevelID).
			Find(&attempts).Error; findErr != nil {
			return findErr
		}
		valid := false
		for _, a := range attempts {
			if (a.IsFile && a.FilePath == option) || (!a.IsFile && a.Code == option) {
				valid = true
				break
			}
		}
		if !valid {
			return errors.New("недопустимый вариант ответа")
		}

		// Проверяем существование голоса внутри транзакции
		var existingVote BlackboxVote
		getVoteErr := tx.Where("session_id = ? AND voter_id = ?", sessionID, voterTeamID).First(&existingVote).Error
		if getVoteErr == nil {
			return errors.New("ваш голос уже учтён")
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
	var passing game.GamePassing
	if err := s.db.WithContext(ctx).First(&passing, session.GamePassingID).Error; err != nil {
		return false, err
	}
	return s.isGameManagerForGame(ctx, passing.GameID, userID)
}

// isGameManagerForGame — автор или соавтор игры (author+co_authors UNION).
func (s *BlackboxVoteService) isGameManagerForGame(ctx context.Context, gameID, userID uint) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Raw(`
		SELECT COUNT(*) FROM (
			SELECT 1 FROM games WHERE id = ? AND author_id = ? AND deleted_at IS NULL
			UNION
			SELECT 1 FROM co_authors WHERE game_id = ? AND user_id = ? AND deleted_at IS NULL
		) sub
	`, gameID, userID, gameID, userID).Scan(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
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
		var passing game.GamePassing
		if findErr := s.db.WithContext(ctx).First(&passing, session.GamePassingID).Error; findErr != nil {
			return nil, findErr
		}
		var memberCount int64
		if countErr := s.db.WithContext(ctx).Table("team_members").
			Where("team_id = ? AND user_id = ?", passing.TeamID, userID).Count(&memberCount).Error; countErr != nil {
			return nil, countErr
		}
		if memberCount == 0 {
			return nil, errors.New("доступ запрещён")
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
	var passing game.GamePassing
	if findErr := s.db.WithContext(ctx).Joins("Game").First(&passing, session.GamePassingID).Error; findErr != nil {
		return "", findErr
	}
	g := passing.Game
	// C4: автор или соавтор (консистентно с доступом к странице мониторинга).
	ok, err := s.isGameManagerForGame(ctx, g.ID, userID)
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
		var captains []string
		if err := s.db.WithContext(ctx).Model(&user.User{}).
			Select("users.email").
			Joins("JOIN teams ON teams.captain_id = users.id").
			Joins("JOIN game_passings ON game_passings.team_id = teams.id").
			Where("game_passings.game_id = ? AND game_passings.status = ?", g.ID, game.StatusStarted).
			Pluck("email", &captains).Error; err != nil {
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
