// internal/domain/team/service_test.go
package team_test

import (
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
	ts := team.NewTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	tm, err := ts.CreateTeam("Dream Team", cap.ID)
	require.NoError(t, err)
	assert.Equal(t, "Dream Team", tm.Name)
	assert.Equal(t, cap.ID, tm.CaptainID)
}

func TestTeamService_AddMember_ByCaptain(t *testing.T) {
	db := setupTeamDB(t)
	ts := team.NewTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	member := createUser(t, db, "mem@test.com", "pass")
	tm, _ := ts.CreateTeam("Test", cap.ID)

	err := ts.AddMember(tm.ID, member.ID, cap.ID)
	require.NoError(t, err)

	var count int64
	db.Table("team_members").Where("team_id = ? AND user_id = ?", tm.ID, member.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestTeamService_AddMember_NotCaptain(t *testing.T) {
	db := setupTeamDB(t)
	ts := team.NewTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	member := createUser(t, db, "mem@test.com", "pass")
	other := createUser(t, db, "other@test.com", "pass")
	tm, _ := ts.CreateTeam("Test", cap.ID)

	err := ts.AddMember(tm.ID, member.ID, other.ID)
	assert.Error(t, err)
}

func TestTeamService_RemoveMember(t *testing.T) {
	db := setupTeamDB(t)
	ts := team.NewTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	member := createUser(t, db, "mem@test.com", "pass")
	tm, _ := ts.CreateTeam("Test", cap.ID)
	_ = ts.AddMember(tm.ID, member.ID, cap.ID)

	err := ts.RemoveMember(tm.ID, member.ID, cap.ID)
	require.NoError(t, err)

	var count int64
	db.Table("team_members").Where("team_id = ? AND user_id = ?", tm.ID, member.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestTeamService_RemoveCaptain(t *testing.T) {
	db := setupTeamDB(t)
	ts := team.NewTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	tm, _ := ts.CreateTeam("Test", cap.ID)

	err := ts.RemoveMember(tm.ID, cap.ID, cap.ID)
	assert.Error(t, err)
}

func TestTeamService_ChangeCaptain(t *testing.T) {
	db := setupTeamDB(t)
	ts := team.NewTeamService(db)

	oldCap := createUser(t, db, "old@test.com", "pass")
	newCap := createUser(t, db, "new@test.com", "pass")
	tm, _ := ts.CreateTeam("Test", oldCap.ID)
	_ = ts.AddMember(tm.ID, newCap.ID, oldCap.ID)

	err := ts.ChangeCaptain(tm.ID, newCap.ID, oldCap.ID)
	require.NoError(t, err)

	var updated team.Team
	db.First(&updated, tm.ID)
	assert.Equal(t, newCap.ID, updated.CaptainID)
}

func TestTeamService_ChangeCaptain_NewNotMember(t *testing.T) {
	db := setupTeamDB(t)
	ts := team.NewTeamService(db)

	oldCap := createUser(t, db, "old@test.com", "pass")
	newCap := createUser(t, db, "new@test.com", "pass")
	tm, _ := ts.CreateTeam("Test", oldCap.ID)

	err := ts.ChangeCaptain(tm.ID, newCap.ID, oldCap.ID)
	assert.Error(t, err)
}

func TestTeamService_CanManageTeam(t *testing.T) {
	db := setupTeamDB(t)
	ts := team.NewTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	member := createUser(t, db, "mem@test.com", "pass")
	tm, _ := ts.CreateTeam("Test", cap.ID)
	_ = ts.AddMember(tm.ID, member.ID, cap.ID)

	assert.True(t, ts.CanManageTeam(tm.ID, cap.ID))
	assert.False(t, ts.CanManageTeam(tm.ID, member.ID))
}

func TestTeamService_GetMyTeams(t *testing.T) {
	db := setupTeamDB(t)
	ts := team.NewTeamService(db)

	u1 := createUser(t, db, "user1@test.com", "pass")
	u2 := createUser(t, db, "user2@test.com", "pass")

	tmA, _ := ts.CreateTeam("Team A", u1.ID)
	tmB, _ := ts.CreateTeam("Team B", u2.ID)
	_ = ts.AddMember(tmB.ID, u1.ID, u2.ID)

	teams, err := ts.GetMyTeams(u1.ID)
	require.NoError(t, err)
	assert.Len(t, teams, 2)

	ids := []uint{teams[0].ID, teams[1].ID}
	assert.Contains(t, ids, tmA.ID)
	assert.Contains(t, ids, tmB.ID)
}

// ---------- InvitationService ----------

func TestInvitationService_Create(t *testing.T) {
	db := setupTeamDB(t)
	ts := team.NewTeamService(db)
	authorizer := &gameAuthorizerStub{db}
	invSvc := team.NewInvitationService(db, ts, authorizer, &config.Config{})

	cap := createUser(t, db, "cap@test.com", "pass")
	invited := createUser(t, db, "inv@test.com", "pass")
	tm, _ := ts.CreateTeam("Inv Team", cap.ID)

	inv, err := invSvc.CreateInvitation(tm.ID, invited.ID, cap.ID)
	require.NoError(t, err)
	assert.Equal(t, team.InvitationPending, inv.Status)
	assert.Equal(t, invited.ID, inv.UserID)
}

func TestInvitationService_Accept(t *testing.T) {
	db := setupTeamDB(t)
	ts := team.NewTeamService(db)
	authorizer := &gameAuthorizerStub{db}
	invSvc := team.NewInvitationService(db, ts, authorizer, &config.Config{})

	cap := createUser(t, db, "cap@test.com", "pass")
	invited := createUser(t, db, "inv@test.com", "pass")
	tm, _ := ts.CreateTeam("Inv Team", cap.ID)

	inv, _ := invSvc.CreateInvitation(tm.ID, invited.ID, cap.ID)

	err := invSvc.AcceptInvitation(inv.ID, invited.ID)
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
	ts := team.NewTeamService(db)
	authorizer := &gameAuthorizerStub{db}
	invSvc := team.NewInvitationService(db, ts, authorizer, &config.Config{})

	cap := createUser(t, db, "cap@test.com", "pass")
	invited := createUser(t, db, "inv@test.com", "pass")
	tm, _ := ts.CreateTeam("Inv Team", cap.ID)

	inv, _ := invSvc.CreateInvitation(tm.ID, invited.ID, cap.ID)

	err := invSvc.DeclineInvitation(inv.ID, invited.ID)
	require.NoError(t, err)

	var updated team.Invitation
	db.First(&updated, inv.ID)
	assert.Equal(t, team.InvitationDeclined, updated.Status)
}

// ---------- заглушки ----------

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

func createUser(t *testing.T, db *gorm.DB, email, _ string) *user.User {
	t.Helper()
	u := &user.User{Email: email, Password: "hashed", Name: email}
	require.NoError(t, db.Create(u).Error)
	return u
}
