// internal/domain/game/game_cover_service.go
package game

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"

	"gengine-0/internal/pkg/storage"
	"gengine-0/internal/pkg/validation"

	"github.com/rs/zerolog/log"
)

const defaultCoverMaxSize = 5 * 1024 * 1024

// GameCoverService отвечает за работу с обложками игр.
type GameCoverService struct {
	gameRepo      GameRepository
	storage       storage.FileStorage
	coAuthor      *CoAuthorService
	maxUploadSize int64
}

// NewGameCoverService создаёт новый сервис обложек.
func NewGameCoverService(
	gameRepo GameRepository,
	storage storage.FileStorage,
	coAuthor *CoAuthorService,
	maxUploadSize int64,
) *GameCoverService {
	if maxUploadSize <= 0 {
		maxUploadSize = defaultCoverMaxSize
	}
	return &GameCoverService{
		gameRepo:      gameRepo,
		storage:       storage,
		coAuthor:      coAuthor,
		maxUploadSize: maxUploadSize,
	}
}

// CreateGameWithCover создаёт игру с загрузкой обложки.
// M1 (PASS-19): игра ВСЕГДА создаётся черновиком (как CRUDService.Create) —
// раньше dto.IsDraft из клиента позволял опубликовать игру БЕЗ уровней,
// минуя guard Publish (проверка CountLevelsByGame). Публикация — только
// через Publish после добавления уровней.
func (s *GameCoverService) CreateGameWithCover(ctx context.Context, dto *CreateGameDTO, authorID uint) (*Game, error) {
	game := &Game{
		Name:                 dto.Name,
		Description:          dto.Description,
		MaxTeamNumber:        dto.MaxTeamNumber,
		Visibility:           dto.Visibility,
		StartsAt:             dto.StartsAt,
		RegistrationDeadline: dto.RegistrationDeadline,
		IsDraft:              true,
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
func (s *GameCoverService) UpdateGameWithCover(ctx context.Context, gameID uint, dto *UpdateGameDTO, userID uint, isAdmin bool) error {
	game, err := s.gameRepo.GetByID(ctx, gameID)
	if err != nil {
		return err
	}

	if !isAdmin {
		isManager, err := s.coAuthor.CanEditContent(ctx, gameID, userID)
		if err != nil {
			return fmt.Errorf("ошибка проверки прав: %w", err)
		}
		if !isManager {
			return errors.New("только автор или контент-менеджер может редактировать игру")
		}
	}

	game.Name = dto.Name
	game.Description = dto.Description
	game.MaxTeamNumber = dto.MaxTeamNumber
	game.Visibility = dto.Visibility
	game.StartsAt = dto.StartsAt
	game.RegistrationDeadline = dto.RegistrationDeadline
	// IsDraft не изменяется через Update — только через Publish()

	// Собираем старые пути для удаления ПОСЛЕ успешного Update (M2, PASS-19):
	// раньше файл удалялся ДО коммита БД — при ошибке Update оставалась битая
	// обложка (в БД старый путь, файла нет).
	var oldCoversToDelete []string
	if dto.DeleteCover {
		if game.CoverPath != "" {
			oldCoversToDelete = append(oldCoversToDelete, game.CoverPath)
			game.CoverPath = ""
		}
	} else if dto.CoverFile != nil {
		newPath, err := s.saveCoverFile(dto.CoverFile, userID)
		if err != nil {
			return fmt.Errorf("не удалось сохранить новую обложку: %w", err)
		}
		if game.CoverPath != "" {
			oldCoversToDelete = append(oldCoversToDelete, game.CoverPath)
		}
		game.CoverPath = newPath
	}

	if err := s.gameRepo.Update(ctx, game); err != nil {
		// M2: Update упал — возвращаем ошибку, старые файлы НЕ удаляем
		// (в БД остался прежний путь). Новый файл (если загружали) — сирота,
		// но целостность БД важнее; cleanup можно добавить отдельно.
		return err
	}

	// M3 (PASS-19): удаляем старые обложки только после успешного Update;
	// ошибка Delete не затирает путь в БД (файл уже отсоединён).
	for _, old := range oldCoversToDelete {
		if delErr := s.storage.Delete(old); delErr != nil {
			log.Error().Err(delErr).Str("path", old).Msg("UpdateGameWithCover: failed to delete old cover")
		}
	}
	return nil
}

// saveCoverFile — внутренняя функция для загрузки файла обложки с проверками.
func (s *GameCoverService) saveCoverFile(fileHeader *multipart.FileHeader, userID uint) (string, error) {
	file, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("не удалось открыть файл: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Debug().Err(closeErr).Msg("game_cover: file close")
		}
	}()

	if fileHeader.Size > s.maxUploadSize {
		return "", fmt.Errorf("размер файла не должен превышать %d МБ", s.maxUploadSize/(1024*1024))
	}

	allowedTypes := validation.AllowedImageTypes
	contentType := fileHeader.Header.Get("Content-Type")
	if !validation.IsAllowedType(contentType, allowedTypes) {
		return "", errors.New("допустимы только JPEG, PNG и WebP")
	}

	webPath, err := s.storage.Save("uploads/covers", file, fileHeader.Filename, userID, s.maxUploadSize, allowedTypes)
	if err != nil {
		return "", fmt.Errorf("ошибка сохранения обложки: %w", err)
	}
	return webPath, nil
}
