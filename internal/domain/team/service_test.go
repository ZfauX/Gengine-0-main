// internal/domain/team/service_test.go
package team_test

import (
	"context"
	"testing"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTeamDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testutil.SetupPostgresDB(t,
		&team.Team{}, &team.Invitation{},
		&user.User{},
		&game.Game{}, &game.GamePassing{}, &game.CoAuthor{},
	)
}

// ---------- TeamService ----------

func TestTeamService_CreateTeam(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	tm, err := ts.CreateTeam(context.Background(), "Dream Team", cap.ID)
	require.NoError(t, err)
	assert.Equal(t, "Dream Team", tm.Name)
	assert.Equal(t, cap.ID, tm.CaptainID)
}

func TestTeamService_AddMember_ByCaptain(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	member := createUser(t, db, "mem@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Test", cap.ID)

	err := ts.AddMember(context.Background(), tm.ID, member.ID, cap.ID)
	require.NoError(t, err)

	var count int64
	db.Table("team_members").Where("team_id = ? AND user_id = ?", tm.ID, member.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestTeamService_AddMember_NotCaptain(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	member := createUser(t, db, "mem@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Test", cap.ID)

	err := ts.AddMember(context.Background(), tm.ID, member.ID, other.ID)
	assert.Error(t, err)
}

func TestTeamService_RemoveMember(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	member := createUser(t, db, "mem@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Test", cap.ID)
	_ = ts.AddMember(context.Background(), tm.ID, member.ID, cap.ID)

	err := ts.RemoveMember(context.Background(), tm.ID, member.ID, cap.ID)
	require.NoError(t, err)

	var count int64
	db.Table("team_members").Where("team_id = ? AND user_id = ?", tm.ID, member.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestTeamService_RemoveCaptain(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Test", cap.ID)

	err := ts.RemoveMember(context.Background(), tm.ID, cap.ID, cap.ID)
	assert.Error(t, err)
}

func TestTeamService_ChangeCaptain(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	oldCap := createUser(t, db, "old@test.com", "pass")
	newCap := createUser(t, db, "new@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Test", oldCap.ID)
	_ = ts.AddMember(context.Background(), tm.ID, newCap.ID, oldCap.ID)

	err := ts.ChangeCaptain(context.Background(), tm.ID, newCap.ID, oldCap.ID)
	require.NoError(t, err)

	var updated team.Team
	db.First(&updated, tm.ID)
	assert.Equal(t, newCap.ID, updated.CaptainID)
}

func TestTeamService_ChangeCaptain_NewNotMember(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	oldCap := createUser(t, db, "old@test.com", "pass")
	newCap := createUser(t, db, "new@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Test", oldCap.ID)

	err := ts.ChangeCaptain(context.Background(), tm.ID, newCap.ID, oldCap.ID)
	assert.Error(t, err)
}

func TestTeamService_CanManageTeam(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	member := createUser(t, db, "mem@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Test", cap.ID)
	_ = ts.AddMember(context.Background(), tm.ID, member.ID, cap.ID)

	assert.True(t, ts.CanManageTeam(context.Background(), tm.ID, cap.ID))
	assert.False(t, ts.CanManageTeam(context.Background(), tm.ID, member.ID))
}

func TestTeamService_GetMyTeams(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	u1 := createUser(t, db, "user1@test.com", "pass")
	u2 := createUser(t, db, "user2@test.com", "pass")

	tmA, _ := ts.CreateTeam(context.Background(), "Team A", u1.ID)
	tmB, _ := ts.CreateTeam(context.Background(), "Team B", u2.ID)
	_ = ts.AddMember(context.Background(), tmB.ID, u1.ID, u2.ID)

	teams, err := ts.GetMyTeams(context.Background(), u1.ID)
	require.NoError(t, err)
	assert.Len(t, teams, 2)

	ids := []uint{teams[0].ID, teams[1].ID}
	assert.Contains(t, ids, tmA.ID)
	assert.Contains(t, ids, tmB.ID)
}

// ---------- InvitationService ----------

func TestInvitationService_Create(t *testing.T) {
	db := setupTeamDB(t)
	ts, invSvc := newTeamAndInvitationServices(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	invited := createUser(t, db, "inv@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Inv Team", cap.ID)

	inv, err := invSvc.CreateInvitation(context.Background(), tm.ID, invited.ID, cap.ID)
	require.NoError(t, err)
	assert.Equal(t, team.InvitationPending, inv.Status)
	assert.Equal(t, invited.ID, inv.UserID)
}

func TestInvitationService_Accept(t *testing.T) {
	db := setupTeamDB(t)
	ts, invSvc := newTeamAndInvitationServices(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	invited := createUser(t, db, "inv@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Inv Team", cap.ID)

	inv, _ := invSvc.CreateInvitation(context.Background(), tm.ID, invited.ID, cap.ID)

	err := invSvc.AcceptInvitation(context.Background(), inv.ID, invited.ID)
	require.NoError(t, err)

	var updated team.Invitation
	db.First(&updated, inv.ID)
	assert.Equal(t, team.InvitationAccepted, updated.Status)

	var count int64
	db.Table("team_members").Where("team_id = ? AND user_id = ?", tm.ID, invited.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestInvitationService_Decline(t *testing.T) {
	db := setupTeamDB(t)
	ts, invSvc := newTeamAndInvitationServices(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	invited := createUser(t, db, "inv@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Inv Team", cap.ID)

	inv, _ := invSvc.CreateInvitation(context.Background(), tm.ID, invited.ID, cap.ID)

	err := invSvc.DeclineInvitation(context.Background(), inv.ID, invited.ID)
	require.NoError(t, err)

	var updated team.Invitation
	db.First(&updated, inv.ID)
	assert.Equal(t, team.InvitationDeclined, updated.Status)
}

// ---------- Вспомогательные функции ----------

// gameAuthorizerStub — заглушка для middleware.GameAuthorizer.
type gameAuthorizerStub struct {
	db *gorm.DB
}

func (g *gameAuthorizerStub) IsUserManager(gameID, userID uint) (bool, error) {
	var ga game.Game
	if err := g.db.First(&ga, gameID).Error; err != nil {
		return false, err
	}
	return ga.AuthorID == userID, nil
}

func (g *gameAuthorizerStub) HasPermission(gameID, userID uint, role string) (bool, error) {
	return g.IsUserManager(gameID, userID)
}

func newTeamService(db *gorm.DB) *team.TeamService {
	teamRepo := team.NewGormTeamRepo(db)
	authorizer := &gameAuthorizerStub{db}
	return team.NewTeamService(teamRepo, authorizer)
}

func newTeamAndInvitationServices(db *gorm.DB) (*team.TeamService, *team.InvitationService) {
	teamRepo := team.NewGormTeamRepo(db)
	invRepo := team.NewGormInvitationRepo(db)
	authorizer := &gameAuthorizerStub{db}
	cfg := &config.Config{}
	ts := team.NewTeamService(teamRepo, authorizer)
	invSvc := team.NewInvitationService(invRepo, teamRepo, authorizer, cfg)
	return ts, invSvc
}

func createUser(t *testing.T, db *gorm.DB, email, _ string) *user.User {
	t.Helper()
	u := &user.User{Email: email, Password: "hashed", Name: email}
	require.NoError(t, db.Create(u).Error)
	return u
}
