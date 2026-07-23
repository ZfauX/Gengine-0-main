// internal/domain/tournament/repository.go
package tournament

import (
	"context"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
)

type TournamentRepository interface {
	Create(ctx context.Context, t *Tournament) error
	GetByID(ctx context.Context, id uint) (*Tournament, error) // ДОЛЖЕН возвращать два значения
	Update(ctx context.Context, t *Tournament) error
	List(ctx context.Context) ([]Tournament, error)
	Delete(ctx context.Context, id uint) error
}

type TournamentGameRepository interface {
	AddGame(ctx context.Context, tournamentID, gameID uint, order int) error
	RemoveGame(ctx context.Context, tournamentID, gameID uint) error
	ListGames(ctx context.Context, tournamentID uint) ([]game.Game, error)
	GetAvailableGames(ctx context.Context, tournamentID, authorID uint) ([]game.Game, error)
	FindByGameID(ctx context.Context, gameID uint) (*TournamentGame, error)
	ListFinishedPassings(ctx context.Context, gameID uint, status game.GamePassingStatus) ([]game.GamePassing, error)
}

type TournamentTeamRepository interface {
	AddTeam(ctx context.Context, tournamentID, teamID uint) error
	RemoveTeam(ctx context.Context, tournamentID, teamID uint) error
	ListTeams(ctx context.Context, tournamentID uint) ([]team.Team, error)
	GetByTournamentAndTeam(ctx context.Context, tournamentID, teamID uint) (*TournamentTeam, error)
	GetByTournamentAndTeamIDs(ctx context.Context, tournamentID uint, teamIDs []uint) ([]TournamentTeam, error)
	FindByGameAndTeam(ctx context.Context, gameID, teamID uint) (*game.GamePassing, error)
	FindPassingsByGamesAndTeam(ctx context.Context, gameIDs []uint, teamID uint) ([]game.GamePassing, error)
	CreatePassing(ctx context.Context, passing *game.GamePassing) error
	GetTeam(ctx context.Context, teamID uint) (*team.Team, error)
	GetCaptain(ctx context.Context, captainID uint) (*user.User, error)
}

type TournamentResultRepository interface {
	Upsert(ctx context.Context, result *TournamentResult) error
	GetLeaderboard(ctx context.Context, tournamentID uint) ([]TournamentResult, error)
	GetByTournamentAndTeam(ctx context.Context, tournamentID, teamID uint) (*TournamentResult, error)
	GetByTournamentAndTeamIDs(ctx context.Context, tournamentID uint, teamIDs []uint) ([]TournamentResult, error)
}

type gormTournamentRepo struct{ db *gorm.DB }

func NewGormTournamentRepo(db *gorm.DB) TournamentRepository { return &gormTournamentRepo{db} }

func (r *gormTournamentRepo) Create(ctx context.Context, t *Tournament) error {
	return r.db.WithContext(ctx).Create(t).Error
}
func (r *gormTournamentRepo) GetByID(ctx context.Context, id uint) (*Tournament, error) {
	var t Tournament
	err := r.db.WithContext(ctx).Preload("Author").First(&t, id).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormTournamentRepo) Update(ctx context.Context, t *Tournament) error {
	return r.db.WithContext(ctx).Save(t).Error
}
func (r *gormTournamentRepo) List(ctx context.Context) ([]Tournament, error) {
	var tournaments []Tournament
	err := r.db.WithContext(ctx).Preload("Author").Order("created_at DESC").Find(&tournaments).Error
	return tournaments, err
}
func (r *gormTournamentRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Tournament{}, id).Error
}

type gormTournamentGameRepo struct{ db *gorm.DB }

func NewGormTournamentGameRepo(db *gorm.DB) TournamentGameRepository {
	return &gormTournamentGameRepo{db}
}

func (r *gormTournamentGameRepo) AddGame(ctx context.Context, tournamentID, gameID uint, order int) error {
	tg := TournamentGame{
		TournamentID: tournamentID,
		GameID:       gameID,
		OrderIndex:   order,
	}
	return r.db.WithContext(ctx).Create(&tg).Error
}
func (r *gormTournamentGameRepo) RemoveGame(ctx context.Context, tournamentID, gameID uint) error {
	return r.db.WithContext(ctx).Where("tournament_id = ? AND game_id = ?", tournamentID, gameID).Delete(&TournamentGame{}).Error
}
func (r *gormTournamentGameRepo) ListGames(ctx context.Context, tournamentID uint) ([]game.Game, error) {
	var games []game.Game
	err := r.db.WithContext(ctx).Joins("JOIN tournament_games ON tournament_games.game_id = games.id").
		Where("tournament_games.tournament_id = ?", tournamentID).
		Order("tournament_games.order_index ASC").
		Find(&games).Error
	return games, err
}
func (r *gormTournamentGameRepo) GetAvailableGames(ctx context.Context, tournamentID, authorID uint) ([]game.Game, error) {
	var games []game.Game
	subQuery := r.db.WithContext(ctx).Table("tournament_games").Select("game_id").Where("tournament_id = ?", tournamentID)
	err := r.db.WithContext(ctx).Where("author_id = ? AND id NOT IN (?)", authorID, subQuery).Find(&games).Error
	return games, err
}
func (r *gormTournamentGameRepo) FindByGameID(ctx context.Context, gameID uint) (*TournamentGame, error) {
	var tg TournamentGame
	err := r.db.WithContext(ctx).Where("game_id = ?", gameID).First(&tg).Error
	if err != nil {
		return nil, err
	}
	return &tg, nil
}
func (r *gormTournamentGameRepo) ListFinishedPassings(ctx context.Context, gameID uint, status game.GamePassingStatus) ([]game.GamePassing, error) {
	var passings []game.GamePassing
	err := r.db.WithContext(ctx).Where("game_id = ? AND status = ?", gameID, status).Find(&passings).Error
	return passings, err
}

