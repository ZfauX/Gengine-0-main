// internal/domain/monitor/service.go
package monitor

import (
	"context"
	"errors"
	"fmt"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/email"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// ---------- BlackboxVoteService ----------

type BlackboxVoteService struct {
	blackboxRepo BlackboxRepository
	gameRepo     game.GameRepository
	cfg          *config.Config
}

func NewBlackboxVoteService(
	blackboxRepo BlackboxRepository,
	gameRepo game.GameRepository,
	cfg *config.Config,
) *BlackboxVoteService {
	return &BlackboxVoteService{
		blackboxRepo: blackboxRepo,
		gameRepo:     gameRepo,
		cfg:          cfg,
	}
}

// StartVoting открывает новую сессию голосования и оповещает участников.
func (s *BlackboxVoteService) StartVoting(ctx context.Context, gamePassingID, levelID, userID uint) error {
	var passing game.GamePassing
	if err := s.gameRepo.DB(ctx).First(&passing, gamePassingID).Error; err != nil {
		return err
	}
	g, err := s.gameRepo.GetByID(ctx, passing.GameID)
	if err != nil {
		return err
	}
	if g.AuthorID != userID {
		return errors.New("только автор может запустить голосование")
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
		return err
	}

	if s.cfg != nil && s.cfg.SMTP.Enabled {
		var captains []string
		s.gameRepo.DB(ctx).Model(&user.User{}).
			Select("users.email").
			Joins("JOIN teams ON teams.captain_id = users.id").
			Joins("JOIN game_passings ON game_passings.team_id = teams.id").
			Where("game_passings.game_id = ? AND game_passings.status = ?", g.ID, game.StatusStarted).
			Pluck("email", &captains)

		for _, emailAddr := range captains {
			if err := email.Enqueue(
				emailAddr,
				"Запущено голосование",
				fmt.Sprintf("В игре «%s» запущено голосование за лучший ответ.", g.Name),
			); err != nil {
				log.Error().Err(err).Str("game", g.Name).Msg("failed to enqueue voting start email")
			}
		}
	}
	return nil
}

// Vote регистрирует голос команды за выбранный вариант.
func (s *BlackboxVoteService) Vote(ctx context.Context, sessionID, voterTeamID uint, option string) error {
	session, err := s.blackboxRepo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if !session.IsOpen {
		return errors.New("голосование закрыто")
	}

	// Валидация опциона внутри транзакции для избежания race condition
	return s.gameRepo.DB(ctx).Transaction(func(tx *gorm.DB) error {
		// Блокируем сессию для сериализации
		var lockedSession BlackboxVotingSession
		if lockErr := tx.Set("gorm:query_option", "FOR UPDATE").First(&lockedSession, sessionID).Error; lockErr != nil {
			return lockErr
		}

		var attempts []game.Attempt
		tx.Where("level_progress_id IN (SELECT id FROM level_progresses WHERE game_passing_id = ? AND level_id = ?)",
			lockedSession.GamePassingID, lockedSession.LevelID).
			Find(&attempts)
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
		_, getVoteErr := s.blackboxRepo.GetVoteBySessionAndVoter(ctx, sessionID, voterTeamID)
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

// GetVotingResults возвращает пары «вариант — количество голосов».
func (s *BlackboxVoteService) GetVotingResults(ctx context.Context, sessionID uint) (map[string]int, error) {
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

	var passing game.GamePassing
	if findErr := s.gameRepo.DB(ctx).First(&passing, session.GamePassingID).Error; findErr != nil {
		return "", findErr
	}
	g, getErr := s.gameRepo.GetByID(ctx, passing.GameID)
	if getErr != nil {
		return "", getErr
	}
	if g.AuthorID != userID {
		return "", errors.New("только автор может завершить голосование")
	}

	results, getResultsErr := s.GetVotingResults(ctx, sessionID)
	if getResultsErr != nil {
		return "", getResultsErr
	}

	maxVotes := 0
	winner := ""
	for option, count := range results {
		if count > maxVotes {
			maxVotes = count
			winner = option
		}
	}

	session.IsOpen = false
	session.WinnerOption = winner
	if updateErr := s.blackboxRepo.UpdateSession(ctx, session); updateErr != nil {
		return "", updateErr
	}

	if s.cfg != nil && s.cfg.SMTP.Enabled {
		var captains []string
		s.gameRepo.DB(ctx).Model(&user.User{}).
			Select("users.email").
			Joins("JOIN teams ON teams.captain_id = users.id").
			Joins("JOIN game_passings ON game_passings.team_id = teams.id").
			Where("game_passings.game_id = ? AND game_passings.status = ?", g.ID, game.StatusStarted).
			Pluck("email", &captains)

		for _, emailAddr := range captains {
			if emailErr := email.Enqueue(
				emailAddr,
				"Голосование завершено",
				fmt.Sprintf("В игре «%s» завершено голосование. Победивший вариант: %s", g.Name, winner),
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
