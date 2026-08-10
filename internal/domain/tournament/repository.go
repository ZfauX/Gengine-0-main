// internal/domain/tournament/repository.go
package tournament

import (
	"context"

	"gengine-0/internal/domain/game"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/domain/user"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	AddTeamTx(tx *gorm.DB, ctx context.Context, tournamentID, teamID uint) (bool, error)
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
	Upsert(tx *gorm.DB, result *TournamentResult) error
	UpsertMany(tx *gorm.DB, results []TournamentResult) error
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
	// F-4 (pass 35): LIMIT + без тяжёлого Description — листинг не должен
	// тащить всё в память при росте числа турниров.
	err := r.db.WithContext(ctx).
		Select("id, created_at, updated_at, deleted_at, name, author_id, "+
			"points_for_first, points_for_second, points_for_third, points_for_participation").
		// P-43-9 (pass 43): Preload только нужных колонок автора — раньше users.*
		// (password_hash/email) на каждый турнир листинга.
		Preload("Author", func(db *gorm.DB) *gorm.DB {
			return db.Select("id, name, avatar_path")
		}).
		Order("created_at DESC").
		Limit(50).
		Find(&tournaments).Error
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
	// P-43-7 (pass 43): Select(id,name)+LIMIT — UI нужен только dropdown (ID/Name);
	// раньше грузились все строки games.* (description/search_vector) автора.
	subQuery := r.db.WithContext(ctx).Table("tournament_games").Select("game_id").Where("tournament_id = ?", tournamentID)
	err := r.db.WithContext(ctx).
		Select("id, name").
		Where("author_id = ? AND id NOT IN (?)", authorID, subQuery).
		Order("created_at DESC").
		Limit(100).
		Find(&games).Error
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
	// Только ещё не начисленные прохождения — идемпотентность UpdateScoresForGame.
	err := r.db.WithContext(ctx).Where("game_id = ? AND status = ? AND tournament_scored = false", gameID, status).Find(&passings).Error
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

func (r *gormTournamentTeamRepo) AddTeamTx(tx *gorm.DB, ctx context.Context, tournamentID, teamID uint) (bool, error) {
	tt := TournamentTeam{
		TournamentID: tournamentID,
		TeamID:       teamID,
	}
	// ON CONFLICT DO NOTHING (C-H3): конкурентная заявка не вызовет duplicate-key.
	// RowsAffected==0 → команда уже добавлена.
	res := tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tournament_id"}, {Name: "team_id"}},
		DoNothing: true,
	}).Create(&tt)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
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

func (r *gormTournamentResultRepo) Upsert(tx *gorm.DB, result *TournamentResult) error {
	return tx.Save(result).Error
}

// UpsertMany батч-upsert результатов (M9, pass 30): один запрос вместо
// построчного Save на каждую команду в UpdateScoresForGame.
func (r *gormTournamentResultRepo) UpsertMany(tx *gorm.DB, results []TournamentResult) error {
	if len(results) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tournament_id"},
			{Name: "team_id"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"score":        gorm.Expr("tournament_results.score + EXCLUDED.score"),
			"games_played": gorm.Expr("tournament_results.games_played + EXCLUDED.games_played"),
		}),
	}).Create(&results).Error
}
func (r *gormTournamentResultRepo) GetLeaderboard(ctx context.Context, tournamentID uint) ([]TournamentResult, error) {
	var results []TournamentResult
	// P-44-1 (pass 44): Preload только Team.Name + защитный LIMIT — раньше
	// тащили все команды (все колонки) на каждую выборку лидерборда.
	err := r.db.WithContext(ctx).
		Preload("Team", func(db *gorm.DB) *gorm.DB {
			return db.Select("id, name")
		}).
		Where("tournament_id = ?", tournamentID).
		Order("score DESC").
		Limit(100).
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
