package game

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type SimulateService struct {
	DB          *gorm.DB
	coAuthorSvc *CoAuthorService
}

func NewSimulateService(db *gorm.DB, ca *CoAuthorService) *SimulateService {
	return &SimulateService{DB: db, coAuthorSvc: ca}
}

type SimulateResult struct {
	TotalTime    time.Duration
	LevelsPassed int
	Log          []SimulateStep
}

type SimulateStep struct {
	LevelName string
	Code      string
	Duration  time.Duration
	Success   bool
}

func (s *SimulateService) Simulate(ctx context.Context, gameID, userID uint) (*SimulateResult, error) {
	var game Game
	if err := s.DB.WithContext(ctx).Preload("Levels.Questions.Answers").First(&game, gameID).Error; err != nil {
		return nil, err
	}
	isManager, err := s.coAuthorSvc.IsUserManager(ctx, gameID, userID)
	if err != nil {
		return nil, err
	}
	if !isManager {
		return nil, errors.New("только автор или соавтор может запустить симуляцию")
	}
	if len(game.Levels) == 0 {
		return nil, errors.New("игра не содержит уровней")
	}
	result := &SimulateResult{}
	startTime := time.Now()
	for i, lvl := range game.Levels {
		code := "невозможно определить"
		if len(lvl.Questions) > 0 && len(lvl.Questions[0].Answers) > 0 {
			code = lvl.Questions[0].Answers[0].Code
		}
		// Имитация задержки: 100ms на уровень вместо 5s
		delay := time.Duration(i+1) * 100 * time.Millisecond
		if delay > 500*time.Millisecond {
			delay = 500 * time.Millisecond
		}
		time.Sleep(delay)
		step := SimulateStep{LevelName: lvl.Name, Code: code, Duration: time.Since(startTime), Success: true}
		result.Log = append(result.Log, step)
		result.LevelsPassed++
	}
	result.TotalTime = time.Since(startTime)
	return result, nil
}
