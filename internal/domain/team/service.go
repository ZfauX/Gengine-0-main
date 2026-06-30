// internal/domain/team/service.go
package team

import (
	"context"
	"errors"
	"fmt"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/metrics"
	"gengine-0/internal/pkg/middleware"

	"gorm.io/gorm"
)

type TeamService struct {
	teamRepo   TeamRepository
	authorizer middleware.GameAuthorizer
}

func NewTeamService(teamRepo TeamRepository, authorizer middleware.GameAuthorizer) *TeamService {
	return &TeamService{
		teamRepo:   teamRepo,
		authorizer: authorizer,
	}
}

func (s *TeamService) GetMyTeams(ctx context.Context, userID uint) ([]Team, error) {
	return s.teamRepo.GetTeamsByUserID(ctx, userID)
}

func (s *TeamService) GetTeamsByCaptain(ctx context.Context, captainID uint) ([]Team, error) {
	return s.teamRepo.GetByCaptainID(ctx, captainID)
}

func (s *TeamService) CreateTeam(ctx context.Context, name string, captainID uint) (*Team, error) {
	team := &Team{
		Name:      name,
		CaptainID: captainID,
	}
	err := s.teamRepo.Create(ctx, team)
	if err == nil {
		metrics.IncTeamsTotal()
	}
	return team, err
}

func (s *TeamService) GetTeamWithMembers(ctx context.Context, teamID uint) (*Team, []user.User, error) {
	team, err := s.teamRepo.GetByIDWithMembers(ctx, teamID)
	if err != nil {
		return nil, nil, err
	}
	members := team.Members
	found := false
	for _, m := range members {
		if m.ID == team.CaptainID {
			found = true
			break
		}
	}
	if !found && team.CaptainID != 0 {
		members = append([]user.User{team.Captain}, members...)
	}
	return team, members, nil
}

func (s *TeamService) CanManageTeam(ctx context.Context, teamID, userID uint) bool {
	team, err := s.teamRepo.GetByID(ctx, teamID)
	if err != nil {
		return false
	}
	if team.CaptainID == userID {
		return true
	}
	var passing gamePassing
	if err := s.teamRepo.(*gormTeamRepo).db.Where("team_id = ?", teamID).First(&passing).Error; err == nil {
		var g gameModel
		if s.teamRepo.(*gormTeamRepo).db.First(&g, passing.GameID).Error == nil && g.AuthorID == userID {
			return true
		}
	}
	return false
}

func (s *TeamService) GetAvailableUsers(ctx context.Context, teamID uint) ([]user.User, error) {
	var users []user.User
	subQuery := s.teamRepo.(*gormTeamRepo).db.Table("team_members").Select("user_id").Where("team_id = ?", teamID)
	err := s.teamRepo.(*gormTeamRepo).db.Model(&user.User{}).Where("id NOT IN (?)", subQuery).Find(&users).Error
	return users, err
}

func (s *TeamService) AddMember(ctx context.Context, teamID, newMemberID, actorID uint) error {
	if !s.CanManageTeam(ctx, teamID, actorID) {
		return errors.New("только капитан или автор игры может добавлять участников")
	}
	isMember, err := s.teamRepo.IsMember(ctx, teamID, newMemberID)
	if err != nil {
		return err
	}
	if isMember {
		return errors.New("пользователь уже в команде")
	}
	if err := s.teamRepo.AddMember(ctx, teamID, newMemberID); err != nil {
		return err
	}
	s.updateTeamMembersTotal()
	return nil
}

func (s *TeamService) RemoveMember(ctx context.Context, teamID, memberID, actorID uint) error {
	if !s.CanManageTeam(ctx, teamID, actorID) {
		return errors.New("нет прав на удаление участников")
	}
	team, err := s.teamRepo.GetByID(ctx, teamID)
	if err != nil {
		return err
	}
	if team.CaptainID == memberID {
		return errors.New("невозможно удалить капитана")
	}
	if err := s.teamRepo.RemoveMember(ctx, teamID, memberID); err != nil {
		return err
	}
	s.updateTeamMembersTotal()
	return nil
}

func (s *TeamService) ChangeCaptain(ctx context.Context, teamID, newCaptainID, actorID uint) error {
	if !s.CanManageTeam(ctx, teamID, actorID) {
		return errors.New("нет прав на смену капитана")
	}
	isMember, err := s.teamRepo.IsMember(ctx, teamID, newCaptainID)
	if err != nil {
		return err
	}
	if !isMember {
		return errors.New("новый капитан должен состоять в команде")
	}
	return s.teamRepo.ChangeCaptain(ctx, teamID, newCaptainID)
}

// updateTeamMembersTotal обновляет gauge с общим количеством участников команд.
func (s *TeamService) updateTeamMembersTotal() {
	var count int64
	s.teamRepo.(*gormTeamRepo).db.Table("team_members").Count(&count)
	metrics.SetTeamMembersTotal(float64(count))
}

