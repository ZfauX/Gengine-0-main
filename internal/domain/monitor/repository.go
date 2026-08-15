// internal/domain/monitor/repository.go
package monitor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	GetOrCreatePersonalRoom(ctx context.Context, userA, userB uint) (*ChatRoom, bool, error)
	// DeleteRoom (PASS-6 P4): удаление комнаты (используется для отката при
	// создании личного чата с несуществующим собеседником).
	DeleteRoom(ctx context.Context, roomID uint) error
	// AcceptPersonalRoom (DEEP-REVIEW PASS-6 M7): получатель (userID) даёт
	// согласие на личный чат. Возвращает false, если userID не участник
	// комнаты или комната не личная.
	AcceptPersonalRoom(ctx context.Context, roomID, userID uint) (bool, error)
	// GetAcceptedStatus (M2, PASS-7): свежее значение Accepted для личной
	// комнаты. WS-соединение загружает комнату один раз при подключении —
	// если получатель принял чат ПОСЛЕ открытия сокета, стейт устаревает.
	GetAcceptedStatus(ctx context.Context, roomID uint) (bool, error)
	// InvalidateTeamPermCache (M2, PASS-8): сбрасывает perm-кэш права на
	// отправку для ВСЕХ комнат команды (вызывается при изменении членства —
	// удалении участника/смене капитана). Раньше кэш жил до TTL 5с, и
	// исключённый из команды продолжал писать в чат до 5 секунд.
	InvalidateTeamPermCache(ctx context.Context, teamID uint) error
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

// chatPermCacheTTL (DEEP-REVIEW PASS-3 P5): короткий TTL кэша права на отправку
// сообщения — убирает 2-3 запроса на КАЖДОЕ WS-сообщение чата. Права меняются
// редко (AddRoomMember), задержка применения ≤ TTL приемлема.
const chatPermCacheTTL = 5 * time.Second

// chatPermCacheMaxEntries (DEEP-REVIEW PASS-4 M8): верхняя граница размера кэша —
// lazy sweep при превышении, чтобы map не росла бесконечно (медленная утечка).
const chatPermCacheMaxEntries = 10000

type chatPermEntry struct {
	allowed      bool
	memberExists bool
	expires      time.Time
}

type gormChatRepo struct {
	db *gorm.DB

	permCacheMu sync.Mutex
	permCache   map[string]chatPermEntry
	// lastSweep (PASS-5 M1): sweep не чаще 1 раза в секунду — при переполнении
	// кэша каждый промах не платит O(n) под локом на горячем пути чата.
	lastSweep time.Time
}

func NewGormChatRepo(db *gorm.DB) ChatRepository {
	return &gormChatRepo{db: db, permCache: make(map[string]chatPermEntry)}
}

func (r *gormChatRepo) permCacheKey(roomID, userID uint) string {
	return strconv.FormatUint(uint64(roomID), 10) + ":" + strconv.FormatUint(uint64(userID), 10)
}

// invalidatePermCache сбрасывает кэш права пользователя в комнате (вызывается
// при изменении членства/прав).
func (r *gormChatRepo) invalidatePermCache(roomID, userID uint) {
	r.permCacheMu.Lock()
	delete(r.permCache, r.permCacheKey(roomID, userID))
	r.permCacheMu.Unlock()
}

// sweepPermCache (M8/PASS-4, M1/PASS-5, L6/PASS-6): удаляет истёкшие записи
// при превышении размера, но НЕ чаще 1 раза в секунду. Если после удаления
// истёкших размер ВСЁ ЕЩЁ превышает cap (всплеск свежих записей) — удаляем
// лишние, чтобы map не росла бесконечно (старые по времени истечения).
func (r *gormChatRepo) sweepPermCache() {
	if len(r.permCache) <= chatPermCacheMaxEntries {
		return
	}
	now := time.Now()
	if now.Sub(r.lastSweep) < time.Second {
		return
	}
	r.lastSweep = now
	for k, e := range r.permCache {
		if now.After(e.expires) {
			delete(r.permCache, k)
		}
	}
	// L6: принудительный cap — удаляем до размера max (самые скорые к истечению).
	// M10 (PASS-17): один проход + sort вместо вложенного цикла (O(n²) при
	// всплеске свежих записей на горячем пути чата).
	if len(r.permCache) > chatPermCacheMaxEntries {
		type entry struct {
			key string
			exp time.Time
		}
		all := make([]entry, 0, len(r.permCache))
		for k, e := range r.permCache {
			all = append(all, entry{key: k, exp: e.expires})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].exp.Before(all[j].exp) })
		toRemove := len(all) - chatPermCacheMaxEntries
		for i := 0; i < toRemove; i++ {
			delete(r.permCache, all[i].key)
		}
	}
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
	err := r.db.WithContext(ctx).Table("chat_room_members").
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
	if err != nil {
		return err
	}
	// P5 (PASS-3): права изменились — сбрасываем кэш права на отправку.
	r.invalidatePermCache(roomID, userID)
	return nil
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
	// DEEP-REVIEW PASS-3 P5: короткий TTL-кэш (5с) — на горячем пути чата
	// (каждое WS-сообщение) не делаем 2-3 запроса к БД.
	now := time.Now()
	ck := r.permCacheKey(roomID, userID)
	r.permCacheMu.Lock()
	if e, ok := r.permCache[ck]; ok && now.Before(e.expires) {
		r.permCacheMu.Unlock()
		return e.allowed, e.memberExists, nil
	}
	r.permCacheMu.Unlock()

	allowed, memberExists, err = r.canSendMessageDB(ctx, roomID, teamID, userID)
	if err != nil {
		return false, false, err
	}

	r.permCacheMu.Lock()
	r.sweepPermCache()
	r.permCache[ck] = chatPermEntry{allowed: allowed, memberExists: memberExists, expires: now.Add(chatPermCacheTTL)}
	r.permCacheMu.Unlock()
	return allowed, memberExists, nil
}

