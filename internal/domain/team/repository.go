// internal/domain/team/repository.go
package team

import (
	"context"
	"strings"

	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/i18n"
	"gengine-0/internal/pkg/sqlutil"

	"gorm.io/gorm"
)

// Локальные модели для запросов к внешним таблицам.
type teamGamePassing struct {
	ID     uint
	GameID uint
	TeamID uint
}

func (teamGamePassing) TableName() string { return "game_passings" }

type teamGameModel struct {
	ID       uint
	AuthorID uint
}

func (teamGameModel) TableName() string { return "games" }

type userUser struct {
	ID    uint
	Email string
}

func (userUser) TableName() string { return "users" }

type TeamRepository interface {
	Create(ctx context.Context, team *Team) error
	GetByID(ctx context.Context, id uint) (*Team, error)
	GetByIDWithMembers(ctx context.Context, id uint) (*Team, error)
	GetByCaptainID(ctx context.Context, captainID uint) ([]Team, error)
	GetTeamsByUserID(ctx context.Context, userID uint) ([]Team, error)
	Update(ctx context.Context, team *Team) error
	Delete(ctx context.Context, id uint) error
	Count(ctx context.Context) (int64, error)
	CountSearch(ctx context.Context, query string) (int64, error)
	ListAllPaginated(ctx context.Context, offset, limit int) ([]Team, error)
	SearchPaginated(ctx context.Context, query string, offset, limit int) ([]Team, error)
	AddMember(ctx context.Context, teamID, userID uint) error
	RemoveMember(ctx context.Context, teamID, userID uint) error
	ChangeCaptain(ctx context.Context, teamID, newCaptainID uint) error
	IsMember(ctx context.Context, teamID, userID uint) (bool, error)
	// ListByIDs возвращает команды по списку ID (A-M2, pass 34: для
	// notify-чтений GameAdminService вместо raw s.db).
	ListByIDs(ctx context.Context, ids []uint) ([]Team, error)
	GetPassingByTeam(ctx context.Context, teamID uint) (*teamGamePassing, error)
	GetUserByID(ctx context.Context, userID uint) (*userUser, error)
	GetGameByID(ctx context.Context, gameID uint) (*teamGameModel, error)
	GetOrCreateTeamChatRoom(ctx context.Context, teamID uint, teamName string) (*teamChatRoom, error)
	SearchUsersForInvitation(ctx context.Context, query string, teamID uint) ([]struct {
		ID   uint
		Name string
	}, error)
	TeamMembersCount(ctx context.Context) (int64, error)
	BeginTransaction(ctx context.Context) *gorm.DB
	DeclineInvitation(ctx context.Context, id uint) error
	GetAvailableUsers(ctx context.Context, teamID uint) ([]user.User, error)
}

type InvitationRepository interface {
	Create(ctx context.Context, inv *Invitation) error
	GetByID(ctx context.Context, id uint) (*Invitation, error)
	GetByTeamAndUser(ctx context.Context, teamID, userID uint) (*Invitation, error)
	ListByTeam(ctx context.Context, teamID uint) ([]Invitation, error)
	ListPendingByUser(ctx context.Context, userID uint) ([]Invitation, error)
	UpdateStatus(ctx context.Context, id uint, status InvitationStatus) error
	Delete(ctx context.Context, id uint) error
}

type gormTeamRepo struct{ db *gorm.DB }

func NewGormTeamRepo(db *gorm.DB) TeamRepository { return &gormTeamRepo{db} }

func (r *gormTeamRepo) Create(ctx context.Context, team *Team) error {
	return r.db.WithContext(ctx).Create(team).Error
}
func (r *gormTeamRepo) GetByID(ctx context.Context, id uint) (*Team, error) {
	var t Team
	err := r.db.WithContext(ctx).First(&t, id).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormTeamRepo) GetByIDWithMembers(ctx context.Context, id uint) (*Team, error) {
	var t Team
	err := r.db.WithContext(ctx).Preload("Captain").Preload("Members").First(&t, id).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormTeamRepo) GetByCaptainID(ctx context.Context, captainID uint) ([]Team, error) {
	var teams []Team
	err := r.db.WithContext(ctx).Where("captain_id = ?", captainID).Find(&teams).Error
	return teams, err
}
func (r *gormTeamRepo) GetTeamsByUserID(ctx context.Context, userID uint) ([]Team, error) {
	var teams []Team
	// F-5 (pass 34): один UNION вместо 3 последовательных запросов
	// (капитанские команды + команды-членства).
	err := r.db.WithContext(ctx).
		Raw(`
			SELECT t.* FROM teams t WHERE t.captain_id = ? AND t.deleted_at IS NULL
			UNION
			SELECT t.* FROM teams t
			JOIN team_members tm ON tm.team_id = t.id
			WHERE tm.user_id = ? AND t.captain_id != ? AND t.deleted_at IS NULL
		`, userID, userID, userID).
		Scan(&teams).Error
	return teams, err
}
func (r *gormTeamRepo) Update(ctx context.Context, team *Team) error {
	return r.db.WithContext(ctx).Save(team).Error
}

