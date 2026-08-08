// internal/domain/game/repository.go
package game

import (
	"context"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/pkg/sqlutil"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GameRepository определяет контракт для работы с играми.
type GameRepository interface {
	Create(ctx context.Context, game *Game) error
	GetByID(ctx context.Context, id uint) (*Game, error)
	GetByIDPreloaded(ctx context.Context, id uint) (*Game, error)
	Update(ctx context.Context, game *Game) error
	Delete(ctx context.Context, id uint) error
	Save(ctx context.Context, game *Game) error
	Model(ctx context.Context) *gorm.DB
	// Новый метод для календаря
	ListByDateRange(ctx context.Context, from, to time.Time) ([]Game, error)
	// Read-хелперы для сервисного слоя (C1 — без прямого *gorm.DB в сервисах).
	GetPassingByUser(ctx context.Context, gameID, userID uint) (*GamePassing, error)
	GetFinishedPassingByGameAndTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error)
	GetLogsByGameID(ctx context.Context, gameID uint) ([]Log, error)
	GetLogsByGameIDPaginated(ctx context.Context, gameID uint, page, pageSize int) ([]Log, int64, error)
	GetGameSettingByGameID(ctx context.Context, gameID uint) (*GameSetting, error)
	UpsertGameSetting(ctx context.Context, settings *GameSetting) error
	IsTeamCaptain(ctx context.Context, teamID, userID uint) (bool, error)
	IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error)
	// Типизированные счётчики (A-5, pass 32) — заменяют escape-hatch Model(ctx).
	CountActivePassings(ctx context.Context, gameID uint) (int64, error)
	CountLevelsByGame(ctx context.Context, gameID uint) (int64, error)
	CountPublished(ctx context.Context) (int64, error)
	// AdminListGames — админ-листинг с фильтрами (A-5, pass 32): инкапсулирует
	// динамическую GORM-цепочку, которую раньше строил handler через Model(ctx).
	AdminListGames(ctx context.Context, query, status string, offset, limit int) ([]Game, int64, error)
}

// GamePassingRepository — контракт для прохождений.
type GamePassingRepository interface {
	Create(ctx context.Context, passing *GamePassing) error
	GetByID(ctx context.Context, id uint) (*GamePassing, error)
	FindByGameAndTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error)
	FindActiveByGame(ctx context.Context, gameID uint) ([]GamePassing, error)
	UpdateStatus(ctx context.Context, id uint, status GamePassingStatus) error
	Save(ctx context.Context, passing *GamePassing) error
}

// ---------- GORM implementations ----------

type gormGameRepo struct{ db *gorm.DB }

func NewGormGameRepo(db *gorm.DB) GameRepository { return &gormGameRepo{db} }

func (r *gormGameRepo) Create(ctx context.Context, game *Game) error {
	return r.db.WithContext(ctx).Create(game).Error
}
func (r *gormGameRepo) GetByID(ctx context.Context, id uint) (*Game, error) {
	var g Game
	err := r.db.WithContext(ctx).First(&g, id).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}
func (r *gormGameRepo) GetByIDPreloaded(ctx context.Context, id uint) (*Game, error) {
	var g Game
	err := r.db.WithContext(ctx).Preload("Author").Preload("GameSetting").First(&g, id).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}
func (r *gormGameRepo) Update(ctx context.Context, game *Game) error {
	return r.db.WithContext(ctx).Save(game).Error
}
func (r *gormGameRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Game{}, id).Error
}
func (r *gormGameRepo) Save(ctx context.Context, game *Game) error {
	return r.db.WithContext(ctx).Save(game).Error
}
func (r *gormGameRepo) Model(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).Model(&Game{})
}

// ListByDateRange возвращает опубликованные публичные игры за указанный период.
func (r *gormGameRepo) ListByDateRange(ctx context.Context, from, to time.Time) ([]Game, error) {
	var games []Game
	err := r.db.WithContext(ctx).
		Preload("Author").
		Where("is_draft = false AND visibility = 'public' AND starts_at BETWEEN ? AND ?", from, to).
		Order("starts_at ASC").
		Find(&games).Error
	return games, err
}

func (r *gormGameRepo) GetPassingByUser(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	var passing GamePassing
	err := r.db.WithContext(ctx).
		Joins("JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.game_id = ? AND game_passings.status IN (?,?) AND team_members.user_id = ?",
			gameID, StatusAccepted, StatusStarted, userID).
		Order("game_passings.id ASC").
		First(&passing).Error
	if err != nil {
		return nil, err
	}
	return &passing, nil
}

func (r *gormGameRepo) GetFinishedPassingByGameAndTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error) {
	var passing GamePassing
	err := r.db.WithContext(ctx).
		Where("game_id = ? AND team_id = ? AND status = ?", gameID, teamID, StatusFinished).
		First(&passing).Error
	if err != nil {
		return nil, err
	}
	return &passing, nil
}

func (r *gormGameRepo) GetLogsByGameID(ctx context.Context, gameID uint) ([]Log, error) {
	var logs []Log
	err := r.db.WithContext(ctx).
		Joins("JOIN game_passings ON game_passings.id = logs.game_passing_id").
		Where("game_passings.game_id = ?", gameID).
		Order("logs.created_at ASC").
		Find(&logs).Error
	return logs, err
}

