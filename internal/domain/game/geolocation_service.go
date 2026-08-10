// internal/domain/game/geolocation_service.go
// G-1..G-4 (pass 45): сервис геолокации игроков.
package game

import (
	"context"
	"time"
)

// GeolocationService — работа с позициями игроков.
type GeolocationService struct {
	repo GeolocationRepository
}

func NewGeolocationService(repo GeolocationRepository) *GeolocationService {
	return &GeolocationService{repo: repo}
}

// UpdateLocation сохраняет позицию игрока в прохождении.
// Проверка членства выполняется вызывающим хендлером.
func (s *GeolocationService) UpdateLocation(ctx context.Context, passingID, teamID, userID uint, lat, lng, accuracy float64) error {
	loc := &PlayerLocation{
		GamePassingID: passingID,
		TeamID:        teamID,
		UserID:        userID,
		Latitude:      lat,
		Longitude:     lng,
		Accuracy:      accuracy,
	}
	return s.repo.UpsertLocation(ctx, loc)
}

// LocationsByGame возвращает позиции всех игроков игры.
func (s *GeolocationService) LocationsByGame(ctx context.Context, gameID uint) ([]PlayerLocation, error) {
	return s.repo.GetLocationsByGame(ctx, gameID)
}

// freshWindow — окно «свежести» позиции (маркер считается активным).
const freshWindow = 2 * time.Minute

// IsFresh возвращает true, если позиция обновлялась в окне свежести.
func (l PlayerLocation) IsFresh(now time.Time) bool {
	return now.Sub(l.UpdatedAt) <= freshWindow
}
