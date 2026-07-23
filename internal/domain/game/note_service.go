package game

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

type NoteService struct {
	DB          *gorm.DB
	coAuthorSvc *CoAuthorService
}

func NewNoteService(db *gorm.DB, ca *CoAuthorService) *NoteService {
	return &NoteService{DB: db, coAuthorSvc: ca}
}

func (s *NoteService) ListByGame(ctx context.Context, gameID, userID uint) ([]Note, error) {
	isManager, err := s.coAuthorSvc.IsUserManager(ctx, gameID, userID)
	if err != nil {
		return nil, err
	}
	if !isManager {
		return nil, errors.New("только автор или соавтор может видеть заметки")
	}
	var notes []Note
	err = s.DB.WithContext(ctx).Preload("User").Where("game_id = ?", gameID).Order("created_at DESC").Find(&notes).Error
	return notes, err
}

func (s *NoteService) Create(ctx context.Context, gameID uint, levelID *uint, userID uint, text string) (*Note, error) {
	isManager, err := s.coAuthorSvc.IsUserManager(ctx, gameID, userID)
	if err != nil {
		return nil, err
	}
	if !isManager {
		return nil, errors.New("только автор или соавтор может создавать заметки")
	}
	note := Note{GameID: gameID, LevelID: levelID, UserID: userID, Text: text}
	if err := s.DB.Create(&note).Error; err != nil {
		return nil, err
	}
	if err := s.DB.WithContext(ctx).Preload("User").First(&note, note.ID).Error; err != nil {
		return nil, err
	}
	return &note, nil
}

func (s *NoteService) Delete(ctx context.Context, noteID, userID uint) error {
	var note Note
	if err := s.DB.First(&note, noteID).Error; err != nil {
		return err
	}
	isManager, err := s.coAuthorSvc.IsUserManager(ctx, note.GameID, userID)
	if err != nil {
		return err
	}
	if note.UserID != userID && !isManager {
		return errors.New("нет прав на удаление")
	}
	return s.DB.Delete(&note).Error
}
