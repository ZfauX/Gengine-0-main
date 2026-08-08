// internal/domain/user/dashboard_service.go
package user

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
)

// ---------- UserDashboardService ----------

type UserDashboardService struct {
	userRepo UserRepository
}

func NewUserDashboardService(userRepo UserRepository) *UserDashboardService {
	return &UserDashboardService{userRepo: userRepo}
}

type UserDashboard struct {
	AuthoredGames      []DashboardGame
	CaptainTeams       []DashboardTeamWithGame
	MemberTeams        []DashboardTeamWithGame
	ActivePassings     []DashboardPassingWithGame
	PendingInvitations []DashboardInvitation
}

type DashboardGame struct {
	ID      uint
	Name    string
	IsDraft bool
}

type DashboardTeamWithGame struct {
	Team DashboardTeam
	Game DashboardGame
}

type DashboardTeam struct {
	ID   uint
	Name string
}

type DashboardPassingWithGame struct {
	PassingStatus string
	TeamName      string
	GameName      string
	GameID        uint
	PassingID     uint
}

type DashboardInvitation struct {
	ID       uint
	TeamID   uint
	TeamName string
	Status   string
}

// GetDashboard собирает данные для дашборда с оптимизированными запросами.
// Использует 3 запроса вместо 7 за счёт JOIN (запросы — в репозитории, C1).
func (s *UserDashboardService) GetDashboard(ctx context.Context, userID uint) (*UserDashboard, error) {
	var dash UserDashboard

	// 1. Авторские игры
	authoredGames, err := s.userRepo.DashboardAuthoredGames(ctx, userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to get authored games")
		return &dash, fmt.Errorf("failed to get authored games: %w", err)
	}
	for _, g := range authoredGames {
		dash.AuthoredGames = append(dash.AuthoredGames, DashboardGame(g))
	}

	// 2. Единый запрос: команды + прохождения + названия игр через JOIN
	rows, err := s.userRepo.DashboardTeams(ctx, userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to get teams data")
		return &dash, fmt.Errorf("failed to get teams data: %w", err)
	}

	seenTeams := make(map[uint]bool)
	for _, r := range rows {
		// Добавляем команду в список (один раз)
		if !seenTeams[r.TeamID] {
			seenTeams[r.TeamID] = true
			team := DashboardTeam{ID: r.TeamID, Name: r.TeamName}
			twg := DashboardTeamWithGame{Team: team, Game: DashboardGame{}}
			if r.CaptainID == userID {
				dash.CaptainTeams = append(dash.CaptainTeams, twg)
			} else {
				dash.MemberTeams = append(dash.MemberTeams, twg)
			}
		}
		// Активные прохождения
		if r.PassingID != 0 && r.GameName != "" &&
			(r.PassingStatus == "started" || r.PassingStatus == "accepted") {
			dash.ActivePassings = append(dash.ActivePassings, DashboardPassingWithGame{
				PassingStatus: r.PassingStatus,
				TeamName:      r.TeamName,
				GameName:      r.GameName,
				GameID:        r.GameID,
				PassingID:     r.PassingID,
			})
		}
	}

	// 3. Приглашения (некритично: ошибка логируется, дашборд рендерится без них)
	if err := s.loadInvitations(ctx, &dash, userID); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to load invitations")
	}

	return &dash, nil
}

// loadInvitations загружает ожидающие приглашения в структуру дашборда.
// M18 (pass 30): возвращает ошибку — ранее глотала её молча.
func (s *UserDashboardService) loadInvitations(ctx context.Context, dash *UserDashboard, userID uint) error {
	invitations, err := s.userRepo.DashboardInvitations(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to load invitations: %w", err)
	}
	for _, inv := range invitations {
		dash.PendingInvitations = append(dash.PendingInvitations, DashboardInvitation(inv))
	}
	return nil
}
