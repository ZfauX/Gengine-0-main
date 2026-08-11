// internal/domain/monitor/repository.go
package monitor

import (
	"context"
	"errors"
	"fmt"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChatRepository определяет контракт для работы с чатами.
type ChatRepository interface {
	GetOrCreateGameRoom(ctx context.Context, gameID uint) (*ChatRoom, error)
	GetOrCreateCaptainsRoom(ctx context.Context, gameID uint) (*ChatRoom, error)
	GetOrCreateTeamRoom(ctx context.Context, gameID, teamID, passingID uint) (*ChatRoom, error)
	GetOrCreateTeamFloodRoom(ctx context.Context, gameID, teamID, passingID uint) (*ChatRoom, error)
	GetOrCreateServerRoom(ctx context.Context) (*ChatRoom, error)
	GetByID(ctx context.Context, roomID uint) (*ChatRoom, error)
	IsTeamMemberOrCaptain(ctx context.Context, teamID, userID uint) (bool, error)
	SaveMessage(ctx context.Context, roomID, userID uint, content string) (*ChatMessage, error)
	GetMessages(ctx context.Context, roomID uint, limit int) ([]ChatMessage, error)
	// IDEA-11: ленивая подгрузка старой истории (before_id).
	GetMessagesBefore(ctx context.Context, roomID uint, beforeID uint, limit int) ([]ChatMessage, error)
	GetMessageByID(ctx context.Context, messageID uint) (*ChatMessage, error)
	// B-1/B-5 (pass 45): членство в комнате.
	AddRoomMember(ctx context.Context, roomID, userID uint, canRead, canWrite, canAttach bool) error
	GetRoomMember(ctx context.Context, roomID, userID uint) (*ChatRoomMember, error)
	// S-46-5 (pass 46): единая проверка права на отправку сообщения (hot-path чата).
	CanSendMessage(ctx context.Context, roomID uint, teamID *uint, userID uint) (allowed, memberExists bool, err error)
	// B-4 (pass 45): создание произвольных комнат автором/соавтором + список комнат игры.
	CreateRoom(ctx context.Context, room *ChatRoom) error
	ListRoomsByGame(ctx context.Context, gameID uint) ([]ChatRoom, error)
	// B-7 (pass 45): личный чат 1-на-1.
	GetOrCreatePersonalRoom(ctx context.Context, userA, userB uint) (*ChatRoom, error)
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
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id IS NULL AND passing_id IS NULL AND room_type = ?", gameID, RoomTypeGameGeneral).First(&room).Error
	if err == nil {
		return &room, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	room = ChatRoom{
		GameID:   &gameID,
		Name:     "Общий чат игры",
		RoomType: RoomTypeGameGeneral,
	}
	// Create with conflict handling — if duplicate (race), re-query
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		if err := r.db.WithContext(ctx).Where("game_id = ? AND team_id IS NULL AND passing_id IS NULL AND room_type = ?", gameID, RoomTypeGameGeneral).First(&room).Error; err != nil {
			return nil, fmt.Errorf("failed to get or create game chat room: %w", err)
		}
		return &room, nil
	}
	return &room, nil
}

// GetOrCreateCaptainsRoom возвращает комнату «только капитаны» игры (B-2).
func (r *gormChatRepo) GetOrCreateCaptainsRoom(ctx context.Context, gameID uint) (*ChatRoom, error) {
	var room ChatRoom
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id IS NULL AND passing_id IS NULL AND room_type = ?", gameID, RoomTypeGameCaptains).First(&room).Error
	if err == nil {
		return &room, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	room = ChatRoom{
		GameID:   &gameID,
		Name:     "Капитаны",
		RoomType: RoomTypeGameCaptains,
	}
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		if err := r.db.WithContext(ctx).Where("game_id = ? AND team_id IS NULL AND passing_id IS NULL AND room_type = ?", gameID, RoomTypeGameCaptains).First(&room).Error; err != nil {
			return nil, fmt.Errorf("failed to get or create captains chat room: %w", err)
		}
		return &room, nil
	}
	return &room, nil
}

func (r *gormChatRepo) GetOrCreateTeamRoom(ctx context.Context, gameID, teamID, passingID uint) (*ChatRoom, error) {
	var room ChatRoom
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ? AND passing_id = ? AND room_type = ?", gameID, teamID, passingID, RoomTypeTeamGeneral).First(&room).Error
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
		RoomType:  RoomTypeTeamGeneral,
	}
	// Create with conflict handling — if duplicate (race), re-query
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		if err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ? AND passing_id = ? AND room_type = ?", gameID, teamID, passingID, RoomTypeTeamGeneral).First(&room).Error; err != nil {
			return nil, fmt.Errorf("failed to get or create team chat room: %w", err)
		}
		return &room, nil
	}
	return &room, nil
}

