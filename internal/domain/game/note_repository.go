// internal/domain/game/note_repository.go
// A-2 (pass 31): репозиторий заметок — NoteService не обращается к *gorm.DB.
package game

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// NoteRepository — контракт для заметок.
type NoteRepository interface {
	ListByGame(ctx context.Context, gameID uint) ([]Note, error)
	GetByID(ctx context.Context, id uint) (*Note, error)
	Create(ctx context.Context, note *Note) error
	Delete(ctx context.Context, note *Note) error
}

type gormNoteRepo struct{ db *gorm.DB }

func NewGormNoteRepo(db *gorm.DB) NoteRepository {
	return &gormNoteRepo{db: db}
}

func (r *gormNoteRepo) ListByGame(ctx context.Context, gameID uint) ([]Note, error) {
	var notes []Note
	err := r.db.WithContext(ctx).Preload("User", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, name, avatar_path")
	}).Where("game_id = ?", gameID).Order("created_at DESC").Find(&notes).Error
	return notes, err
}

func (r *gormNoteRepo) GetByID(ctx context.Context, id uint) (*Note, error) {
	var note Note
	err := r.db.WithContext(ctx).First(&note, id).Error
	if err != nil {
		return nil, err
	}
	return &note, nil
}

func (r *gormNoteRepo) Create(ctx context.Context, note *Note) error {
	return r.db.WithContext(ctx).Create(note).Error
}

func (r *gormNoteRepo) Delete(ctx context.Context, note *Note) error {
	return r.db.WithContext(ctx).Delete(note).Error
}

var _ NoteRepository = (*gormNoteRepo)(nil)

// ErrNoteNotFound — заметка не найдена.
var ErrNoteNotFound = errors.New("заметка не найдена")
