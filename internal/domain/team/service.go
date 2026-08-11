// internal/domain/team/service.go
package team

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gengine-0/internal/config"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/email"
	"gengine-0/internal/pkg/metrics"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type TeamService struct {
	teamRepo TeamRepository
	// P0-3 (pass 45): userRepo для проверки супер-админа инсталляции.
	userRepo TeamUserRepository

	// P-45-3 (pass 45): throttle метрики «всего участников» — не чаще 1/мин.
	membersMetricMu   sync.Mutex
	membersMetricNext time.Time
}

// TeamUserRepository — минимальный контракт для проверки роли (избегаем
// циклической зависимости team→user).
type TeamUserRepository interface {
	GetUserRole(ctx context.Context, id uint) (string, error)
}

// Sentinel-ошибки команды (A-M4, pass 34): handlers могут делать errors.Is.
var (
	ErrOnlyCaptainCanAdd     = errors.New("только капитан может добавлять участников")
	ErrUserAlreadyInTeam     = errors.New("пользователь уже в команде")
	ErrNoPermissionRemove    = errors.New("нет прав на удаление участников")
	ErrCannotRemoveCaptain   = errors.New("невозможно удалить капитана")
	ErrNoPermissionChangeCap = errors.New("нет прав на смену капитана")
	ErrNewCaptainNotMember   = errors.New("новый капитан должен состоять в команде")
	ErrUserNotFound          = errors.New("пользователь не найден")
	ErrInvitationNotPending  = errors.New("приглашение не в статусе ожидания")
	ErrInvitationExists      = errors.New("приглашение уже отправлено")
	ErrOnlyCaptainCanInvite  = errors.New("только капитан может создавать приглашения")
	// A-5 (pass 45): игрок может состоять только в одной команде.
	ErrAlreadyInOtherTeam = errors.New("игрок уже состоит в другой команде")
)

func NewTeamService(teamRepo TeamRepository) *TeamService {
	return &TeamService{
		teamRepo: teamRepo,
	}
}

// WithUserRepository внедряет репозиторий пользователей для проверки
// супер-админа инсталляции (P0-3, pass 45).
func (s *TeamService) WithUserRepository(repo TeamUserRepository) *TeamService {
	s.userRepo = repo
	return s
}

// isSuperAdmin проверяет, что пользователь — админ инсталляции.
func (s *TeamService) isSuperAdmin(ctx context.Context, userID uint) bool {
	if s.userRepo == nil {
		return false
	}
	role, err := s.userRepo.GetUserRole(ctx, userID)
	return err == nil && role == "admin"
}

func (s *TeamService) GetMyTeams(ctx context.Context, userID uint) ([]Team, error) {
	return s.teamRepo.GetTeamsByUserID(ctx, userID)
}

// GetAllTeams возвращает все доступные команды (вкладка «Команды»).
func (s *TeamService) GetAllTeams(ctx context.Context) ([]Team, error) {
	return s.teamRepo.ListAllTeams(ctx)
}

// SearchUsersForInvitation ищет пользователей для приглашения (без уже состоящих в команде).
func (s *TeamService) SearchUsersForInvitation(ctx context.Context, query string, teamID uint) ([]struct {
	ID   uint
	Name string
}, error) {
	return s.teamRepo.SearchUsersForInvitation(ctx, query, teamID)
}

// GetOrCreateTeamChatRoom находит или создаёт комнату чата команды (C1).
func (s *TeamService) GetOrCreateTeamChatRoom(ctx context.Context, teamID uint, teamName string) (*teamChatRoom, error) {
	return s.teamRepo.GetOrCreateTeamChatRoom(ctx, teamID, teamName)
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
	// P0-3 (pass 45): супер-админ инсталляции управляет любой командой.
	if s.isSuperAdmin(ctx, userID) {
		return true
	}
	team, err := s.teamRepo.GetByID(ctx, teamID)
	if err != nil {
		log.Error().Err(err).Uint("team_id", teamID).Msg("CanManageTeam: failed to get team")
		return false
	}
	return team.CaptainID == userID
}

