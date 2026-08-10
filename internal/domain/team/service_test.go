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
		&team.Team{}, &team.Invitation{}, &team.TeamMember{},
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
	require.NoError(t, ts.AddMember(context.Background(), tm.ID, member.ID, cap.ID))

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
	require.NoError(t, ts.AddMember(context.Background(), tm.ID, newCap.ID, oldCap.ID))

	err := ts.ChangeCaptain(context.Background(), tm.ID, newCap.ID, oldCap.ID, false)
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

	err := ts.ChangeCaptain(context.Background(), tm.ID, newCap.ID, oldCap.ID, false)
	assert.Error(t, err)
}

func TestTeamService_CanManageTeam(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	cap := createUser(t, db, "cap@test.com", "pass")
	member := createUser(t, db, "mem@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Test", cap.ID)
	require.NoError(t, ts.AddMember(context.Background(), tm.ID, member.ID, cap.ID))

	assert.True(t, ts.CanManageTeam(context.Background(), tm.ID, cap.ID))
	assert.False(t, ts.CanManageTeam(context.Background(), tm.ID, member.ID))
}

func TestTeamService_GetMyTeams(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	capA := createUser(t, db, "user1@test.com", "pass")
	capB := createUser(t, db, "user2@test.com", "pass")
	u3 := createUser(t, db, "user3@test.com", "pass")

	tmA, _ := ts.CreateTeam(context.Background(), "Team A", capA.ID)
	tmB, _ := ts.CreateTeam(context.Background(), "Team B", capB.ID)
	// A-5 (pass 45): игрок в одной команде — u3 член только Team B.
	require.NoError(t, ts.AddMember(context.Background(), tmB.ID, u3.ID, capB.ID))

	// Капитан видит свою команду.
	teamsA, err := ts.GetMyTeams(context.Background(), capA.ID)
	require.NoError(t, err)
	require.Len(t, teamsA, 1)
	assert.Equal(t, tmA.ID, teamsA[0].ID)

	// Игрок видит свою команду.
	teams3, err := ts.GetMyTeams(context.Background(), u3.ID)
	require.NoError(t, err)
	require.Len(t, teams3, 1)
	assert.Equal(t, tmB.ID, teams3[0].ID)
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

	// Проверяем, что invited существует в БД
	var userExists bool
	err := db.Raw("SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)", invited.ID).Scan(&userExists).Error
	require.NoError(t, err)
	assert.True(t, userExists, "приглашённый пользователь должен существовать")

	inv, err := invSvc.CreateInvitation(context.Background(), tm.ID, invited.ID, cap.ID)
	require.NoError(t, err)
	assert.NotZero(t, inv.ID, "приглашение должно быть создано")

	// Принимаем приглашение
	err = invSvc.AcceptInvitation(context.Background(), inv.ID, invited.ID)
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

func newTeamService(db *gorm.DB) *team.TeamService {
	teamRepo := team.NewGormTeamRepo(db)
	return team.NewTeamService(teamRepo)
}

func newTeamAndInvitationServices(db *gorm.DB) (*team.TeamService, *team.InvitationService) {
	teamRepo := team.NewGormTeamRepo(db)
	invRepo := team.NewGormInvitationRepo(db)
	cfg := &config.Config{}
	ts := team.NewTeamService(teamRepo)
	invSvc := team.NewInvitationService(invRepo, teamRepo, cfg)
	return ts, invSvc
}

func createUser(t *testing.T, db *gorm.DB, email, _ string) *user.User {
	t.Helper()
	u := &user.User{Email: email, Password: "hashed", Name: email}
	require.NoError(t, db.Create(u).Error)
	return u
}

// A-2/A-3 (pass 45): роли и группы участников.
func TestTeamService_MemberRolesAndGroups(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	cap := createUser(t, db, "cap2@test.com", "pass")
	member := createUser(t, db, "mem2@test.com", "pass")
	random := createUser(t, db, "random2@test.com", "pass")
	tm, _ := ts.CreateTeam(context.Background(), "Test", cap.ID)
	require.NoError(t, ts.AddMember(context.Background(), tm.ID, member.ID, cap.ID))

	// Назначение роли deputy — капитан.
	require.NoError(t, ts.SetMemberRole(context.Background(), tm.ID, member.ID, cap.ID, team.MemberRoleDeputy))
	// Не-капитан не может назначать.
	require.Error(t, ts.SetMemberRole(context.Background(), tm.ID, member.ID, random.ID, team.MemberRoleDeputy))

	// Группа reserve.
	require.NoError(t, ts.SetMemberGroup(context.Background(), tm.ID, member.ID, cap.ID, team.GroupReserve))
	// Роль на поле driver.
	require.NoError(t, ts.SetFieldRole(context.Background(), tm.ID, member.ID, cap.ID, team.FieldRoleDriver))
	// Невалидная роль на поле.
	require.Error(t, ts.SetFieldRole(context.Background(), tm.ID, member.ID, cap.ID, "pilot"))

	members, err := ts.GetMembersWithRoles(context.Background(), tm.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, team.MemberRoleDeputy, members[0].Role)
	assert.Equal(t, team.GroupReserve, members[0].GroupType)
	assert.Equal(t, team.FieldRoleDriver, members[0].FieldRole)
}

// A-5 (pass 45): игрок в одной команде + выход.
func TestTeamService_OneTeamPerPlayerAndLeave(t *testing.T) {
	db := setupTeamDB(t)
	ts := newTeamService(db)

	capA := createUser(t, db, "capa@test.com", "pass")
	capB := createUser(t, db, "capb@test.com", "pass")
	player := createUser(t, db, "player@test.com", "pass")
	tmA, _ := ts.CreateTeam(context.Background(), "Team A", capA.ID)
	tmB, _ := ts.CreateTeam(context.Background(), "Team B", capB.ID)

	// Игрок входит в команду A.
	require.NoError(t, ts.AddMember(context.Background(), tmA.ID, player.ID, capA.ID))
	// Не может войти в команду B (одна команда).
	require.ErrorIs(t, ts.AddMember(context.Background(), tmB.ID, player.ID, capB.ID), team.ErrAlreadyInOtherTeam)

	// Выход из A.
	require.NoError(t, ts.LeaveMember(context.Background(), tmA.ID, player.ID))
	// Теперь может войти в B.
	require.NoError(t, ts.AddMember(context.Background(), tmB.ID, player.ID, capB.ID))

	// Капитан не может выйти.
	require.Error(t, ts.LeaveMember(context.Background(), tmA.ID, capA.ID))
}
