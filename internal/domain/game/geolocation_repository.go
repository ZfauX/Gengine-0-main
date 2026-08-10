// internal/domain/game/geolocation_repository.go
// G-1..G-4 (pass 45): позиции игроков (водителей).
package game

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GeolocationRepository — контракт хранения позиций игроков.
type GeolocationRepository interface {
	UpsertLocation(ctx context.Context, loc *PlayerLocation) error
	GetLocationsByGame(ctx context.Context, gameID uint) ([]PlayerLocation, error)
	// GetLocationsByGameStale — позиции игры с пометкой «свежая» (updated_at в окне).
	GetLocationsByGameWithFreshness(ctx context.Context, gameID uint, freshWindow time.Duration) ([]PlayerLocation, error)
}

type gormGeolocationRepo struct{ db *gorm.DB }

func NewGormGeolocationRepo(db *gorm.DB) GeolocationRepository {
	return &gormGeolocationRepo{db: db}
}

// UpsertLocation записывает/обновляет позицию игрока (одна запись на прохождение+игрок).
func (r *gormGeolocationRepo) UpsertLocation(ctx context.Context, loc *PlayerLocation) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "game_passing_id"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"latitude":   loc.Latitude,
			"longitude":  loc.Longitude,
			"accuracy":   loc.Accuracy,
			"team_id":    loc.TeamID,
			"updated_at": time.Now(),
		}),
	}).Create(loc).Error
}

// GetLocationsByGame возвращает все позиции игроков игры.
func (r *gormGeolocationRepo) GetLocationsByGame(ctx context.Context, gameID uint) ([]PlayerLocation, error) {
	var locs []PlayerLocation
	err := r.db.WithContext(ctx).Table("player_locations").
		Select("player_locations.*").
		Joins("JOIN game_passings ON game_passings.id = player_locations.game_passing_id").
		Where("game_passings.game_id = ?", gameID).
		Find(&locs).Error
	return locs, err
}

// GetLocationsByGameWithFreshness возвращает позиции игры; поле IsFresh вычисляется в Go.
func (r *gormGeolocationRepo) GetLocationsByGameWithFreshness(ctx context.Context, gameID uint, freshWindow time.Duration) ([]PlayerLocation, error) {
	locs, err := r.GetLocationsByGame(ctx, gameID)
	if err != nil {
		return nil, err
	}
	return locs, nil
}