// ListByIDs возвращает команды по списку ID (A-M2, pass 34).
func (r *gormTeamRepo) ListByIDs(ctx context.Context, ids []uint) ([]Team, error) {
	var teams []Team
	if len(ids) == 0 {
		return teams, nil
	}
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&teams).Error
	return teams, err
}
func (r *gormTeamRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Team{}, id).Error
}
func (r *gormTeamRepo) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Team{}).Count(&count).Error
	return count, err
}
func (r *gormTeamRepo) CountSearch(ctx context.Context, query string) (int64, error) {
	var count int64
	like := sqlutil.BuildLikePattern(query)
	err := r.db.WithContext(ctx).Model(&Team{}).
		Joins("LEFT JOIN users ON users.id = teams.captain_id").
		Where("teams.name ILIKE ? OR users.name ILIKE ?", like, like).
		Count(&count).Error
	return count, err
}
func (r *gormTeamRepo) ListAllPaginated(ctx context.Context, offset, limit int) ([]Team, error) {
	var teams []Team
	err := r.db.WithContext(ctx).Preload("Captain").Offset(offset).Limit(limit).Order("id DESC").Find(&teams).Error
	return teams, err
}
func (r *gormTeamRepo) SearchPaginated(ctx context.Context, query string, offset, limit int) ([]Team, error) {
	var teams []Team
	like := sqlutil.BuildLikePattern(query)
	err := r.db.WithContext(ctx).Preload("Captain").
		Joins("LEFT JOIN users ON users.id = teams.captain_id").
		Where("teams.name ILIKE ? OR users.name ILIKE ?", like, like).
		Offset(offset).Limit(limit).Order("id DESC").
		Find(&teams).Error
	return teams, err
}
func (r *gormTeamRepo) AddMember(ctx context.Context, teamID, userID uint) error {
	// C-9: ON CONFLICT DO NOTHING — два параллельных AddMember не дадут дубликат.
	return r.db.WithContext(ctx).Exec(
		"INSERT INTO team_members (team_id, user_id) VALUES (?, ?) ON CONFLICT DO NOTHING", teamID, userID,
	).Error
}
func (r *gormTeamRepo) RemoveMember(ctx context.Context, teamID, userID uint) error {
	return r.db.WithContext(ctx).Exec("DELETE FROM team_members WHERE team_id = ? AND user_id = ?", teamID, userID).Error
}
func (r *gormTeamRepo) ChangeCaptain(ctx context.Context, teamID, newCaptainID uint) error {
	return r.db.WithContext(ctx).Model(&Team{}).Where("id = ?", teamID).Update("captain_id", newCaptainID).Error
}
func (r *gormTeamRepo) IsMember(ctx context.Context, teamID, userID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("team_members").Where("team_id = ? AND user_id = ?", teamID, userID).Count(&count).Error
	return count > 0, err
}
func (r *gormTeamRepo) GetPassingByTeam(ctx context.Context, teamID uint) (*teamGamePassing, error) {
	var passing teamGamePassing
	err := r.db.WithContext(ctx).Where("team_id = ?", teamID).First(&passing).Error
	if err != nil {
		return nil, err
	}
	return &passing, nil
}
func (r *gormTeamRepo) GetUserByID(ctx context.Context, userID uint) (*userUser, error) {
	var u userUser
	err := r.db.WithContext(ctx).First(&u, userID).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}
