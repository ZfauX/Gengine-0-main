// internal/domain/game/repository.go
package game

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// GameRepository определяет контракт для работы с играми.
type GameRepository interface {
	Create(ctx context.Context, game *Game) error
	GetByID(ctx context.Context, id uint) (*Game, error)
	GetByIDPreloaded(ctx context.Context, id uint) (*Game, error)
	Update(ctx context.Context, game *Game) error
	Delete(ctx context.Context, id uint) error
	Save(ctx context.Context, game *Game) error
	Count(ctx context.Context, query *gorm.DB) (int64, error)
	ListFiltered(ctx context.Context, query *gorm.DB, offset, limit int) ([]Game, error)
	Model(ctx context.Context) *gorm.DB
	// Новый метод для календаря
	ListByDateRange(ctx context.Context, from, to time.Time) ([]Game, error)
	// Добавлен метод для получения *gorm.DB с контекстом
	DB(ctx context.Context) *gorm.DB
}

// GamePassingRepository — контракт для прохождений.
type GamePassingRepository interface {
	Create(ctx context.Context, passing *GamePassing) error
	GetByID(ctx context.Context, id uint) (*GamePassing, error)
	FindByGameAndTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error)
	FindActiveByGame(ctx context.Context, gameID uint) ([]GamePassing, error)
	UpdateStatus(ctx context.Context, id uint, status string) error
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
	return &g, err
}
func (r *gormGameRepo) GetByIDPreloaded(ctx context.Context, id uint) (*Game, error) {
	var g Game
	err := r.db.WithContext(ctx).Preload("Author").Preload("GameSetting").First(&g, id).Error
	return &g, err
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
func (r *gormGameRepo) Count(ctx context.Context, query *gorm.DB) (int64, error) {
	var total int64
	err := query.WithContext(ctx).Count(&total).Error
	return total, err
}
func (r *gormGameRepo) ListFiltered(ctx context.Context, query *gorm.DB, offset, limit int) ([]Game, error) {
	var games []Game
	err := query.WithContext(ctx).Offset(offset).Limit(limit).Find(&games).Error
	return games, err
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

// DB возвращает *gorm.DB с контекстом.
func (r *gormGameRepo) DB(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx)
}

type gormGamePassingRepo struct{ db *gorm.DB }

func NewGormGamePassingRepo(db *gorm.DB) GamePassingRepository { return &gormGamePassingRepo{db} }

func (r *gormGamePassingRepo) Create(ctx context.Context, passing *GamePassing) error {
	return r.db.WithContext(ctx).Create(passing).Error
}
func (r *gormGamePassingRepo) GetByID(ctx context.Context, id uint) (*GamePassing, error) {
	var p GamePassing
	err := r.db.WithContext(ctx).First(&p, id).Error
	return &p, err
}
func (r *gormGamePassingRepo) FindByGameAndTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error) {
	var p GamePassing
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ?", gameID, teamID).First(&p).Error
	return &p, err
}
func (r *gormGamePassingRepo) FindActiveByGame(ctx context.Context, gameID uint) ([]GamePassing, error) {
	var passings []GamePassing
	err := r.db.WithContext(ctx).Where("game_id = ? AND status = ?", gameID, StatusStarted).Find(&passings).Error
	return passings, err
}
func (r *gormGamePassingRepo) UpdateStatus(ctx context.Context, id uint, status string) error {
	return r.db.WithContext(ctx).Model(&GamePassing{}).Where("id = ?", id).Update("status", status).Error
}
func (r *gormGamePassingRepo) Save(ctx context.Context, passing *GamePassing) error {
	return r.db.WithContext(ctx).Save(passing).Error
}
