// internal/domain/tournament/repository.go
package tournament

import (
	"context"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"

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
}

type TournamentTeamRepository interface {
	AddTeam(ctx context.Context, tournamentID, teamID uint) error
	RemoveTeam(ctx context.Context, tournamentID, teamID uint) error
	ListTeams(ctx context.Context, tournamentID uint) ([]team.Team, error)
	GetByTournamentAndTeam(ctx context.Context, tournamentID, teamID uint) (*TournamentTeam, error)
}

type TournamentResultRepository interface {
	Upsert(ctx context.Context, result *TournamentResult) error
	GetLeaderboard(ctx context.Context, tournamentID uint) ([]TournamentResult, error)
	GetByTournamentAndTeam(ctx context.Context, tournamentID, teamID uint) (*TournamentResult, error)
}

type gormTournamentRepo struct{ db *gorm.DB }

func NewGormTournamentRepo(db *gorm.DB) TournamentRepository { return &gormTournamentRepo{db} }

func (r *gormTournamentRepo) Create(ctx context.Context, t *Tournament) error {
	return r.db.WithContext(ctx).Create(t).Error
}
func (r *gormTournamentRepo) GetByID(ctx context.Context, id uint) (*Tournament, error) {
	var t Tournament
	err := r.db.WithContext(ctx).Preload("Author").First(&t, id).Error
	return &t, err
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
	return &tt, err
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
	return &res, err
}