func (r *gormTeamRepo) GetGameByID(ctx context.Context, gameID uint) (*teamGameModel, error) {
	var g teamGameModel
	err := r.db.WithContext(ctx).First(&g, gameID).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// SearchUsersForInvitation ищет пользователей по имени/email и исключает уже
// состоящих в команде и её капитана (C1 — без *gorm.DB в хендлере).
func (r *gormTeamRepo) SearchUsersForInvitation(ctx context.Context, query string, teamID uint) ([]struct {
	ID   uint
	Name string
}, error) {
	items := []struct {
		ID   uint
		Name string
	}{}
	// Экранируем wildcard-символы LIKE (C-M7): иначе запрос с % или _ искал
	// бы всех пользователей. Пользовательский ввод — только как литерал.
	escaped := sqlutil.EscapeLike(strings.ToLower(query))
	err := r.db.WithContext(ctx).Table("users").
		Select("id, name").
		Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ?",
			"%"+escaped+"%", "%"+escaped+"%").
		Where("id NOT IN (SELECT user_id FROM team_members WHERE team_id = ?)", teamID).
		Where("id != (SELECT captain_id FROM teams WHERE id = ?)", teamID).
		Limit(20).
		Scan(&items).Error
	return items, err
}

// GetOrCreateTeamChatRoom находит или создаёт общую комнату чата команды (C1).
func (r *gormTeamRepo) GetOrCreateTeamChatRoom(ctx context.Context, teamID uint, teamName string) (*teamChatRoom, error) {
	var room teamChatRoom
	err := r.db.WithContext(ctx).Where("team_id = ? AND game_id IS NULL", teamID).First(&room).Error
	if err == nil {
		return &room, nil
	}
	room = teamChatRoom{
		TeamID: &teamID,
		Name:   i18n.TF("team.chat_room_name", teamName),
	}
	if createErr := r.db.WithContext(ctx).Create(&room).Error; createErr != nil {
		// Конкурентное создание комнаты (C-M5): перечитываем существующую.
		var existing teamChatRoom
		if reErr := r.db.WithContext(ctx).Where("team_id = ? AND game_id IS NULL", teamID).First(&existing).Error; reErr == nil {
			return &existing, nil
		}
		return nil, createErr
	}
	return &room, nil
}
func (r *gormTeamRepo) TeamMembersCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("team_members").Count(&count).Error
	return count, err
}
func (r *gormTeamRepo) BeginTransaction(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).Begin()
}
func (r *gormTeamRepo) DeclineInvitation(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Model(&Invitation{}).Where("id = ?", id).Update("status", InvitationDeclined).Error
}
func (r *gormTeamRepo) GetAvailableUsers(ctx context.Context, teamID uint) ([]user.User, error) {
	var users []user.User
	subQuery := r.db.WithContext(ctx).Table("team_members").Select("user_id").Where("team_id = ?", teamID)
	// P-5: лимитируем выборку — не тянем всю таблицу пользователей.
	err := r.db.WithContext(ctx).Model(&user.User{}).Where("id NOT IN (?)", subQuery).Limit(100).Find(&users).Error
	return users, err
}

type gormInvitationRepo struct{ db *gorm.DB }

func NewGormInvitationRepo(db *gorm.DB) InvitationRepository { return &gormInvitationRepo{db} }

func (r *gormInvitationRepo) Create(ctx context.Context, inv *Invitation) error {
	return r.db.WithContext(ctx).Create(inv).Error
}
func (r *gormInvitationRepo) GetByID(ctx context.Context, id uint) (*Invitation, error) {
	var inv Invitation
	err := r.db.WithContext(ctx).First(&inv, id).Error
	if err != nil {
		return nil, err
	}
	return &inv, nil
}
func (r *gormInvitationRepo) GetByTeamAndUser(ctx context.Context, teamID, userID uint) (*Invitation, error) {
	var inv Invitation
	err := r.db.WithContext(ctx).Where("team_id = ? AND user_id = ?", teamID, userID).First(&inv).Error
	if err != nil {
		return nil, err
	}
	return &inv, nil
}
func (r *gormInvitationRepo) ListByTeam(ctx context.Context, teamID uint) ([]Invitation, error) {
	var invs []Invitation
	err := r.db.WithContext(ctx).Preload("User").Where("team_id = ?", teamID).Find(&invs).Error
	return invs, err
}
func (r *gormInvitationRepo) ListPendingByUser(ctx context.Context, userID uint) ([]Invitation, error) {
	var invs []Invitation
	err := r.db.WithContext(ctx).Preload("Team").Where("user_id = ? AND status = ?", userID, InvitationPending).Find(&invs).Error
	return invs, err
}
func (r *gormInvitationRepo) UpdateStatus(ctx context.Context, id uint, status InvitationStatus) error {
	return r.db.WithContext(ctx).Model(&Invitation{}).Where("id = ?", id).Update("status", status).Error
}
func (r *gormInvitationRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Invitation{}, id).Error
}
