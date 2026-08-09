// internal/domain/monitor/repository.go
package monitor

import (
	"context"
	"errors"
	"fmt"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

// ChatRepository определяет контракт для работы с чатами.
type ChatRepository interface {
	GetOrCreateGameRoom(ctx context.Context, gameID uint) (*ChatRoom, error)
	GetOrCreateTeamRoom(ctx context.Context, gameID, teamID, passingID uint) (*ChatRoom, error)
	GetByID(ctx context.Context, roomID uint) (*ChatRoom, error)
	IsTeamMemberOrCaptain(ctx context.Context, teamID, userID uint) (bool, error)
	SaveMessage(ctx context.Context, roomID, userID uint, content string) (*ChatMessage, error)
	GetMessages(ctx context.Context, roomID uint, limit int) ([]ChatMessage, error)
	GetMessageByID(ctx context.Context, messageID uint) (*ChatMessage, error)
}

// BlackboxRepository определяет контракт для работы с голосованиями.
type BlackboxRepository interface {
	CreateSession(ctx context.Context, session *BlackboxVotingSession) error
	GetSessionByPassingAndLevel(ctx context.Context, passingID, levelID uint) (*BlackboxVotingSession, error)
	GetSessionByID(ctx context.Context, id uint) (*BlackboxVotingSession, error)
	GetVotesBySession(ctx context.Context, sessionID uint) ([]BlackboxVote, error)
	// A-2 (pass 36): typed read-методы — раньше BlackboxVoteService ходил
	// в БД напрямую (s.db) для passing/капитанов/членства.
	GetPassingByGamePassingID(ctx context.Context, passingID uint) (*game.GamePassing, error)
	GetPassingWithGameByGamePassingID(ctx context.Context, passingID uint) (*game.GamePassing, error)
	GetCaptainEmailsByGame(ctx context.Context, gameID uint) ([]string, error)
	IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error)
}

// ---------- GORM implementations ----------

type gormChatRepo struct{ db *gorm.DB }

func NewGormChatRepo(db *gorm.DB) ChatRepository {
	return &gormChatRepo{db: db}
}

func (r *gormChatRepo) GetOrCreateGameRoom(ctx context.Context, gameID uint) (*ChatRoom, error) {
	var room ChatRoom
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id IS NULL AND passing_id IS NULL", gameID).First(&room).Error
	if err == nil {
		return &room, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	room = ChatRoom{
		GameID: &gameID,
		Name:   "Общий чат игры",
	}
	// Create with conflict handling — if duplicate (race), re-query
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		if err := r.db.WithContext(ctx).Where("game_id = ? AND team_id IS NULL AND passing_id IS NULL", gameID).First(&room).Error; err != nil {
			return nil, fmt.Errorf("failed to get or create game chat room: %w", err)
		}
		return &room, nil
	}
	return &room, nil
}

func (r *gormChatRepo) GetOrCreateTeamRoom(ctx context.Context, gameID, teamID, passingID uint) (*ChatRoom, error) {
	var room ChatRoom
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ? AND passing_id = ?", gameID, teamID, passingID).First(&room).Error
	if err == nil {
		return &room, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	room = ChatRoom{
		GameID:    &gameID,
		TeamID:    &teamID,
		PassingID: &passingID,
		Name:      "Командный чат",
	}
	// Create with conflict handling — if duplicate (race), re-query
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		if err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ? AND passing_id = ?", gameID, teamID, passingID).First(&room).Error; err != nil {
			return nil, fmt.Errorf("failed to get or create team chat room: %w", err)
		}
		return &room, nil
	}
	return &room, nil
}

func (r *gormChatRepo) SaveMessage(ctx context.Context, roomID, userID uint, content string) (*ChatMessage, error) {
	msg := ChatMessage{
		RoomID:  roomID,
		UserID:  userID,
		Content: content,
	}
	if err := r.db.WithContext(ctx).Create(&msg).Error; err != nil {
		return nil, err
	}
	return r.GetMessageByID(ctx, msg.ID)
}

// GetByID возвращает комнату чата по ID (для проверки прав в ChatWS).
func (r *gormChatRepo) GetByID(ctx context.Context, roomID uint) (*ChatRoom, error) {
	var room ChatRoom
	if err := r.db.WithContext(ctx).First(&room, roomID).Error; err != nil {
		return nil, err
	}
	return &room, nil
}

