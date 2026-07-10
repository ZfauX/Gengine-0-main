// internal/domain/game/game_cover_service.go
package game

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"slices"

	"gengine-0/internal/pkg/storage"

	"github.com/rs/zerolog/log"
)

// GameCoverService отвечает за работу с обложками игр.
type GameCoverService struct {
	gameRepo GameRepository
	storage  storage.FileStorage
	coAuthor *CoAuthorService
}

// NewGameCoverService создаёт новый сервис обложек.
func NewGameCoverService(
	gameRepo GameRepository,
	storage storage.FileStorage,
	coAuthor *CoAuthorService,
) *GameCoverService {
	return &GameCoverService{
		gameRepo: gameRepo,
		storage:  storage,
		coAuthor: coAuthor,
	}
}

// CreateGameWithCover создаёт игру с загрузкой обложки.
func (s *GameCoverService) CreateGameWithCover(ctx context.Context, dto *CreateGameDTO, authorID uint) (*Game, error) {
	game := &Game{
		Name:                 dto.Name,
		Description:          dto.Description,
		MaxTeamNumber:        dto.MaxTeamNumber,
		Visibility:           dto.Visibility,
		StartsAt:             dto.StartsAt,
		RegistrationDeadline: dto.RegistrationDeadline,
		IsDraft:              dto.IsDraft,
		AuthorID:             authorID,
	}

	if dto.CoverFile != nil {
		coverPath, err := s.saveCoverFile(dto.CoverFile, authorID)
		if err != nil {
			return nil, fmt.Errorf("не удалось сохранить обложку: %w", err)
		}
		game.CoverPath = coverPath
	}

	if err := s.gameRepo.Create(ctx, game); err != nil {
		if game.CoverPath != "" {
			if delErr := s.storage.Delete(game.CoverPath); delErr != nil {
				log.Error().Err(delErr).Str("path", game.CoverPath).Msg("CreateGameWithCover: failed to delete orphaned cover")
			}
		}
		return nil, err
	}

	return game, nil
}

// UpdateGameWithCover обновляет игру с возможностью замены или удаления обложки.
func (s *GameCoverService) UpdateGameWithCover(ctx context.Context, gameID uint, dto *UpdateGameDTO, userID uint) error {
	game, err := s.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		return err
	}

	isManager, err := s.coAuthor.HasPermission(gameID, userID, "content")
	if err != nil {
		return fmt.Errorf("ошибка проверки прав: %w", err)
	}
	if !isManager {
		return errors.New("только автор или контент-менеджер может редактировать игру")
	}

	game.Name = dto.Name
	game.Description = dto.Description
	game.MaxTeamNumber = dto.MaxTeamNumber
	game.Visibility = dto.Visibility
	game.StartsAt = dto.StartsAt
	game.RegistrationDeadline = dto.RegistrationDeadline
	// IsDraft не изменяется через Update — только через Publish()

	if dto.DeleteCover {
		if game.CoverPath != "" {
			if err := s.storage.Delete(game.CoverPath); err != nil {
				log.Error().Err(err).Str("path", game.CoverPath).Msg("UpdateGameWithCover: failed to delete cover")
			}
			game.CoverPath = ""
		}
	} else if dto.CoverFile != nil {
		newPath, err := s.saveCoverFile(dto.CoverFile, userID)
		if err != nil {
			return fmt.Errorf("не удалось сохранить новую обложку: %w", err)
		}
		if game.CoverPath != "" {
			if err := s.storage.Delete(game.CoverPath); err != nil {
				log.Error().Err(err).Str("path", game.CoverPath).Msg("UpdateGameWithCover: failed to delete old cover")
			}
		}
		game.CoverPath = newPath
	}

	return s.gameRepo.Update(ctx, game)
}

// saveCoverFile — внутренняя функция для загрузки файла обложки с проверками.
func (s *GameCoverService) saveCoverFile(fileHeader *multipart.FileHeader, userID uint) (string, error) {
	file, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("не удалось открыть файл: %w", err)
	}
	defer func() { _ = file.Close() }()

	if fileHeader.Size > 5*1024*1024 {
		return "", errors.New("размер файла не должен превышать 5 МБ")
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
	contentType := fileHeader.Header.Get("Content-Type")
	if !slices.Contains(allowedTypes, contentType) {
		return "", errors.New("допустимы только JPEG, PNG и WebP")
	}

	webPath, err := s.storage.Save("uploads/covers", file, fileHeader.Filename, userID, 5*1024*1024, allowedTypes)
	if err != nil {
		return "", fmt.Errorf("ошибка сохранения обложки: %w", err)
	}
	return webPath, nil
}
