package game

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

type SimulateService struct {
	DB        *gorm.DB
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

func (s *SimulateService) Simulate(gameID, userID uint) (*SimulateResult, error) {
	var game Game
	if err := s.DB.Preload("Levels.Questions.Answers").First(&game, gameID).Error; err != nil {
		return nil, err
	}
	isManager, _ := s.coAuthorSvc.IsUserManager(gameID, userID)
	if !isManager {
		return nil, errors.New("только автор или соавтор может запустить симуляцию")
	}
	if len(game.Levels) == 0 {
		return nil, errors.New("игра не содержит уровней")
	}
	result := &SimulateResult{}
	startTime := time.Now()
	for _, lvl := range game.Levels {
		code := "невозможно определить"
		if len(lvl.Questions) > 0 && len(lvl.Questions[0].Answers) > 0 {
			code = lvl.Questions[0].Answers[0].Code
		}
		time.Sleep(5 * time.Second)
		step := SimulateStep{LevelName: lvl.Name, Code: code, Duration: time.Since(startTime), Success: true}
		result.Log = append(result.Log, step)
		result.LevelsPassed++
	}
	result.TotalTime = time.Since(startTime)
	return result, nil
}