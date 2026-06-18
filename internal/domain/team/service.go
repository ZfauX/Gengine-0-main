// internal/domain/team/service.go
package team

import (
	"errors"
	"fmt"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/middleware"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// ---------- TeamService ----------

type TeamService struct {
	DB *gorm.DB
}

func NewTeamService(db *gorm.DB) *TeamService {
	return &TeamService{DB: db}
}

// GetMyTeams возвращает все команды, где пользователь капитан или участник.
func (s *TeamService) GetMyTeams(userID uint) ([]Team, error) {
	var teams []Team
	s.DB.Where("captain_id = ?", userID).Find(&teams)

	var memberTeamIDs []uint
	s.DB.Table("team_members").Where("user_id = ?", userID).Pluck("team_id", &memberTeamIDs)
	if len(memberTeamIDs) > 0 {
		var memberTeams []Team
		s.DB.Where("id IN ? AND captain_id != ?", memberTeamIDs, userID).Find(&memberTeams)
		teams = append(teams, memberTeams...)
	}
	return teams, nil
}

// GetTeamsByCaptain возвращает команды, в которых пользователь является капитаном.
func (s *TeamService) GetTeamsByCaptain(captainID uint) ([]Team, error) {
	var teams []Team
	err := s.DB.Where("captain_id = ?", captainID).Find(&teams).Error
	return teams, err
}

// CreateTeam создаёт новую команду.
func (s *TeamService) CreateTeam(name string, captainID uint) (*Team, error) {
	team := &Team{
		Name:      name,
		CaptainID: captainID,
	}
	if err := s.DB.Create(team).Error; err != nil {
		return nil, err
	}
	return team, nil
}

// GetTeamWithMembers возвращает команду и состав участников.
func (s *TeamService) GetTeamWithMembers(teamID uint) (*Team, []user.User, error) {
	var team Team
	if err := s.DB.First(&team, teamID).Error; err != nil {
		return nil, nil, err
	}

	// Загружаем капитана явно, чтобы заполнить team.Captain
	var captain user.User
	if err := s.DB.First(&captain, team.CaptainID).Error; err == nil {
		team.Captain = captain
	}

	var members []user.User
	s.DB.Table("users").
		Joins("JOIN team_members ON team_members.user_id = users.id").
		Where("team_members.team_id = ?", teamID).
		Find(&members)

	// Если капитана ещё нет в списке участников – добавляем его первым
	if team.CaptainID != 0 {
		found := false
		for _, m := range members {
			if m.ID == team.CaptainID {
				found = true
				break
			}
		}
		if !found {
			members = append([]user.User{team.Captain}, members...)
		}
	}

	return &team, members, nil
}

// CanManageTeam определяет, может ли пользователь управлять командой.
func (s *TeamService) CanManageTeam(teamID, userID uint) bool {
	var team Team
	if err := s.DB.First(&team, teamID).Error; err != nil {
		return false
	}
	if team.CaptainID == userID {
		return true
	}
	var passing gamePassing
	if err := s.DB.Where("team_id = ?", teamID).First(&passing).Error; err == nil {
		var g gameModel
		if s.DB.First(&g, passing.GameID).Error == nil && g.AuthorID == userID {
			return true
		}
	}
	return false
}

// GetAvailableUsers возвращает пользователей, ещё не состоящих в данной команде.
func (s *TeamService) GetAvailableUsers(teamID uint) ([]user.User, error) {
	var users []user.User
	subQuery := s.DB.Table("team_members").Select("user_id").Where("team_id = ?", teamID)
	err := s.DB.Model(&user.User{}).Where("id NOT IN (?)", subQuery).Find(&users).Error
	return users, err
}

// AddMember добавляет участника в команду.
func (s *TeamService) AddMember(teamID, newMemberID, actorID uint) error {
	if !s.CanManageTeam(teamID, actorID) {
		return errors.New("только капитан или автор игры может добавлять участников")
	}
	var count int64
	s.DB.Table("team_members").Where("team_id = ? AND user_id = ?", teamID, newMemberID).Count(&count)
	if count > 0 {
		return errors.New("пользователь уже в команде")
	}
	return s.DB.Exec("INSERT INTO team_members (team_id, user_id) VALUES (?, ?)", teamID, newMemberID).Error
}

// RemoveMember удаляет участника из команды.
func (s *TeamService) RemoveMember(teamID, memberID, actorID uint) error {
	if !s.CanManageTeam(teamID, actorID) {
		return errors.New("нет прав на удаление участников")
	}
	var team Team
	if err := s.DB.First(&team, teamID).Error; err != nil {
		return err
	}
	if team.CaptainID == memberID {
		return errors.New("невозможно удалить капитана")
	}
	return s.DB.Exec("DELETE FROM team_members WHERE team_id = ? AND user_id = ?", teamID, memberID).Error
}

// ChangeCaptain меняет капитана команды.
func (s *TeamService) ChangeCaptain(teamID, newCaptainID, actorID uint) error {
	if !s.CanManageTeam(teamID, actorID) {
		return errors.New("нет прав на смену капитана")
	}
	var count int64
	s.DB.Table("team_members").Where("team_id = ? AND user_id = ?", teamID, newCaptainID).Count(&count)
	if count == 0 {
		return errors.New("новый капитан должен состоять в команде")
	}
	return s.DB.Model(&Team{}).Where("id = ?", teamID).Update("captain_id", newCaptainID).Error
}

// ---------- локальные модели, заменяющие импорт game ----------

type gamePassing struct {
	ID     uint `gorm:"primaryKey"`
	GameID uint `gorm:"column:game_id"`
	TeamID uint `gorm:"column:team_id"`
}

func (gamePassing) TableName() string { return "game_passings" }

type gameModel struct {
	ID       uint `gorm:"primaryKey"`
	AuthorID uint `gorm:"column:author_id"`
}

func (gameModel) TableName() string { return "games" }

// ---------- InvitationService ----------

type InvitationService struct {
	DB          *gorm.DB
	teamService *TeamService
	authorizer  middleware.GameAuthorizer
	cfg         *config.Config
}

func NewInvitationService(db *gorm.DB, ts *TeamService, authorizer middleware.GameAuthorizer, cfg *config.Config) *InvitationService {
	return &InvitationService{
		DB:          db,
		teamService: ts,
		authorizer:  authorizer,
		cfg:         cfg,
	}
}

// CreateInvitation создаёт приглашение и отправляет email приглашённому.
func (s *InvitationService) CreateInvitation(teamID, invitedUserID, actorID uint) (*Invitation, error) {
	var team Team
	if err := s.DB.First(&team, teamID).Error; err != nil {
		return nil, err
	}

	isCaptain := (team.CaptainID == actorID)
	if !isCaptain {
		var passing gamePassing
		if err := s.DB.Where("team_id = ?", teamID).First(&passing).Error; err != nil {
			return nil, errors.New("не удалось определить игру для команды")
		}
		ok, _ := s.authorizer.IsUserManager(passing.GameID, actorID)
		if !ok {
			return nil, errors.New("только капитан или автор игры может создавать приглашения")
		}
	}

	var count int64
	s.DB.Table("team_members").Where("team_id = ? AND user_id = ?", teamID, invitedUserID).Count(&count)
	if count > 0 || team.CaptainID == invitedUserID {
		return nil, errors.New("пользователь уже в команде")
	}

	var existing Invitation
	err := s.DB.Where("team_id = ? AND user_id = ? AND status = ?", teamID, invitedUserID, InvitationPending).First(&existing).Error
	if err == nil {
		return nil, errors.New("приглашение уже отправлено")
	}

	inv := &Invitation{
		TeamID: teamID,
		UserID: invitedUserID,
		Status: InvitationPending,
	}
	if err := s.DB.Create(inv).Error; err != nil {
		return nil, err
	}

	if s.cfg != nil {
		emailService := email.NewEmailService(s.cfg)
		var invitedUser user.User
		if err := s.DB.First(&invitedUser, invitedUserID).Error; err == nil {
			acceptLink := fmt.Sprintf("%s/invitations/%d/accept", s.cfg.Server.BaseURL, inv.ID)
			if err := emailService.Send(invitedUser.Email, "Приглашение в команду",
				fmt.Sprintf("Вас пригласили в команду «%s». Принять приглашение: %s", team.Name, acceptLink)); err != nil {
				log.Error().Err(err).Uint("team_id", teamID).Uint("invited_user", invitedUserID).Msg("failed to send invitation email")
			}
		}
	}

	return inv, nil
}

// ListByTeam возвращает все приглашения команды.
func (s *InvitationService) ListByTeam(teamID uint) ([]Invitation, error) {
	var invs []Invitation
	err := s.DB.Preload("User").Where("team_id = ?", teamID).Find(&invs).Error
	return invs, err
}

// GetPendingForUser возвращает все необработанные приглашения для пользователя.
func (s *InvitationService) GetPendingForUser(userID uint) ([]Invitation, error) {
	var invs []Invitation
	err := s.DB.Preload("Team").Where("user_id = ? AND status = ?", userID, InvitationPending).Find(&invs).Error
	return invs, err
}

// AcceptInvitation принимает приглашение.
func (s *InvitationService) AcceptInvitation(invitationID, userID uint) error {
	var inv Invitation
	if err := s.DB.First(&inv, invitationID).Error; err != nil {
		return err
	}
	if inv.UserID != userID {
		return errors.New("вы не можете принять это приглашение")
	}
	if inv.Status != InvitationPending {
		return errors.New("приглашение уже обработано")
	}
	inv.Status = InvitationAccepted
	if err := s.DB.Save(&inv).Error; err != nil {
		return err
	}
	return s.DB.Exec("INSERT INTO team_members (team_id, user_id) VALUES (?, ?)", inv.TeamID, userID).Error
}

// DeclineInvitation отклоняет приглашение.
func (s *InvitationService) DeclineInvitation(invitationID, userID uint) error {
	var inv Invitation
	if err := s.DB.First(&inv, invitationID).Error; err != nil {
		return err
	}
	if inv.UserID != userID {
		return errors.New("вы не можете отклонить это приглашение")
	}
	if inv.Status != InvitationPending {
		return errors.New("приглашение уже обработано")
	}
	inv.Status = InvitationDeclined
	return s.DB.Save(&inv).Error
}