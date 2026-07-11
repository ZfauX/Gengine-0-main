package game

import (
	"errors"
	"strings"

	"gengine-0/internal/domain/level"

	"gorm.io/gorm"
)

type AttemptService struct {
	DB *gorm.DB
}

func NewAttemptService(db *gorm.DB) *AttemptService {
	return &AttemptService{DB: db}
}

// SubmitCode проверяет введённый код для указанного прогресса уровня.
// Делегирует SubmitCodeWithTx для единообразной обработки.
func (s *AttemptService) SubmitCode(progress *LevelProgress, code string) (*Attempt, bool, error) {
	return s.SubmitCodeWithTx(s.DB, progress, code)
}

// SubmitCodeWithTx — проверяет код внутри переданной транзакции.
// Возвращает попытку и флаг успеха.
func (s *AttemptService) SubmitCodeWithTx(tx *gorm.DB, progress *LevelProgress, code string) (*Attempt, bool, error) {
	var lvl level.Level
	if err := tx.Preload("Questions.Answers").First(&lvl, progress.LevelID).Error; err != nil {
		return nil, false, err
	}

	if lvl.Type == level.TypeBlackbox || lvl.RequiresConfirmation {
		attempt := &Attempt{
			LevelProgressID: progress.ID,
			Code:            code,
			Success:         false,
		}
		if err := tx.Create(attempt).Error; err != nil {
			return nil, false, err
		}
		return attempt, false, nil
	}

	success := false
	for _, q := range lvl.Questions {
		for _, a := range q.Answers {
			if strings.EqualFold(a.Code, code) {
				success = true
				break
			}
		}
		if success {
			break
		}
	}

	attempt := &Attempt{
		LevelProgressID: progress.ID,
		Code:            code,
		Success:         success,
	}
	if err := tx.Create(attempt).Error; err != nil {
		return nil, false, err
	}
	return attempt, success, nil
}

// SubmitFile создаёт файловую попытку.
// Делегирует SubmitFileWithTx для единообразной обработки.
func (s *AttemptService) SubmitFile(progress *LevelProgress, filePath string) (*Attempt, error) {
	return s.SubmitFileWithTx(s.DB, progress, filePath)
}

// SubmitFileWithTx создаёт файловую попытку внутри переданной транзакции.
func (s *AttemptService) SubmitFileWithTx(tx *gorm.DB, progress *LevelProgress, filePath string) (*Attempt, error) {
	attempt := &Attempt{
		LevelProgressID: progress.ID,
		IsFile:          true,
		FilePath:        filePath,
		Success:         false,
	}
	if err := tx.Create(attempt).Error; err != nil {
		return nil, err
	}
	return attempt, nil
}

// AcceptPendingAttempt помечает последнюю неподтверждённую попытку как успешную.
// Делегирует AcceptPendingAttemptWithTx для единообразной обработки.
func (s *AttemptService) AcceptPendingAttempt(progress *LevelProgress) error {
	return s.AcceptPendingAttemptWithTx(s.DB, progress)
}

// AcceptPendingAttemptWithTx работает в транзакции.
func (s *AttemptService) AcceptPendingAttemptWithTx(tx *gorm.DB, progress *LevelProgress) error {
	var lastAttempt Attempt
	err := tx.
		Where("level_progress_id = ? AND success = false", progress.ID).
		Order("created_at DESC").
		First(&lastAttempt).Error
	if err != nil {
		return errors.New("нет ожидающей попытки для подтверждения")
	}
	lastAttempt.Success = true
	return tx.Save(&lastAttempt).Error
}