// ---------- локальные модели ----------
type gamePassing struct {
	ID     uint
	GameID uint
	TeamID uint
}

func (gamePassing) TableName() string { return "game_passings" }

type gameModel struct {
	ID       uint
	AuthorID uint
}

func (gameModel) TableName() string { return "games" }

// ---------- InvitationService ----------

type InvitationService struct {
	invRepo    InvitationRepository
	teamRepo   TeamRepository
	authorizer middleware.GameAuthorizer
	cfg        *config.Config
}

func NewInvitationService(
	invRepo InvitationRepository,
	teamRepo TeamRepository,
	authorizer middleware.GameAuthorizer,
	cfg *config.Config,
) *InvitationService {
	return &InvitationService{
		invRepo:    invRepo,
		teamRepo:   teamRepo,
		authorizer: authorizer,
		cfg:        cfg,
	}
}

func (s *InvitationService) CreateInvitation(ctx context.Context, teamID, invitedUserID, actorID uint) (*Invitation, error) {
	team, err := s.teamRepo.GetByID(ctx, teamID)
	if err != nil {
		return nil, err
	}

	isCaptain := (team.CaptainID == actorID)
	if !isCaptain {
		var passing gamePassing
		if err := s.teamRepo.(*gormTeamRepo).db.Where("team_id = ?", teamID).First(&passing).Error; err != nil {
			return nil, errors.New("не удалось определить игру для команды")
		}
		ok, _ := s.authorizer.IsUserManager(passing.GameID, actorID)
		if !ok {
			return nil, errors.New("только капитан или автор игры может создавать приглашения")
		}
	}

	isMember, err := s.teamRepo.IsMember(ctx, teamID, invitedUserID)
	if err != nil {
		return nil, err
	}
	if isMember || team.CaptainID == invitedUserID {
		return nil, errors.New("пользователь уже в команде")
	}

	existing, _ := s.invRepo.GetByTeamAndUser(ctx, teamID, invitedUserID)
	if existing != nil && existing.Status == InvitationPending {
		return nil, errors.New("приглашение уже отправлено")
	}

	inv := &Invitation{
		TeamID: teamID,
		UserID: invitedUserID,
		Status: InvitationPending,
	}
	if err := s.invRepo.Create(ctx, inv); err != nil {
		return nil, err
	}

	if s.cfg != nil && s.cfg.SMTP.Enabled {
		emailService := email.NewEmailService(s.cfg)
		var invitedUser user.User
		if err := s.teamRepo.(*gormTeamRepo).db.First(&invitedUser, invitedUserID).Error; err == nil {
			acceptLink := fmt.Sprintf("%s/invitations/%d/accept", s.cfg.Server.BaseURL, inv.ID)
			_ = emailService.Send(invitedUser.Email, "Приглашение в команду",
				fmt.Sprintf("Вас пригласили в команду «%s». Принять приглашение: %s", team.Name, acceptLink))
		}
	}

	return inv, nil
}

func (s *InvitationService) ListByTeam(ctx context.Context, teamID uint) ([]Invitation, error) {
	return s.invRepo.ListByTeam(ctx, teamID)
}

func (s *InvitationService) GetPendingForUser(ctx context.Context, userID uint) ([]Invitation, error) {
	return s.invRepo.ListPendingByUser(ctx, userID)
}

func (s *InvitationService) AcceptInvitation(ctx context.Context, invitationID, userID uint) error {
	inv, err := s.invRepo.GetByID(ctx, invitationID)
	if err != nil {
		return err
	}
	if inv.UserID != userID {
		return errors.New("вы не можете принять это приглашение")
	}
	if inv.Status != InvitationPending {
		return errors.New("приглашение уже обработано")
	}

	db := s.teamRepo.(*gormTeamRepo).db

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Обновляем статус приглашения через tx
		if err := tx.Model(&Invitation{}).Where("id = ?", invitationID).Update("status", InvitationAccepted).Error; err != nil {
			return err
		}
		// Добавляем участника через tx
		if err := tx.Exec("INSERT INTO team_members (team_id, user_id) VALUES (?, ?)", inv.TeamID, userID).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Обновляем метрику после успешной транзакции
	if ts, ok := s.teamRepo.(*gormTeamRepo); ok {
		var count int64
		ts.db.Table("team_members").Count(&count)
		metrics.SetTeamMembersTotal(float64(count))
	}
	return nil
}

func (s *InvitationService) DeclineInvitation(ctx context.Context, invitationID, userID uint) error {
	inv, err := s.invRepo.GetByID(ctx, invitationID)
	if err != nil {
		return err
	}
	if inv.UserID != userID {
		return errors.New("вы не можете отклонить это приглашение")
	}
	if inv.Status != InvitationPending {
		return errors.New("приглашение уже обработано")
	}
	return s.invRepo.UpdateStatus(ctx, invitationID, InvitationDeclined)
}