// canSendMessageDB — оригинальная логика проверки права на отправку (без кэша).
func (r *gormChatRepo) canSendMessageDB(ctx context.Context, roomID uint, teamID *uint, userID uint) (allowed bool, memberExists bool, err error) {
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
// GetOrCreatePersonalRoom возвращает/создаёт личный чат 1-на-1 (B-7, pass 45).
// Пара userA<userB нормализуется — комната уникальна независимо от порядка.
// Возвращает created=true, если комната была создана этим вызовом
// (DEEP-REVIEW PASS-6 P4: позволяет хендлеру проверить существование
// собеседника ТОЛЬКО при создании, не тратя лишний GetByID на каждую страницу).
func (r *gormChatRepo) GetOrCreatePersonalRoom(ctx context.Context, initiatorID, otherID uint) (*ChatRoom, bool, error) {
	userA, userB := initiatorID, otherID
	if userA > userB {
		userA, userB = userB, userA
	}
	var room ChatRoom
	err := r.db.WithContext(ctx).
		Where("room_type = ? AND user1_id = ? AND user2_id = ?", RoomTypePersonal, userA, userB).
		First(&room).Error
	if err == nil {
		return &room, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	room = ChatRoom{
		Name:     "Личный чат",
		RoomType: RoomTypePersonal,
		User1ID:  &userA,
		User2ID:  &userB,
		// M7 (PASS-6): инициатор (создатель) — OwnerID; согласие получателя
		// (User2ID) изначально не дано — он должен принять переписку.
		OwnerID:  &initiatorID,
		Accepted: false,
	}
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		if err := r.db.WithContext(ctx).
			Where("room_type = ? AND user1_id = ? AND user2_id = ?", RoomTypePersonal, userA, userB).
			First(&room).Error; err != nil {
			return nil, false, fmt.Errorf("failed to get or create personal chat room: %w", err)
		}
		return &room, false, nil
	}
	return &room, true, nil
}

// DeleteRoom удаляет комнату (PASS-6 P4: откат при создании с несуществующим
// собеседником; soft-delete — сообщения сохраняются, комната скрывается).
func (r *gormChatRepo) DeleteRoom(ctx context.Context, roomID uint) error {
	return r.db.WithContext(ctx).Delete(&ChatRoom{}, roomID).Error
}

// AcceptPersonalRoom (M7, PASS-6; H1, PASS-7): согласие на личный чат даёт
// ПОЛУЧАТЕЛЬ — участник, не являющийся владельцем (OwnerID). Раньше условие
// было по user2_id, но GetOrCreatePersonalRoom нормализует пару (user1=min,
// user2=max), поэтому получатель может быть и user1, а владелец — user2
// (инициатор с большим ID мог бы принять сам себя — обход консента).
func (r *gormChatRepo) AcceptPersonalRoom(ctx context.Context, roomID, userID uint) (bool, error) {
	res := r.db.WithContext(ctx).Model(&ChatRoom{}).
		Where("id = ? AND room_type = ? AND (owner_id IS NULL OR owner_id <> ?) AND (user1_id = ? OR user2_id = ?)",
			roomID, RoomTypePersonal, userID, userID, userID).
		Update("accepted", true)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// GetAcceptedStatus (M2, PASS-7): SELECT только accepted для личной комнаты.
func (r *gormChatRepo) GetAcceptedStatus(ctx context.Context, roomID uint) (bool, error) {
	var accepted bool
	err := r.db.WithContext(ctx).Model(&ChatRoom{}).
		Where("id = ? AND room_type = ?", roomID, RoomTypePersonal).
		Select("accepted").Scan(&accepted).Error
	if err != nil {
		return false, err
	}
	return accepted, nil
}

// InvalidateTeamPermCache (M2, PASS-8): сбрасывает perm-кэш всех комнат команды.
// Вызывается TeamService при изменении членства (RemoveMember/LeaveMember/смена
// капитана) — иначе исключённый участник продолжал писать в чат до TTL (5с).
func (r *gormChatRepo) InvalidateTeamPermCache(ctx context.Context, teamID uint) error {
	var roomIDs []uint
	if err := r.db.WithContext(ctx).Model(&ChatRoom{}).
		Where("team_id = ?", teamID).
		Pluck("id", &roomIDs).Error; err != nil {
		return err
	}
	if len(roomIDs) == 0 {
		return nil
	}
	r.permCacheMu.Lock()
	for _, roomID := range roomIDs {
		prefix := strconv.FormatUint(uint64(roomID), 10) + ":"
		for k := range r.permCache {
			if strings.HasPrefix(k, prefix) {
				delete(r.permCache, k)
			}
		}
	}
	r.permCacheMu.Unlock()
	return nil
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

// IsTeamMember проверяет членство пользователя в команде (включая капитана —
// DEEP-REVIEW PASS-4 M10: раньше капитан, не входящий в team_members, не мог
// голосовать; в чате это учтено в IsTeamMemberOrCaptain).
func (r *gormBlackboxRepo) IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Table("team_members").
		Where("team_id = ? AND user_id = ?", teamID, userID).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	// Капитан может не быть в team_members.
	var captainCount int64
	if err := r.db.WithContext(ctx).Table("teams").
		Where("id = ? AND captain_id = ?", teamID, userID).Count(&captainCount).Error; err != nil {
		return false, err
	}
	return captainCount > 0, nil
}
