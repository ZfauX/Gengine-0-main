package game

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/pkg/cache"
	"gengine-0/internal/pkg/metrics"

	"gorm.io/gorm"
)

// levelAnswersCacheTTL (H2, PASS-18): ответы уровня кэшируются на 30с —
// граф Level.Questions.Answers статичен на время игры, но может меняться при
// редактировании модератором; 30с — разумный компромисс между скоростью
// SubmitCode (раньше 2 запроса на КАЖДУЮ попытку) и свежестью.
const levelAnswersCacheTTL = 30 * time.Second

// AttemptService — сервис проверки попыток. Все операции требуют
// переданного *gorm.DB (обычно транзакции) — A-2 (pass 37): удалены не-Tx
// методы (SubmitCode/SubmitFile/AcceptPendingAttempt), которые были мёртвым
// кодом и писали вне транзакции.
type AttemptService struct {
	cache cache.CacheStore
}

func NewAttemptService(c cache.CacheStore) *AttemptService {
	if c == nil {
		c = &cache.NoopCache{}
	}
	return &AttemptService{cache: c}
}

// SubmitCodeWithTx — проверяет код внутри переданной транзакции.
// Возвращает попытку и флаг успеха.
// C-4 (pass 45): если для команды задан персональный ответ уровня
// (level_team_answers), правильным считается именно он (вместо общих ответов).
func (s *AttemptService) SubmitCodeWithTx(ctx context.Context, tx *gorm.DB, progress *LevelProgress, code string, teamID uint) (*Attempt, bool, error) {
	// H2 (PASS-18): граф Level.Questions.Answers грузим с TTL-кэшем (30с) —
	// раньше Preload выполнялся на КАЖДУЮ отправку кода (2 запроса/попытка).
	// Персональные ответы команды (level_team_answers) читаются отдельно ниже.
	lvl := progress.Level
	if lvl.ID == 0 || (lvl.Type != level.TypeFileUpload && lvl.Type != level.TypeBlackbox && !lvl.RequiresConfirmation && len(lvl.Questions) == 0) {
		cached, err := s.loadLevelWithAnswers(ctx, tx, progress.LevelID)
		if err != nil {
			return nil, false, err
		}
		lvl = *cached
	}

	if lvl.Type == level.TypeFileUpload {
		return nil, false, errors.New("на этом уровне ожидается файл, а не текстовый код")
	}

	if lvl.Type == level.TypeBlackbox || lvl.RequiresConfirmation {
		attempt := &Attempt{
			LevelProgressID: progress.ID,
			Code:            code,
			Success:         false,
		}
		if err := tx.WithContext(ctx).Create(attempt).Error; err != nil {
			return nil, false, err
		}
		metrics.IncAttempt(false)
		return attempt, false, nil
	}

	// C-4 (pass 45): персональный ответ команды имеет приоритет.
	if teamID > 0 {
		var teamAnswer LevelTeamAnswer
		if err := tx.WithContext(ctx).Where("level_id = ? AND team_id = ?", lvl.ID, teamID).First(&teamAnswer).Error; err == nil {
			success := strings.EqualFold(teamAnswer.Code, code)
			attempt := &Attempt{
				LevelProgressID: progress.ID,
				Code:            code,
				Success:         success,
			}
			if createErr := tx.WithContext(ctx).Create(attempt).Error; createErr != nil {
				return nil, false, createErr
			}
			metrics.IncAttempt(success)
			return attempt, success, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, err
		}
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
	if err := tx.WithContext(ctx).Create(attempt).Error; err != nil {
		return nil, false, err
	}
	metrics.IncAttempt(success)
	return attempt, success, nil
}

// SubmitFileWithTx создаёт файловую попытку внутри переданной транзакции.
func (s *AttemptService) SubmitFileWithTx(ctx context.Context, tx *gorm.DB, progress *LevelProgress, filePath string) (*Attempt, error) {
	attempt := &Attempt{
		LevelProgressID: progress.ID,
		IsFile:          true,
		FilePath:        filePath,
		Success:         false,
	}
	if err := tx.WithContext(ctx).Create(attempt).Error; err != nil {
		return nil, err
	}
	metrics.IncAttempt(false)
	return attempt, nil
}

// AcceptPendingAttemptWithTx работает в транзакции.
func (s *AttemptService) AcceptPendingAttemptWithTx(ctx context.Context, tx *gorm.DB, progress *LevelProgress) error {
	var lastAttempt Attempt
	err := tx.WithContext(ctx).
		Where("level_progress_id = ? AND success = false", progress.ID).
		Order("created_at DESC").
		First(&lastAttempt).Error
	if err != nil {
		// Различаем «нет ожидающей попытки» и реальную ошибку БД (B3).
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("нет ожидающей попытки для подтверждения")
		}
		return err
	}
	lastAttempt.Success = true
	return tx.WithContext(ctx).Save(&lastAttempt).Error
}

// loadLevelWithAnswers загружает уровень с графом Questions.Answers,
// используя TTL-кэш (H2, PASS-18; H1 fix PASS-19). Ключ level:answers:<id>.
// H1 (PASS-19): cacheGetJSON работает и с Valkey (GetBytesWithCtx +
// json.Unmarshal), и с in-memory — раньше v.(level.Level) не хитился с Valkey
// (JSON → map[string]any), и Preload выполнялся на каждую попытку.
// ВАЖНО: возвращаемое значение НЕ мутировать (контракт иммутабельности кэша).
func (s *AttemptService) loadLevelWithAnswers(ctx context.Context, tx *gorm.DB, levelID uint) (*level.Level, error) {
	key := fmt.Sprintf("level:answers:%d", levelID)
	var lvl level.Level
	if cacheGetJSON(s.cache, ctx, key, &lvl) {
		return &lvl, nil
	}
	if err := tx.WithContext(ctx).Preload("Questions.Answers").First(&lvl, levelID).Error; err != nil {
		return nil, err
	}
	s.cache.SetWithCtx(ctx, key, lvl, levelAnswersCacheTTL)
	return &lvl, nil
}
