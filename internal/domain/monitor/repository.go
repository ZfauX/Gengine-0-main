// internal/domain/monitor/repository.go
package monitor

import (
	"context"

	"gorm.io/gorm"
)

// ChatRepository определяет контракт для работы с чатами.
type ChatRepository interface {
	GetOrCreateGameRoom(ctx context.Context, gameID uint) (*ChatRoom, error)
	GetOrCreateTeamRoom(ctx context.Context, gameID, teamID, passingID uint) (*ChatRoom, error)
	SaveMessage(ctx context.Context, roomID, userID uint, content string) (*ChatMessage, error)
	GetMessages(ctx context.Context, roomID uint, limit int) ([]ChatMessage, error)
}

// BlackboxRepository определяет контракт для работы с голосованиями.
type BlackboxRepository interface {
	CreateSession(ctx context.Context, session *BlackboxVotingSession) error
	GetSessionByPassingAndLevel(ctx context.Context, passingID, levelID uint) (*BlackboxVotingSession, error)
	GetSessionByID(ctx context.Context, id uint) (*BlackboxVotingSession, error)
	UpdateSession(ctx context.Context, session *BlackboxVotingSession) error
	CreateVote(ctx context.Context, vote *BlackboxVote) error
	GetVotesBySession(ctx context.Context, sessionID uint) ([]BlackboxVote, error)
	GetVoteBySessionAndVoter(ctx context.Context, sessionID, voterID uint) (*BlackboxVote, error)
}

// ---------- GORM implementations ----------

type gormChatRepo struct{ db *gorm.DB }

func NewGormChatRepo(db *gorm.DB) ChatRepository {
	return &gormChatRepo{db: db}
}

func (r *gormChatRepo) GetOrCreateGameRoom(ctx context.Context, gameID uint) (*ChatRoom, error) {
	var room ChatRoom
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id IS NULL", gameID).First(&room).Error
	if err == nil {
		return &room, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	room = ChatRoom{
		GameID: &gameID,
		Name:   "Общий чат игры",
	}
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		return nil, createErr
	}
	return &room, nil
}

func (r *gormChatRepo) GetOrCreateTeamRoom(ctx context.Context, gameID, teamID, passingID uint) (*ChatRoom, error) {
	var room ChatRoom
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ? AND passing_id = ?", gameID, teamID, passingID).First(&room).Error
	if err == nil {
		return &room, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	room = ChatRoom{
		GameID:    &gameID,
		TeamID:    &teamID,
		PassingID: &passingID,
		Name:      "Командный чат",
	}
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		return nil, createErr
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
	r.db.WithContext(ctx).Preload("User").First(&msg, msg.ID)
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
	return &session, err
}

func (r *gormBlackboxRepo) GetSessionByID(ctx context.Context, id uint) (*BlackboxVotingSession, error) {
	var session BlackboxVotingSession
	err := r.db.WithContext(ctx).First(&session, id).Error
	return &session, err
}

func (r *gormBlackboxRepo) UpdateSession(ctx context.Context, session *BlackboxVotingSession) error {
	return r.db.WithContext(ctx).Save(session).Error
}

func (r *gormBlackboxRepo) CreateVote(ctx context.Context, vote *BlackboxVote) error {
	return r.db.WithContext(ctx).Create(vote).Error
}

func (r *gormBlackboxRepo) GetVotesBySession(ctx context.Context, sessionID uint) ([]BlackboxVote, error) {
	var votes []BlackboxVote
	err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Find(&votes).Error
	return votes, err
}

func (r *gormBlackboxRepo) GetVoteBySessionAndVoter(ctx context.Context, sessionID, voterID uint) (*BlackboxVote, error) {
	var vote BlackboxVote
	err := r.db.WithContext(ctx).Where("session_id = ? AND voter_id = ?", sessionID, voterID).First(&vote).Error
	return &vote, err
}