// GetOrCreateTeamFloodRoom возвращает флудилку команды (B-3).
func (r *gormChatRepo) GetOrCreateTeamFloodRoom(ctx context.Context, gameID, teamID, passingID uint) (*ChatRoom, error) {
	var room ChatRoom
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ? AND passing_id = ? AND room_type = ?", gameID, teamID, passingID, RoomTypeTeamFlood).First(&room).Error
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
		Name:      "Флудилка",
		RoomType:  RoomTypeTeamFlood,
	}
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		if err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ? AND passing_id = ? AND room_type = ?", gameID, teamID, passingID, RoomTypeTeamFlood).First(&room).Error; err != nil {
			return nil, fmt.Errorf("failed to get or create team flood room: %w", err)
		}
		return &room, nil
	}
	return &room, nil
}

// GetOrCreateServerRoom возвращает общий чат всех игроков сервера (B-6).
// Единственная комната с room_type=server (без game/team).
func (r *gormChatRepo) GetOrCreateServerRoom(ctx context.Context) (*ChatRoom, error) {
	var room ChatRoom
	err := r.db.WithContext(ctx).Where("room_type = ?", RoomTypeServer).First(&room).Error
	if err == nil {
		return &room, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	room = ChatRoom{
		Name:     "Общий чат",
		RoomType: RoomTypeServer,
	}
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		if err := r.db.WithContext(ctx).Where("room_type = ?", RoomTypeServer).First(&room).Error; err != nil {
			return nil, fmt.Errorf("failed to get or create server chat room: %w", err)
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
	// DEEP-REVIEW (pass 46): Preload только id/name/avatar_path — раньше тянули
	// полные строки users (включая password_hash, email) на каждое сообщение.
	if err := r.db.WithContext(ctx).Preload("User", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, name, avatar_path")
	}).First(&msg, messageID).Error; err != nil {
		return nil, err
	}
	return &msg, nil
}

func (r *gormChatRepo) GetMessages(ctx context.Context, roomID uint, limit int) ([]ChatMessage, error) {
	var msgs []ChatMessage
	err := r.db.WithContext(ctx).Preload("User", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, name, avatar_path")
	}).
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

// GetMessagesBefore (IDEA-11): возвращает до limit сообщений СТАРШЕ beforeID
// (в порядке возрастания created_at). Используется для ленивой подгрузки
// истории («загрузить ранее») — без повторной загрузки уже показанных.
func (r *gormChatRepo) GetMessagesBefore(ctx context.Context, roomID uint, beforeID uint, limit int) ([]ChatMessage, error) {
	if beforeID == 0 {
		return r.GetMessages(ctx, roomID, limit)
	}
	var msgs []ChatMessage
	err := r.db.WithContext(ctx).Preload("User", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, name, avatar_path")
	}).
		Where("room_id = ? AND id < ?", roomID, beforeID).
		Order("created_at DESC").
		Limit(limit).
		Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	// reverse slice — возвращаем от старых к новым.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// AddRoomMember добавляет/обновляет членство в комнате (B-1/B-5, pass 45).
// Используем map, а не struct: GORM пропускает zero-value (false) поля struct
// при Create, и тогда БД применяет default:true — права «выключить» нельзя.
func (r *gormChatRepo) AddRoomMember(ctx context.Context, roomID, userID uint, canRead, canWrite, canAttach bool) error {
	return r.db.WithContext(ctx).Table("chat_room_members").
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "room_id"}, {Name: "user_id"}},
			DoUpdates: clause.Assignments(map[string]any{"can_read": canRead, "can_write": canWrite, "can_attach": canAttach}),
		}).
		Create(map[string]any{
			"room_id":    roomID,
			"user_id":    userID,
			"can_read":   canRead,
			"can_write":  canWrite,
			"can_attach": canAttach,
		}).Error
}

