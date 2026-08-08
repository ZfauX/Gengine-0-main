// internal/domain/game/svc_facade.go
// Фасад-делегатор GameService: вызовы подсервисов (pass 29 — split god-file).
package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gengine-0/internal/pkg/cache"

	"gorm.io/gorm"
)

func (s *GameService) GetUserGamesView(ctx context.Context, userID uint) string {
	if userID == 0 {
		return "table"
	}
	view, err := s.userRepo.GetGamesView(ctx, userID)
	if err != nil {
		return "table"
	}
	if view != "cards" {
		return "table"
	}
	return "cards"
}

// ListReviews делегирует ReviewService.
func (s *GameService) ListReviews(ctx context.Context, gameID uint) ([]Review, error) {
	if s.reviewService == nil {
		return []Review{}, nil
	}
	return s.reviewService.ListByGame(ctx, gameID)
}

// cacheGetRating пытается получить рейтинг из кэша, поддерживая как in-memory, так и Valkey.
func cacheGetRating(store cache.CacheStore, ctx context.Context, key string) (float64, int64, bool) {
	// Valkey: сырые JSON-байты → одна десериализация в struct (P9).
	if vc, ok := store.(*cache.ValkeyCache); ok {
		raw, ok := vc.GetBytesWithCtx(ctx, key)
		if !ok {
			return 0, 0, false
		}
		var rr struct {
			Avg   float64 `json:"avg"`
			Count int64   `json:"count"`
		}
		if err := json.Unmarshal(raw, &rr); err != nil {
			vc.DeleteWithCtx(ctx, key)
			return 0, 0, false
		}
		return rr.Avg, rr.Count, true
	}

	cached, ok := store.GetWithCtx(ctx, key)
	if !ok {
		return 0, 0, false
	}
	switch v := cached.(type) {
	case map[string]any:
		avg, avgOk := v["avg"].(float64)
		if !avgOk {
			return 0, 0, false
		}
		var count int64
		switch cv := v["count"].(type) {
		case int64:
			count = cv
		case float64:
			count = int64(cv)
		case int:
			count = int64(cv)
		default:
			return 0, 0, false
		}
		return avg, count, true
	default:
		return 0, 0, false
	}
}

// GetAverageRating делегирует RatingService (кэш на 5 мин внутри сервиса рейтинга).
func (s *GameService) GetAverageRating(ctx context.Context, gameID uint) (float64, int64, error) {
	if s.ratingService == nil {
		return 0, 0, nil
	}
	return s.ratingService.GetAverageRating(ctx, gameID)
}

// GetGameWithStats объединяет запросы: игра + отзывы + рейтинг (оптимизация Show).
func (s *GameService) GetGameWithStats(ctx context.Context, gameID uint) (*Game, []Review, float64, int64, error) {
	return s.crudService.GetGameWithStats(ctx, gameID)
}

// AutocompleteSearch делегирует GameListingService (для /api/search/games).
func (s *GameService) AutocompleteSearch(ctx context.Context, query string, limit int) ([]AutocompleteItem, error) {
	return s.listingService.AutocompleteSearch(ctx, query, limit)
}

// ShowGame возвращает данные для страницы игры за один вызов:
// игра (с проверкой прав и кэшем) + отзывы + рейтинг.
func (s *GameService) ShowGame(ctx context.Context, gameID, viewerID uint, isAdmin bool) (*Game, []Review, float64, int64, error) {
	game, err := s.GetByID(ctx, gameID, viewerID, isAdmin)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	reviews, avgRating, reviewsCount, err := s.crudService.GetStats(ctx, gameID)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	return game, reviews, avgRating, reviewsCount, nil
}

// IsUserManager делегирует CoAuthorService.
func (s *GameService) IsUserManager(ctx context.Context, gameID, userID uint) (bool, error) {
	return s.coAuthorSvc.IsUserManager(ctx, gameID, userID)
}

// GetPassingByUser возвращает активное passing для игры и пользователя.
// ORDER BY id: при совпадении нескольких статусов выбираем детерминированно (B10).
func (s *GameService) GetPassingByUser(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	return s.gameRepo.GetPassingByUser(ctx, gameID, userID)
}

// GetFinishedPassingForTeam возвращает завершённое прохождение команды (для экспорта результатов).
func (s *GameService) GetFinishedPassingForTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error) {
	return s.gameRepo.GetFinishedPassingByGameAndTeam(ctx, gameID, teamID)
}

// IsTeamCaptain — является ли пользователь капитаном команды (для экспорта результатов).
func (s *GameService) IsTeamCaptain(ctx context.Context, teamID, userID uint) (bool, error) {
	return s.gameRepo.IsTeamCaptain(ctx, teamID, userID)
}

// GetGameByIDUnchecked возвращает игру без кэша и проверки прав (для проверки авторства при экспорте).
func (s *GameService) GetGameByIDUnchecked(ctx context.Context, gameID uint) (*Game, error) {
	return s.crudService.GetByID(ctx, gameID)
}

// GetLogsByGameID возвращает логи игры, отсортированные по времени создания.
func (s *GameService) GetLogsByGameID(ctx context.Context, gameID uint) ([]Log, error) {
	return s.gameRepo.GetLogsByGameID(ctx, gameID)
}

// GetLogsByGameIDPaginated возвращает страницу логов игры.
func (s *GameService) GetLogsByGameIDPaginated(ctx context.Context, gameID uint, page, pageSize int) ([]Log, int64, error) {
	return s.gameRepo.GetLogsByGameIDPaginated(ctx, gameID, page, pageSize)
}

// GetSettingsWithDefaults загружает настройки игры или возвращает значения по умолчанию.
func (s *GameService) GetSettingsWithDefaults(ctx context.Context, gameID uint) (*GameSetting, error) {
	settings, err := s.gameRepo.GetGameSettingByGameID(ctx, gameID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return defaultGameSetting(gameID), nil
		}
		return nil, err
	}
	return settings, nil
}

// SaveSettings сохраняет или обновляет настройки игры.
func (s *GameService) SaveSettings(ctx context.Context, gameID uint, input GameSetting) (*GameSetting, error) {
	settings := GameSetting{
		GameID:                   gameID,
		AllowHints:               input.AllowHints,
		HintPenaltySeconds:       input.HintPenaltySeconds,
		MaxHints:                 input.MaxHints,
		PerLevelTimeLimit:        input.PerLevelTimeLimit,
		HideAnswersUntilFinished: input.HideAnswersUntilFinished,
		AutoStart:                input.AutoStart,
	}

	// Единый upsert в репозитории (H6, pass 30 — сырой SQL не в сервисе).
	if err := s.gameRepo.UpsertGameSetting(ctx, &settings); err != nil {
		return nil, err
	}

	// Инвалидируем кэш game:%d — GameSetting входит в закэшированную игру (P4).
	s.cache.DeleteWithCtx(ctx, fmt.Sprintf("game:%d", gameID))
	s.invalidateGameListCache(ctx)

	return &settings, nil
}