// userInOtherTeam проверяет, что пользователь уже состоит в какой-либо команде
// (кроме указанной). A-5 (pass 45): игрок — только в одной команде.
func (s *TeamService) userInOtherTeam(ctx context.Context, userID, excludeTeamID uint) (bool, error) {
	teams, err := s.teamRepo.GetTeamsByUserID(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, t := range teams {
		if t.ID != excludeTeamID {
			return true, nil
		}
	}
	return false, nil
}

func (s *TeamService) GetAvailableUsers(ctx context.Context, teamID uint) ([]user.User, error) {
	return s.teamRepo.GetAvailableUsers(ctx, teamID)
}

func (s *TeamService) AddMember(ctx context.Context, teamID, newMemberID, actorID uint) error {
	if !s.CanManageTeam(ctx, teamID, actorID) {
		return ErrOnlyCaptainCanAdd
	}
	isMember, err := s.teamRepo.IsMember(ctx, teamID, newMemberID)
	if err != nil {
		return err
	}
	if isMember {
		return ErrUserAlreadyInTeam
	}
	// A-5 (pass 45): игрок не может состоять в другой команде.
	inOther, err := s.userInOtherTeam(ctx, newMemberID, teamID)
	if err != nil {
		return err
	}
	if inOther {
		return ErrAlreadyInOtherTeam
	}
	if err := s.teamRepo.AddMember(ctx, teamID, newMemberID); err != nil {
		return err
	}
	s.updateTeamMembersTotal(ctx)
	return nil
}

func (s *TeamService) RemoveMember(ctx context.Context, teamID, memberID, actorID uint) error {
	if !s.CanManageTeam(ctx, teamID, actorID) {
		return ErrNoPermissionRemove
	}
	team, err := s.teamRepo.GetByID(ctx, teamID)
	if err != nil {
		return err
	}
	if team.CaptainID == memberID {
		return ErrCannotRemoveCaptain
	}
	if err := s.teamRepo.RemoveMember(ctx, teamID, memberID); err != nil {
		return err
	}
	s.updateTeamMembersTotal(ctx)
	return nil
}

// LeaveMember — добровольный выход игрока из команды (A-5, pass 45).
// Капитан не может выйти через этот метод — он должен передать капитанство
// или команда остаётся без капитана (админ может удалить).
func (s *TeamService) LeaveMember(ctx context.Context, teamID, userID uint) error {
	team, err := s.teamRepo.GetByID(ctx, teamID)
	if err != nil {
		return err
	}
	if team.CaptainID == userID {
		return errors.New("капитан не может выйти из команды — передайте капитанство сначала")
	}
	if err := s.teamRepo.RemoveMember(ctx, teamID, userID); err != nil {
		return err
	}
	s.updateTeamMembersTotal(ctx)
	return nil
}

// SetMemberRole назначает роль участника (member/deputy) — капитан или админ (A-2).
func (s *TeamService) SetMemberRole(ctx context.Context, teamID, memberID, actorID uint, role string) error {
	if role != MemberRole && role != MemberRoleDeputy {
		return errors.New("неизвестная роль участника")
	}
	if !s.CanManageTeam(ctx, teamID, actorID) {
		return ErrNoPermissionChangeCap
	}
	team, err := s.teamRepo.GetByID(ctx, teamID)
	if err != nil {
		return err
	}
	if team.CaptainID == memberID {
		return errors.New("капитан уже имеет высшую роль")
	}
	return s.teamRepo.SetMemberRole(ctx, teamID, memberID, role)
}

// SetMemberGroup переводит участника в группу (main/reserve) (A-3).
func (s *TeamService) SetMemberGroup(ctx context.Context, teamID, memberID, actorID uint, groupType string) error {
	if groupType != GroupMain && groupType != GroupReserve {
		return errors.New("неизвестная группа")
	}
	if !s.CanManageTeam(ctx, teamID, actorID) {
		return ErrNoPermissionChangeCap
	}
	return s.teamRepo.SetMemberGroup(ctx, teamID, memberID, groupType)
}

// SetFieldRole назначает роль на поле (field/driver/navigator) (A-3).
func (s *TeamService) SetFieldRole(ctx context.Context, teamID, memberID, actorID uint, fieldRole string) error {
	switch fieldRole {
	case FieldRoleField, FieldRoleDriver, FieldRoleNavigator:
	default:
		return errors.New("неизвестная роль на поле")
	}
	if !s.CanManageTeam(ctx, teamID, actorID) {
		return ErrNoPermissionChangeCap
	}
	return s.teamRepo.SetFieldRole(ctx, teamID, memberID, fieldRole)
}

// GetMembersWithRoles возвращает участников с ролями и группами (A-2/A-3).
func (s *TeamService) GetMembersWithRoles(ctx context.Context, teamID uint) ([]TeamMember, error) {
	return s.teamRepo.GetMembersWithRoles(ctx, teamID)
}

func (s *TeamService) ChangeCaptain(ctx context.Context, teamID, newCaptainID, actorID uint, isAdmin bool) error {
	if !isAdmin && !s.CanManageTeam(ctx, teamID, actorID) {
		return ErrNoPermissionChangeCap
	}
	// Captain is always considered a member, even if not in team_members table
	if newCaptainID != actorID {
		isMember, err := s.teamRepo.IsMember(ctx, teamID, newCaptainID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrNewCaptainNotMember
		}
	}
	return s.teamRepo.ChangeCaptain(ctx, teamID, newCaptainID)
}

// updateTeamMembersTotal обновляет gauge с общим количеством участников команд.
// P-45-3 (pass 45): throttle 1 раз в минуту — раньше полный COUNT(*) по всей
// таблице team_members выполнялся на КАЖДУЮ мутацию (AddMember/RemoveMember/
// AcceptInvitation). Метрика не читается запросами, точность ±1 мин достаточна.
func (s *TeamService) updateTeamMembersTotal(ctx context.Context) {
	now := time.Now()
	s.membersMetricMu.Lock()
	if now.Before(s.membersMetricNext) {
		s.membersMetricMu.Unlock()
		return
	}
	s.membersMetricNext = now.Add(time.Minute)
	s.membersMetricMu.Unlock()

	count, err := s.teamRepo.TeamMembersCount(ctx)
	if err != nil {
		return
	}
	metrics.SetTeamMembersTotal(float64(count))
}

// ---------- InvitationService ----------

type InvitationService struct {
	invRepo  InvitationRepository
	teamRepo TeamRepository
	cfg      *config.Config
}

func NewInvitationService(
	invRepo InvitationRepository,
	teamRepo TeamRepository,
	cfg *config.Config,
) *InvitationService {
	return &InvitationService{
		invRepo:  invRepo,
		teamRepo: teamRepo,
		cfg:      cfg,
	}
}

func (s *InvitationService) CreateInvitation(ctx context.Context, teamID, invitedUserID, actorID uint) (*Invitation, error) {
	team, getTeamErr := s.teamRepo.GetByID(ctx, teamID)
	if getTeamErr != nil {
		return nil, getTeamErr
	}

	if team.CaptainID != actorID {
		return nil, ErrOnlyCaptainCanInvite
	}

	// Проверяем, что приглашаемый пользователь существует
	invitedUser, getUserErr := s.teamRepo.GetUserByID(ctx, invitedUserID)
	if getUserErr != nil {
		return nil, ErrUserNotFound
	}

	isMember, memberErr := s.teamRepo.IsMember(ctx, teamID, invitedUserID)
	if memberErr != nil {
		return nil, memberErr
	}
	if isMember || team.CaptainID == invitedUserID {
		return nil, ErrUserAlreadyInTeam
	}

	existing, getExistErr := s.invRepo.GetByTeamAndUser(ctx, teamID, invitedUserID)
	if getExistErr != nil && !errors.Is(getExistErr, gorm.ErrRecordNotFound) {
		return nil, getExistErr
	}
	if existing != nil && existing.Status == InvitationPending {
		return nil, ErrInvitationExists
	}

	inv := &Invitation{
		TeamID: teamID,
		UserID: invitedUserID,
		Status: InvitationPending,
	}
	if createErr := s.invRepo.Create(ctx, inv); createErr != nil {
		return nil, createErr
	}

	if s.cfg != nil && s.cfg.SMTP.Enabled {
		acceptLink := fmt.Sprintf("%s/invitations/%d/accept", s.cfg.Server.BaseURL, inv.ID)
		if emailErr := email.Enqueue(
			invitedUser.Email,
			"Приглашение в команду",
			fmt.Sprintf("Вас пригласили в команду «%s». Принять приглашение: %s", team.Name, acceptLink),
		); emailErr != nil {
			log.Error().Err(emailErr).Str("email", invitedUser.Email).Msg("failed to enqueue invitation email")
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
	inv, getErr := s.invRepo.GetByID(ctx, invitationID)
	if getErr != nil {
		return getErr
	}
	if inv.UserID != userID {
		return errors.New("вы не можете принять это приглашение")
	}
	if inv.Status != InvitationPending {
		return errors.New("приглашение уже обработано")
	}
	// A-5 (pass 45): игрок может принять приглашение только если не состоит
	// в другой команде.
	userTeams, teamErr := s.teamRepo.GetTeamsByUserID(ctx, userID)
	if teamErr != nil {
		return teamErr
	}
	for _, t := range userTeams {
		if t.ID != inv.TeamID {
			return ErrAlreadyInOtherTeam
		}
	}

	tx := s.teamRepo.BeginTransaction(ctx)
	if tx.Error != nil {
		return tx.Error
	}
	defer tx.Rollback()

	// Атомарный claim (C-H2): только если приглашение всё ещё pending.
	// RowsAffected==0 → конкурентный accept уже обработал его.
	res := tx.Model(&Invitation{}).Where("id = ? AND status = ?", invitationID, InvitationPending).Update("status", InvitationAccepted)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("приглашение уже обработано")
	}
	// ON CONFLICT (user_id) DO NOTHING защищает от дубликата team_members И
	// от нарушения уникального индекса «один игрок в одной команде» (000061).
	// DEEP-REVIEW (pass 46): если RowsAffected==0 — конкурентная вставка в другую
	// команду выиграла гонку, возвращаем понятную ошибку.
	ins := tx.Exec("INSERT INTO team_members (team_id, user_id) VALUES (?, ?) ON CONFLICT (user_id) DO NOTHING", inv.TeamID, userID)
	if ins.Error != nil {
		return ins.Error
	}
	if ins.RowsAffected == 0 {
		return ErrAlreadyInOtherTeam
	}

	if commitErr := tx.Commit().Error; commitErr != nil {
		return commitErr
	}

	count, countErr := s.teamRepo.TeamMembersCount(ctx)
	if countErr == nil {
		metrics.SetTeamMembersTotal(float64(count))
	}
	return nil
}

func (s *InvitationService) DeclineInvitation(ctx context.Context, invitationID, userID uint) error {
	inv, getErr := s.invRepo.GetByID(ctx, invitationID)
	if getErr != nil {
		return getErr
	}
	if inv.UserID != userID {
		return errors.New("вы не можете отклонить это приглашение")
	}
	if inv.Status != InvitationPending {
		return errors.New("приглашение уже обработано")
	}
	return s.invRepo.UpdateStatus(ctx, invitationID, InvitationDeclined)
}