// IsTeamMemberOrCaptain — участник команды или её капитан (для командного чата).
func (r *gormChatRepo) IsTeamMemberOrCaptain(ctx context.Context, teamID, userID uint) (bool, error) {
	var memberCount int64
	if err := r.db.WithContext(ctx).Table("team_members").
		Where("team_id = ? AND user_id = ?", teamID, userID).Count(&memberCount).Error; err != nil {
		return false, err
	}
	if memberCount > 0 {
		return true, nil
	}
	var capt struct{ CaptainID uint }
	if err := r.db.WithContext(ctx).Table("teams").Where("id = ?", teamID).First(&capt).Error; err != nil {
		return false, err
	}
	return capt.CaptainID == userID, nil
}

// GetMessageByID возвращает сообщение с прелоадом автора.
func (r *gormChatRepo) GetMessageByID(ctx context.Context, messageID uint) (*ChatMessage, error) {
	var msg ChatMessage
	if err := r.db.WithContext(ctx).Preload("User").First(&msg, messageID).Error; err != nil {
		return nil, err
	}
	return &msg, nil
}

func (r *gormChatRepo) GetMessages(ctx context.Context, roomID uint, limit int) ([]ChatMessage, error) {
	var msgs []ChatMessage
	err := r.db.WithContext(ctx).Preload("User").
		Where("room_id = ?", roomID).
		Order("created_at DESC").
		Limit(limit).
		Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	// reverse slice
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

type gormBlackboxRepo struct{ db *gorm.DB }

func NewGormBlackboxRepo(db *gorm.DB) BlackboxRepository {
	return &gormBlackboxRepo{db: db}
}

func (r *gormBlackboxRepo) CreateSession(ctx context.Context, session *BlackboxVotingSession) error {
	return r.db.WithContext(ctx).Create(session).Error
}

func (r *gormBlackboxRepo) GetSessionByPassingAndLevel(ctx context.Context, passingID, levelID uint) (*BlackboxVotingSession, error) {
	var session BlackboxVotingSession
	err := r.db.WithContext(ctx).Where("game_passing_id = ? AND level_id = ?", passingID, levelID).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *gormBlackboxRepo) GetSessionByID(ctx context.Context, id uint) (*BlackboxVotingSession, error) {
	var session BlackboxVotingSession
	err := r.db.WithContext(ctx).First(&session, id).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *gormBlackboxRepo) GetVotesBySession(ctx context.Context, sessionID uint) ([]BlackboxVote, error) {
	var votes []BlackboxVote
	err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Find(&votes).Error
	return votes, err
}

// GetPassingByGamePassingID загружает прохождение (A-2, pass 36).
func (r *gormBlackboxRepo) GetPassingByGamePassingID(ctx context.Context, passingID uint) (*game.GamePassing, error) {
	var passing game.GamePassing
	if err := r.db.WithContext(ctx).First(&passing, passingID).Error; err != nil {
		return nil, err
	}
	return &passing, nil
}

// GetPassingWithGameByGamePassingID загружает прохождение с игрой (JOIN).
func (r *gormBlackboxRepo) GetPassingWithGameByGamePassingID(ctx context.Context, passingID uint) (*game.GamePassing, error) {
	var passing game.GamePassing
	if err := r.db.WithContext(ctx).Joins("Game").First(&passing, passingID).Error; err != nil {
		return nil, err
	}
	return &passing, nil
}

// GetCaptainEmailsByGame возвращает email капитанов стартовавших команд.
func (r *gormBlackboxRepo) GetCaptainEmailsByGame(ctx context.Context, gameID uint) ([]string, error) {
	var captains []string
	err := r.db.WithContext(ctx).Model(&user.User{}).
		Select("users.email").
		Joins("JOIN teams ON teams.captain_id = users.id").
		Joins("JOIN game_passings ON game_passings.team_id = teams.id").
		Where("game_passings.game_id = ? AND game_passings.status = ?", gameID, game.StatusStarted).
		Pluck("email", &captains).Error
	return captains, err
}

// IsTeamMember проверяет членство пользователя в команде.
func (r *gormBlackboxRepo) IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error) {
	var memberCount int64
	if err := r.db.WithContext(ctx).Table("team_members").
		Where("team_id = ? AND user_id = ?", teamID, userID).Count(&memberCount).Error; err != nil {
		return false, err
	}
	return memberCount > 0, nil
}