// GetLogsByGameIDPaginated возвращает страницу логов игры (H6, pass 30 —
// перенесено из svc_facade: сырой SQL должен жить в репозитории).
func (r *gormGameRepo) GetLogsByGameIDPaginated(ctx context.Context, gameID uint, page, pageSize int) ([]Log, int64, error) {
	var total int64
	db := r.db.WithContext(ctx).Session(&gorm.Session{NewDB: true})
	if err := db.Model(&Log{}).
		Joins("JOIN game_passings ON game_passings.id = logs.game_passing_id").
		Where("game_passings.game_id = ?", gameID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	} else if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize
	var logs []Log
	err := db.
		Joins("JOIN game_passings ON game_passings.id = logs.game_passing_id").
		Where("game_passings.game_id = ?", gameID).
		Order("logs.created_at ASC").
		Limit(pageSize).Offset(offset).
		Find(&logs).Error
	return logs, total, err
}

func (r *gormGameRepo) GetGameSettingByGameID(ctx context.Context, gameID uint) (*GameSetting, error) {
	var settings GameSetting
	err := r.db.WithContext(ctx).Where("game_id = ?", gameID).First(&settings).Error
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpsertGameSetting сохраняет или обновляет настройки одним upsert-запросом
// (H6, pass 30). Единый OnConflict (B4): update-then-insert имел гонку.
func (r *gormGameRepo) UpsertGameSetting(ctx context.Context, settings *GameSetting) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "game_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"allow_hints":                 settings.AllowHints,
			"hint_penalty_seconds":        settings.HintPenaltySeconds,
			"max_hints":                   settings.MaxHints,
			"per_level_time_limit":        settings.PerLevelTimeLimit,
			"hide_answers_until_finished": settings.HideAnswersUntilFinished,
			"auto_start":                  settings.AutoStart,
		}),
	}).Create(settings).Error
}

func (r *gormGameRepo) IsTeamCaptain(ctx context.Context, teamID, userID uint) (bool, error) {
	var capt struct{ CaptainID uint }
	err := r.db.WithContext(ctx).Table("teams").Select("captain_id").Where("id = ?", teamID).First(&capt).Error
	if err != nil {
		return false, err
	}
	return capt.CaptainID == userID, nil
}

// IsTeamMember проверяет членство пользователя в команде (A-1, pass 31:
// использовалось напрямую в SSE-хендлере через raw-запрос).
func (r *gormGameRepo) IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("team_members").
		Where("team_id = ? AND user_id = ?", teamID, userID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// CountActivePassings — количество активных прохождений игры
// (A-5, pass 32: типизированная замена Model(ctx).Model(&GamePassing{})).
func (r *gormGameRepo) CountActivePassings(ctx context.Context, gameID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&GamePassing{}).
		Where("game_id = ? AND status IN ?", gameID, []GamePassingStatus{StatusStarted, StatusTesting}).
		Count(&count).Error
	return count, err
}

// CountLevelsByGame — количество уровней игры (A-5, pass 32).
func (r *gormGameRepo) CountLevelsByGame(ctx context.Context, gameID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&level.Level{}).Where("game_id = ?", gameID).Count(&count).Error
	return count, err
}

// CountPublished — количество опубликованных игр (метрики, A-5 pass 32).
func (r *gormGameRepo) CountPublished(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Game{}).Where("is_draft = false").Count(&count).Error
	return count, err
}

// AdminListGames — админ-листинг с фильтрами (A-5, pass 32).
func (r *gormGameRepo) AdminListGames(ctx context.Context, query, status string, offset, limit int) ([]Game, int64, error) {
	db := r.db.WithContext(ctx).Model(&Game{}).Preload("Author")
	if query != "" {
		like := sqlutil.BuildLikePattern(query)
		db = db.Joins("LEFT JOIN users ON users.id = games.author_id").
			Where("games.name ILIKE ? OR users.name ILIKE ?", like, like)
	}
	switch status {
	case "draft":
		db = db.Where("is_draft = true")
	case "published":
		db = db.Where("is_draft = false")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var games []Game
	if err := db.Order("id DESC").Offset(offset).Limit(limit).Find(&games).Error; err != nil {
		return nil, 0, err
	}
	return games, total, nil
}

type gormGamePassingRepo struct{ db *gorm.DB }

func NewGormGamePassingRepo(db *gorm.DB) GamePassingRepository { return &gormGamePassingRepo{db} }

func (r *gormGamePassingRepo) Create(ctx context.Context, passing *GamePassing) error {
	return r.db.WithContext(ctx).Create(passing).Error
}
func (r *gormGamePassingRepo) GetByID(ctx context.Context, id uint) (*GamePassing, error) {
	var p GamePassing
	err := r.db.WithContext(ctx).First(&p, id).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}
func (r *gormGamePassingRepo) FindByGameAndTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error) {
	var p GamePassing
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ?", gameID, teamID).First(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}
func (r *gormGamePassingRepo) FindActiveByGame(ctx context.Context, gameID uint) ([]GamePassing, error) {
	var passings []GamePassing
	err := r.db.WithContext(ctx).Where("game_id = ? AND status = ?", gameID, StatusStarted).Find(&passings).Error
	return passings, err
}
func (r *gormGamePassingRepo) UpdateStatus(ctx context.Context, id uint, status GamePassingStatus) error {
	return r.db.WithContext(ctx).Model(&GamePassing{}).Where("id = ?", id).Update("status", status).Error
}
func (r *gormGamePassingRepo) Save(ctx context.Context, passing *GamePassing) error {
	return r.db.WithContext(ctx).Save(passing).Error
}