// GetRoomMember возвращает членство пользователя в комнате (B-5).
func (r *gormChatRepo) GetRoomMember(ctx context.Context, roomID, userID uint) (*ChatRoomMember, error) {
	var m ChatRoomMember
	err := r.db.WithContext(ctx).Where("room_id = ? AND user_id = ?", roomID, userID).First(&m).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// CanSendMessage проверяет право на отправку сообщения за минимальное число
// запросов (S-46-5, pass 46; DEEP-REVIEW P2, pass 46):
//   - один запрос комнаты;
//   - для командных комнат — один запрос членства (LEFT JOIN team_members + teams);
//   - один запрос chat_room_members (can_write).
//
// Возвращает (allowed, memberExists, err): memberExists показывает, есть ли запись
// chat_room_members (для логов), allowed — можно ли писать.
func (r *gormChatRepo) CanSendMessage(ctx context.Context, roomID uint, teamID *uint, userID uint) (allowed bool, memberExists bool, err error) {
	var room ChatRoom
	if err := r.db.WithContext(ctx).Where("id = ?", roomID).First(&room).Error; err != nil {
		return false, false, err
	}

	// Проверка членства в команде (только для командных комнат) — один запрос
	// с LEFT JOIN team_members + teams вместо COUNT + First.
	if teamID != nil && room.TeamID != nil {
		var res struct{ Member bool }
		if err := r.db.WithContext(ctx).Raw(`
			SELECT EXISTS(
				SELECT 1 FROM teams t
				LEFT JOIN team_members tm ON tm.team_id = t.id
				WHERE t.id = ? AND (tm.user_id = ? OR t.captain_id = ?)
			) AS member`, *room.TeamID, userID, userID).Scan(&res).Error; err != nil {
			return false, false, err
		}
		if !res.Member {
			return false, false, nil
		}
	}

	// Права записи в комнате: если записи нет — разрешено (общая/серверная комната).
	var member ChatRoomMember
	merr := r.db.WithContext(ctx).Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if merr == nil {
		return member.CanWrite, true, nil
	}
	if !errors.Is(merr, gorm.ErrRecordNotFound) {
		return false, false, merr
	}
	return true, false, nil
}

// CreateRoom создаёт произвольную комнату игры (B-4, pass 45).
func (r *gormChatRepo) CreateRoom(ctx context.Context, room *ChatRoom) error {
	return r.db.WithContext(ctx).Create(room).Error
}

// ListRoomsByGame возвращает все комнаты игры (кроме системных командных).
func (r *gormChatRepo) ListRoomsByGame(ctx context.Context, gameID uint) ([]ChatRoom, error) {
	var rooms []ChatRoom
	err := r.db.WithContext(ctx).
		Where("game_id = ? AND team_id IS NULL", gameID).
		Order("created_at ASC").
		Find(&rooms).Error
	return rooms, err
}

// GetOrCreatePersonalRoom возвращает/создаёт личный чат 1-на-1 (B-7, pass 45).
// Пара userA<userB нормализуется — комната уникальна независимо от порядка.
func (r *gormChatRepo) GetOrCreatePersonalRoom(ctx context.Context, userA, userB uint) (*ChatRoom, error) {
	if userA > userB {
		userA, userB = userB, userA
	}
	var room ChatRoom
	err := r.db.WithContext(ctx).
		Where("room_type = ? AND user1_id = ? AND user2_id = ?", RoomTypePersonal, userA, userB).
		First(&room).Error
	if err == nil {
		return &room, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	room = ChatRoom{
		Name:     "Личный чат",
		RoomType: RoomTypePersonal,
		User1ID:  &userA,
		User2ID:  &userB,
	}
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		if err := r.db.WithContext(ctx).
			Where("room_type = ? AND user1_id = ? AND user2_id = ?", RoomTypePersonal, userA, userB).
			First(&room).Error; err != nil {
			return nil, fmt.Errorf("failed to get or create personal chat room: %w", err)
		}
		return &room, nil
	}
	return &room, nil
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
