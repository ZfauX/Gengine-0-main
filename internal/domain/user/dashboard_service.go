// internal/domain/user/dashboard_service.go
package user

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
)

// ---------- UserDashboardService ----------

// RecentNotificationsLoader (UX-1, PASS-16): колбэк загрузки последних
// уведомлений. Объявлен здесь как функция без импорта notification-пакета
// (notification→user иначе дал бы import cycle). Конкретную реализацию
// подставляет DI (internal/app/wire_providers.go), оборачивая
// notification.NotificationRepository.
type RecentNotificationsLoader func(ctx context.Context, userID uint) ([]DashboardNotification, error)

type UserDashboardService struct {
	userRepo         UserRepository
	recentNotifsLoad RecentNotificationsLoader
}

func NewUserDashboardService(userRepo UserRepository, recentNotifsLoad RecentNotificationsLoader) *UserDashboardService {
	return &UserDashboardService{userRepo: userRepo, recentNotifsLoad: recentNotifsLoad}
}

type UserDashboard struct {
	AuthoredGames      []DashboardGame
	CaptainTeams       []DashboardTeamWithGame
	MemberTeams        []DashboardTeamWithGame
	ActivePassings     []DashboardPassingWithGame
	PendingInvitations []DashboardInvitation
	// RecentNotifications (UX-1, PASS-16): последние N уведомлений — выводятся
	// на дашборде, чтобы не уходить в /notifications ради нового уведомления.
	RecentNotifications []DashboardNotification
}

// DashboardNotification — уведомление на дашборде (подмножество полей).
type DashboardNotification struct {
	ID        uint
	Type      string
	Title     string
	Body      string
	Link      string
	Read      bool
	CreatedAt time.Time
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
	// UX-5 (PASS-13): прогресс и позиция текущего уровня для «продолжить».
	CompletedLevels int
	TotalLevels     int
	CurrentPosition int
	CanContinue     bool
}

type DashboardInvitation struct {
	ID       uint
	TeamID   uint
	TeamName string
	Status   string
}

// GetDashboard собирает данные для дашборда с оптимизированными запросами.
// M7 (PASS-18): независимые запросы выполняются ПАРАЛЛЕЛЬНО (errgroup) —
// раньше 4 round-trip последовательно на каждую загрузку дашборда.
func (s *UserDashboardService) GetDashboard(ctx context.Context, userID uint) (*UserDashboard, error) {
	var dash UserDashboard

	g, gctx := errgroup.WithContext(ctx)

	// 1. Авторские игры
	g.Go(func() error {
		authoredGames, err := s.userRepo.DashboardAuthoredGames(gctx, userID)
		if err != nil {
			return fmt.Errorf("failed to get authored games: %w", err)
		}
		for _, gg := range authoredGames {
			dash.AuthoredGames = append(dash.AuthoredGames, DashboardGame(gg))
		}
		return nil
	})

	// 2. Единый запрос: команды + прохождения + названия игр через JOIN
	g.Go(func() error {
		rows, err := s.userRepo.DashboardTeams(gctx, userID)
		if err != nil {
			return fmt.Errorf("failed to get teams data: %w", err)
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
				// UX-5 (PASS-13): «продолжить» доступно, пока есть незавершённые уровни.
				canContinue := r.PassingStatus == "started" && r.TotalLevels > 0 && r.CompletedLevels < r.TotalLevels
				curPos := 0
				if r.CurrentPosition != nil {
					curPos = *r.CurrentPosition
				}
				dash.ActivePassings = append(dash.ActivePassings, DashboardPassingWithGame{
					PassingStatus:   r.PassingStatus,
					TeamName:        r.TeamName,
					GameName:        r.GameName,
					GameID:          r.GameID,
					PassingID:       r.PassingID,
					CompletedLevels: r.CompletedLevels,
					TotalLevels:     r.TotalLevels,
					CurrentPosition: curPos,
					CanContinue:     canContinue,
				})
			}
		}
		return nil
	})

	// 3. Приглашения (некритично: ошибка логируется, дашборд рендерится без них)
	g.Go(func() error {
		if err := s.loadInvitations(gctx, &dash, userID); err != nil {
			log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to load invitations")
		}
		return nil
	})

	// 4. Последние уведомления (UX-1, PASS-16): некритично.
	if s.recentNotifsLoad != nil {
		g.Go(func() error {
			if err := s.loadRecentNotifications(gctx, &dash, userID); err != nil {
				log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: failed to load notifications")
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("GetDashboard: one of parallel loads failed")
		return &dash, err
	}
	return &dash, nil
}

// loadRecentNotifications загружает последние уведомления пользователя через
// колбэк, переданный из DI.
func (s *UserDashboardService) loadRecentNotifications(ctx context.Context, dash *UserDashboard, userID uint) error {
	items, err := s.recentNotifsLoad(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to load recent notifications: %w", err)
	}
	dash.RecentNotifications = items
	return nil
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
