// internal/domain/team/repository.go
package team

import (
	"context"
	"gengine-0/internal/domain/user"

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
	AddMember(ctx context.Context, teamID, userID uint) error
	RemoveMember(ctx context.Context, teamID, userID uint) error
	ChangeCaptain(ctx context.Context, teamID, newCaptainID uint) error
	IsMember(ctx context.Context, teamID, userID uint) (bool, error)
	GetPassingByTeam(ctx context.Context, teamID uint) (*teamGamePassing, error)
	GetUserByID(ctx context.Context, userID uint) (*userUser, error)
	GetGameByID(ctx context.Context, gameID uint) (*teamGameModel, error)
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
	var captainTeams []Team
	if err := r.db.WithContext(ctx).Where("captain_id = ?", userID).Find(&captainTeams).Error; err != nil {
		return nil, err
	}
	teams = append(teams, captainTeams...)
	var memberTeamIDs []uint
	if err := r.db.WithContext(ctx).Table("team_members").Where("user_id = ?", userID).Pluck("team_id", &memberTeamIDs).Error; err != nil {
		return teams, err
	}
	if len(memberTeamIDs) > 0 {
		var memberTeams []Team
		if err := r.db.WithContext(ctx).Where("id IN ? AND captain_id != ?", memberTeamIDs, userID).Find(&memberTeams).Error; err != nil {
			return teams, err
		}
		teams = append(teams, memberTeams...)
	}
	return teams, nil
}
func (r *gormTeamRepo) Update(ctx context.Context, team *Team) error {
	return r.db.WithContext(ctx).Save(team).Error
}
func (r *gormTeamRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Team{}, id).Error
}
func (r *gormTeamRepo) AddMember(ctx context.Context, teamID, userID uint) error {
	return r.db.WithContext(ctx).Exec("INSERT INTO team_members (team_id, user_id) VALUES (?, ?)", teamID, userID).Error
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
	err := r.db.WithContext(ctx).Model(&user.User{}).Where("id NOT IN (?)", subQuery).Find(&users).Error
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
