// internal/domain/monitor/service.go
package monitor

import (
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
	DB  *gorm.DB
	cfg *config.Config
}

func NewBlackboxVoteService(db *gorm.DB, cfg *config.Config) *BlackboxVoteService {
	return &BlackboxVoteService{DB: db, cfg: cfg}
}

// StartVoting открывает новую сессию голосования и оповещает участников.
func (s *BlackboxVoteService) StartVoting(gamePassingID, levelID, userID uint) error {
	var passing game.GamePassing
	if err := s.DB.First(&passing, gamePassingID).Error; err != nil {
		return err
	}
	var g game.Game
	if err := s.DB.First(&g, passing.GameID).Error; err != nil {
		return err
	}
	if g.AuthorID != userID {
		return errors.New("только автор может запустить голосование")
	}

	var existing BlackboxVotingSession
	if err := s.DB.Where("game_passing_id = ? AND level_id = ?", gamePassingID, levelID).First(&existing).Error; err == nil {
		if existing.IsOpen {
			return errors.New("голосование уже активно")
		}
		return errors.New("голосование уже было проведено")
	}

	session := BlackboxVotingSession{
		GamePassingID: gamePassingID,
		LevelID:       levelID,
		IsOpen:        true,
	}
	if err := s.DB.Create(&session).Error; err != nil {
		return err
	}

	if s.cfg != nil {
		emailService := email.NewEmailService(s.cfg)

		var captains []string
		s.DB.Model(&user.User{}).
			Select("users.email").
			Joins("JOIN teams ON teams.captain_id = users.id").
			Joins("JOIN game_passings ON game_passings.team_id = teams.id").
			Where("game_passings.game_id = ? AND game_passings.status = ?", g.ID, game.StatusStarted).
			Pluck("email", &captains)

		for _, emailAddr := range captains {
			if err := emailService.Send(emailAddr, "Запущено голосование",
				fmt.Sprintf("В игре «%s» запущено голосование за лучший ответ.", g.Name)); err != nil {
				log.Error().Err(err).Str("game", g.Name).Msg("failed to send voting start email")
			}
		}
	}
	return nil
}

// Vote регистрирует голос команды за выбранный вариант.
func (s *BlackboxVoteService) Vote(sessionID, voterTeamID uint, option string) error {
	var session BlackboxVotingSession
	if err := s.DB.First(&session, sessionID).Error; err != nil {
		return err
	}
	if !session.IsOpen {
		return errors.New("голосование закрыто")
	}

	var attempts []game.Attempt
	s.DB.Where("level_progress_id IN (SELECT id FROM level_progresses WHERE game_passing_id = ? AND level_id = ?)",
		session.GamePassingID, session.LevelID).Find(&attempts)
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

	var existing BlackboxVote
	err := s.DB.Where("session_id = ? AND voter_id = ?", sessionID, voterTeamID).First(&existing).Error
	if err == nil {
		return errors.New("ваш голос уже учтён")
	}

	vote := BlackboxVote{
		SessionID: sessionID,
		VoterID:   voterTeamID,
		Option:    option,
	}
	return s.DB.Create(&vote).Error
}

// GetVotingResults возвращает пары «вариант — количество голосов».
func (s *BlackboxVoteService) GetVotingResults(sessionID uint) (map[string]int, error) {
	var votes []BlackboxVote
	if err := s.DB.Where("session_id = ?", sessionID).Find(&votes).Error; err != nil {
		return nil, err
	}
	results := make(map[string]int)
	for _, v := range votes {
		results[v.Option]++
	}
	return results, nil
}

// CloseVoting закрывает голосование и определяет победителя.
func (s *BlackboxVoteService) CloseVoting(sessionID, userID uint) (string, error) {
	var session BlackboxVotingSession
	if err := s.DB.First(&session, sessionID).Error; err != nil {
		return "", err
	}

	var passing game.GamePassing
	if err := s.DB.First(&passing, session.GamePassingID).Error; err != nil {
		return "", err
	}
	var g game.Game
	if err := s.DB.First(&g, passing.GameID).Error; err != nil {
		return "", err
	}
	if g.AuthorID != userID {
		return "", errors.New("только автор может завершить голосование")
	}

	results, err := s.GetVotingResults(sessionID)
	if err != nil {
		return "", err
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
	s.DB.Save(&session)

	if s.cfg != nil {
		emailService := email.NewEmailService(s.cfg)

		var captains []string
		s.DB.Model(&user.User{}).
			Select("users.email").
			Joins("JOIN teams ON teams.captain_id = users.id").
			Joins("JOIN game_passings ON game_passings.team_id = teams.id").
			Where("game_passings.game_id = ? AND game_passings.status = ?", g.ID, game.StatusStarted).
			Pluck("email", &captains)

		for _, emailAddr := range captains {
			if err := emailService.Send(emailAddr, "Голосование завершено",
				fmt.Sprintf("В игре «%s» завершено голосование. Победивший вариант: %s", g.Name, winner)); err != nil {
				log.Error().Err(err).Str("game", g.Name).Str("winner", winner).Msg("failed to send voting end email")
			}
		}
	}
	return winner, nil
}

// ---------- ChatService ----------

type ChatService struct {
	DB *gorm.DB
}

func NewChatService(db *gorm.DB) *ChatService {
	return &ChatService{DB: db}
}

// GetOrCreateGameRoom возвращает общую комнату чата игры.
func (s *ChatService) GetOrCreateGameRoom(gameID uint) (*ChatRoom, error) {
	var room ChatRoom
	err := s.DB.Where("game_id = ? AND team_id IS NULL", gameID).First(&room).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		room = ChatRoom{
			GameID: &gameID,
			Name:   "Общий чат игры",
		}
		if createErr := s.DB.Create(&room).Error; createErr != nil {
			return nil, createErr
		}
		return &room, nil
	}
	if err != nil {
		return nil, err
	}
	return &room, nil
}

// GetOrCreateTeamRoom возвращает приватную командную комнату для конкретного прохождения.
func (s *ChatService) GetOrCreateTeamRoom(gameID, teamID, passingID uint) (*ChatRoom, error) {
	var room ChatRoom
	err := s.DB.Where("game_id = ? AND team_id = ? AND passing_id = ?", gameID, teamID, passingID).First(&room).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		room = ChatRoom{
			GameID:    &gameID,
			TeamID:    &teamID,
			PassingID: &passingID,
			Name:      "Командный чат",
		}
		if createErr := s.DB.Create(&room).Error; createErr != nil {
			return nil, createErr
		}
		return &room, nil
	}
	if err != nil {
		return nil, err
	}
	return &room, nil
}

// SaveMessage сохраняет новое сообщение в БД.
func (s *ChatService) SaveMessage(roomID, userID uint, content string) (*ChatMessage, error) {
	msg := ChatMessage{
		RoomID:  roomID,
		UserID:  userID,
		Content: content,
	}
	if err := s.DB.Create(&msg).Error; err != nil {
		return nil, err
	}
	s.DB.Preload("User").First(&msg, msg.ID)
	return &msg, nil
}

// GetMessages возвращает последние сообщения комнаты (лимит).
func (s *ChatService) GetMessages(roomID uint, limit int) ([]ChatMessage, error) {
	var msgs []ChatMessage
	err := s.DB.Preload("User").Where("room_id = ?", roomID).Order("created_at DESC").Limit(limit).Find(&msgs).Error
	reverseSlice(msgs)
	return msgs, err
}

func reverseSlice(msgs []ChatMessage) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}