type gormTournamentTeamRepo struct{ db *gorm.DB }

func NewGormTournamentTeamRepo(db *gorm.DB) TournamentTeamRepository {
	return &gormTournamentTeamRepo{db}
}

func (r *gormTournamentTeamRepo) AddTeam(ctx context.Context, tournamentID, teamID uint) error {
	tt := TournamentTeam{
		TournamentID: tournamentID,
		TeamID:       teamID,
	}
	return r.db.WithContext(ctx).Create(&tt).Error
}
func (r *gormTournamentTeamRepo) RemoveTeam(ctx context.Context, tournamentID, teamID uint) error {
	return r.db.WithContext(ctx).Where("tournament_id = ? AND team_id = ?", tournamentID, teamID).Delete(&TournamentTeam{}).Error
}
func (r *gormTournamentTeamRepo) ListTeams(ctx context.Context, tournamentID uint) ([]team.Team, error) {
	var teams []team.Team
	err := r.db.WithContext(ctx).Joins("JOIN tournament_teams ON tournament_teams.team_id = teams.id").
		Where("tournament_teams.tournament_id = ?", tournamentID).
		Find(&teams).Error
	return teams, err
}
func (r *gormTournamentTeamRepo) GetByTournamentAndTeam(ctx context.Context, tournamentID, teamID uint) (*TournamentTeam, error) {
	var tt TournamentTeam
	err := r.db.WithContext(ctx).Where("tournament_id = ? AND team_id = ?", tournamentID, teamID).First(&tt).Error
	if err != nil {
		return nil, err
	}
	return &tt, nil
}
func (r *gormTournamentTeamRepo) GetByTournamentAndTeamIDs(ctx context.Context, tournamentID uint, teamIDs []uint) ([]TournamentTeam, error) {
	var teams []TournamentTeam
	err := r.db.WithContext(ctx).Where("tournament_id = ? AND team_id IN ?", tournamentID, teamIDs).Find(&teams).Error
	return teams, err
}
func (r *gormTournamentTeamRepo) FindByGameAndTeam(ctx context.Context, gameID, teamID uint) (*game.GamePassing, error) {
	var passing game.GamePassing
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ?", gameID, teamID).First(&passing).Error
	if err != nil {
		return nil, err
	}
	return &passing, nil
}
func (r *gormTournamentTeamRepo) FindPassingsByGamesAndTeam(ctx context.Context, gameIDs []uint, teamID uint) ([]game.GamePassing, error) {
	var passings []game.GamePassing
	err := r.db.WithContext(ctx).Where("game_id IN ? AND team_id = ?", gameIDs, teamID).Find(&passings).Error
	return passings, err
}
func (r *gormTournamentTeamRepo) CreatePassing(ctx context.Context, passing *game.GamePassing) error {
	return r.db.WithContext(ctx).Create(passing).Error
}
func (r *gormTournamentTeamRepo) GetTeam(ctx context.Context, teamID uint) (*team.Team, error) {
	var t team.Team
	err := r.db.WithContext(ctx).First(&t, teamID).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *gormTournamentTeamRepo) GetCaptain(ctx context.Context, captainID uint) (*user.User, error) {
	var captain user.User
	err := r.db.WithContext(ctx).First(&captain, captainID).Error
	if err != nil {
		return nil, err
	}
	return &captain, nil
}

type gormTournamentResultRepo struct{ db *gorm.DB }

func NewGormTournamentResultRepo(db *gorm.DB) TournamentResultRepository {
	return &gormTournamentResultRepo{db}
}

func (r *gormTournamentResultRepo) Upsert(ctx context.Context, result *TournamentResult) error {
	return r.db.WithContext(ctx).Save(result).Error
}
func (r *gormTournamentResultRepo) GetLeaderboard(ctx context.Context, tournamentID uint) ([]TournamentResult, error) {
	var results []TournamentResult
	err := r.db.WithContext(ctx).Preload("Team").
		Where("tournament_id = ?", tournamentID).
		Order("score DESC").
		Find(&results).Error
	return results, err
}
func (r *gormTournamentResultRepo) GetByTournamentAndTeam(ctx context.Context, tournamentID, teamID uint) (*TournamentResult, error) {
	var res TournamentResult
	err := r.db.WithContext(ctx).Where("tournament_id = ? AND team_id = ?", tournamentID, teamID).First(&res).Error
	if err != nil {
		return nil, err
	}
	return &res, nil
}
func (r *gormTournamentResultRepo) GetByTournamentAndTeamIDs(ctx context.Context, tournamentID uint, teamIDs []uint) ([]TournamentResult, error) {
	var results []TournamentResult
	err := r.db.WithContext(ctx).Where("tournament_id = ? AND team_id IN ?", tournamentID, teamIDs).Find(&results).Error
	return results, err
}
