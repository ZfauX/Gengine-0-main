package game

import (
	"errors"
	"gorm.io/gorm"
)

type NoteService struct {
	DB        *gorm.DB
	coAuthorSvc *CoAuthorService
}

func NewNoteService(db *gorm.DB, ca *CoAuthorService) *NoteService {
	return &NoteService{DB: db, coAuthorSvc: ca}
}

func (s *NoteService) ListByGame(gameID, userID uint) ([]Note, error) {
	isManager, _ := s.coAuthorSvc.IsUserManager(gameID, userID)
	if !isManager {
		return nil, errors.New("только автор или соавтор может видеть заметки")
	}
	var notes []Note
	err := s.DB.Preload("User").Where("game_id = ?", gameID).Order("created_at DESC").Find(&notes).Error
	return notes, err
}

func (s *NoteService) Create(gameID uint, levelID *uint, userID uint, text string) (*Note, error) {
	isManager, _ := s.coAuthorSvc.IsUserManager(gameID, userID)
	if !isManager {
		return nil, errors.New("только автор или соавтор может создавать заметки")
	}
	note := Note{GameID: gameID, LevelID: levelID, UserID: userID, Text: text}
	if err := s.DB.Create(&note).Error; err != nil {
		return nil, err
	}
	s.DB.Preload("User").First(&note, note.ID)
	return &note, nil
}

func (s *NoteService) Delete(noteID, userID uint) error {
	var note Note
	if err := s.DB.First(&note, noteID).Error; err != nil {
		return err
	}
	isManager, _ := s.coAuthorSvc.IsUserManager(note.GameID, userID)
	if note.UserID != userID && !isManager {
		return errors.New("нет прав на удаление")
	}
	return s.DB.Delete(&note).Error
}