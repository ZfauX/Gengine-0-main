// internal/domain/user/dashboard_service_test.go
// Unit-тесты UserDashboardService с gomock-моками репозитория.
package user

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M18 (pass 30): ошибка загрузки приглашений некритична — дашборд рендерится.
func TestGetDashboard_InvitationErrorIsNonFatal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := NewMockUserRepository(ctrl)
	svc := NewUserDashboardService(repo, nil)
	ctx := context.Background()

	repo.EXPECT().DashboardAuthoredGames(ctx, uint(5)).Return([]DashboardGameRow{}, nil)
	repo.EXPECT().DashboardTeams(ctx, uint(5)).Return([]DashboardTeamRow{}, nil)
	repo.EXPECT().DashboardInvitations(ctx, uint(5)).Return([]DashboardInvitationRow{}, errors.New("db down"))

	dash, err := svc.GetDashboard(ctx, 5)
	require.NoError(t, err, "ошибка приглашений не должна валить дашборд")
	assert.NotNil(t, dash)
	assert.Empty(t, dash.PendingInvitations)
}

// M18: ошибка команд фатальна — дашборд неполный невозможен.
func TestGetDashboard_TeamsErrorIsFatal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := NewMockUserRepository(ctrl)
	svc := NewUserDashboardService(repo, nil)
	ctx := context.Background()

	repo.EXPECT().DashboardAuthoredGames(ctx, uint(5)).Return([]DashboardGameRow{}, nil)
	repo.EXPECT().DashboardTeams(ctx, uint(5)).Return([]DashboardTeamRow{}, errors.New("db down"))

	_, err := svc.GetDashboard(ctx, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get teams data")
}

// M18: успешная загрузка — авторы и приглашения попадают в дашборд.
func TestGetDashboard_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := NewMockUserRepository(ctrl)
	svc := NewUserDashboardService(repo, nil)
	ctx := context.Background()

	repo.EXPECT().DashboardAuthoredGames(ctx, uint(7)).Return(
		[]DashboardGameRow{{ID: 1, Name: "Game A"}}, nil)
	repo.EXPECT().DashboardTeams(ctx, uint(7)).Return([]DashboardTeamRow{}, nil)
	repo.EXPECT().DashboardInvitations(ctx, uint(7)).Return(
		[]DashboardInvitationRow{{ID: 3, TeamID: 4, TeamName: "Team", Status: "pending"}}, nil)

	dash, err := svc.GetDashboard(ctx, 7)
	require.NoError(t, err)
	assert.Len(t, dash.AuthoredGames, 1)
	assert.Equal(t, "Game A", dash.AuthoredGames[0].Name)
	assert.Len(t, dash.PendingInvitations, 1)
	assert.Equal(t, "Team", dash.PendingInvitations[0].TeamName)
}

// GetDashboard активные прохождения отбираются по статусам.
func TestGetDashboard_ActivePassingsFiltered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := NewMockUserRepository(ctrl)
	svc := NewUserDashboardService(repo, nil)
	ctx := context.Background()

	repo.EXPECT().DashboardAuthoredGames(ctx, uint(9)).Return([]DashboardGameRow{}, nil)
	repo.EXPECT().DashboardTeams(ctx, uint(9)).Return([]DashboardTeamRow{
		{TeamID: 1, TeamName: "Alpha", CaptainID: 9, PassingID: 11, GameID: 22, GameName: "G", PassingStatus: "started"},
		{TeamID: 2, TeamName: "Beta", CaptainID: 9, PassingID: 12, GameID: 23, GameName: "H", PassingStatus: "finished"},
	}, nil)
	repo.EXPECT().DashboardInvitations(ctx, uint(9)).Return([]DashboardInvitationRow{}, nil)

	dash, err := svc.GetDashboard(ctx, 9)
	require.NoError(t, err)
	require.Len(t, dash.ActivePassings, 1, "только started/accepted попадают в активные")
	assert.Equal(t, "Alpha", dash.ActivePassings[0].TeamName)
	// Beta — finished, не входит в ActivePassings.
}
