package game

import (
	"context"
	"errors"
)

// ErrNoteForbidden (DEEP-REVIEW PASS-2 #10): нет прав на заметку —
// хендлер мапит её в 403, прочие ошибки — в 500 (не сырой текст).
var ErrNoteForbidden = errors.New("нет прав на заметку")

type NoteService struct {
	repo        NoteRepository
	coAuthorSvc *CoAuthorService
}

func NewNoteService(repo NoteRepository, ca *CoAuthorService) *NoteService {
	return &NoteService{repo: repo, coAuthorSvc: ca}
}

func (s *NoteService) ListByGame(ctx context.Context, gameID, userID uint) ([]Note, error) {
	isManager, err := s.coAuthorSvc.IsUserManager(ctx, gameID, userID)
	if err != nil {
		return nil, err
	}
	if !isManager {
		return nil, ErrNoteForbidden
	}
	return s.repo.ListByGame(ctx, gameID)
}

// ErrNoteInvalidLevel (DEEP-REVIEW PASS-2 #19): переданный level_id не
// принадлежит игре — заметку нельзя привязывать к чужому уровню.
var ErrNoteInvalidLevel = errors.New("уровень не принадлежит игре")

func (s *NoteService) Create(ctx context.Context, gameID uint, levelID *uint, userID uint, text string) (*Note, error) {
	isManager, err := s.coAuthorSvc.IsUserManager(ctx, gameID, userID)
	if err != nil {
		return nil, err
	}
	if !isManager {
		return nil, ErrNoteForbidden
	}
	if levelID != nil {
		// DEEP-REVIEW PASS-2 (#19): валидируем, что уровень действительно
		// принадлежит этой игре. Раньше можно было создать заметку со ссылкой
		// на уровень другой игры (cross-game reference).
		belongs, lvlErr := s.repo.LevelBelongsToGame(ctx, gameID, *levelID)
		if lvlErr != nil {
			return nil, lvlErr
		}
		if !belongs {
			return nil, ErrNoteInvalidLevel
		}
	}
	note := Note{GameID: gameID, LevelID: levelID, UserID: userID, Text: text}
	if err := s.repo.Create(ctx, &note); err != nil {
		return nil, err
	}
	return &note, nil
}

func (s *NoteService) Delete(ctx context.Context, noteID, userID uint) error {
	note, err := s.repo.GetByID(ctx, noteID)
	if err != nil {
		return err
	}
	isManager, err := s.coAuthorSvc.IsUserManager(ctx, note.GameID, userID)
	if err != nil {
		return err
	}
	if note.UserID != userID && !isManager {
		return ErrNoteForbidden
	}
	return s.repo.Delete(ctx, note)
}
