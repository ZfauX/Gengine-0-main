package game

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

type PhotoService struct {
	repo       PhotoRepository
	coAuthRepo CoAuthorRepository
}

func NewPhotoService(repo PhotoRepository, coAuthRepo CoAuthorRepository) *PhotoService {
	return &PhotoService{repo: repo, coAuthRepo: coAuthRepo}
}

func (s *PhotoService) List(ctx context.Context, gameID uint) ([]Photo, error) {
	return s.repo.List(ctx, gameID)
}

func (s *PhotoService) Create(ctx context.Context, photo *Photo) error {
	return s.repo.Create(ctx, photo)
}

func (s *PhotoService) GetByID(ctx context.Context, photoID uint) (*Photo, error) {
	return s.repo.GetByID(ctx, photoID)
}

func (s *PhotoService) Delete(ctx context.Context, photoID, userID uint) error {
	photo, err := s.repo.GetByID(ctx, photoID)
	if err != nil {
		return err
	}
	if photo.UserID != userID {
		// #2: автор игры может удалять чужие фото (в хендлере автор проходит
		// IsUserManager, но в co_authors строки у него нет).
		authorID, err := s.repo.GetGameAuthorID(ctx, photo.GameID)
		if err != nil {
			return err
		}
		if authorID == userID {
			return s.repo.Delete(ctx, photo)
		}

		coAuthor, err := s.coAuthRepo.FindByGameAndUser(ctx, photo.GameID, userID)
		if err != nil {
			// C-4: «нет соавтора» — 403; реальная ошибка БД — 500, а не «нет прав».
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("нет прав на удаление фото")
			}
			return err
		}
		// S-6 (pass 32) + P1 (pass 40): общий hasCoAuthorRole — раньше инлайн
		// сравнение дублировало канонический путь (риск расхождения ролей).
		if !hasCoAuthorRole(coAuthor.Role, RoleContentEditor) {
			return errors.New("нет прав на удаление фото")
		}
	}
	return s.repo.Delete(ctx, photo)
}
