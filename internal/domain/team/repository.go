// internal/domain/team/repository.go
package team

import (
	"context"

	"gorm.io/gorm"
)

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
	return &t, err
}
func (r *gormTeamRepo) GetByIDWithMembers(ctx context.Context, id uint) (*Team, error) {
	var t Team
	err := r.db.WithContext(ctx).Preload("Captain").Preload("Members").First(&t, id).Error
	return &t, err
}
func (r *gormTeamRepo) GetByCaptainID(ctx context.Context, captainID uint) ([]Team, error) {
	var teams []Team
	err := r.db.WithContext(ctx).Where("captain_id = ?", captainID).Find(&teams).Error
	return teams, err
}
func (r *gormTeamRepo) GetTeamsByUserID(ctx context.Context, userID uint) ([]Team, error) {
	var teams []Team
	var captainTeams []Team
	r.db.WithContext(ctx).Where("captain_id = ?", userID).Find(&captainTeams)
	teams = append(teams, captainTeams...)
	var memberTeamIDs []uint
	r.db.WithContext(ctx).Table("team_members").Where("user_id = ?", userID).Pluck("team_id", &memberTeamIDs)
	if len(memberTeamIDs) > 0 {
		var memberTeams []Team
		r.db.WithContext(ctx).Where("id IN ? AND captain_id != ?", memberTeamIDs, userID).Find(&memberTeams)
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

type gormInvitationRepo struct{ db *gorm.DB }

func NewGormInvitationRepo(db *gorm.DB) InvitationRepository { return &gormInvitationRepo{db} }

func (r *gormInvitationRepo) Create(ctx context.Context, inv *Invitation) error {
	return r.db.WithContext(ctx).Create(inv).Error
}
func (r *gormInvitationRepo) GetByID(ctx context.Context, id uint) (*Invitation, error) {
	var inv Invitation
	err := r.db.WithContext(ctx).First(&inv, id).Error
	return &inv, err
}
func (r *gormInvitationRepo) GetByTeamAndUser(ctx context.Context, teamID, userID uint) (*Invitation, error) {
	var inv Invitation
	err := r.db.WithContext(ctx).Where("team_id = ? AND user_id = ?", teamID, userID).First(&inv).Error
	return &inv, err
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
