package game

import (
	"context"
	"errors"
	"time"
)

type SimulateService struct {
	gameRepo    GameRepository
	coAuthorSvc *CoAuthorService
}

func NewSimulateService(gameRepo GameRepository, ca *CoAuthorService) *SimulateService {
	return &SimulateService{gameRepo: gameRepo, coAuthorSvc: ca}
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
	game, err := s.gameRepo.GetByIDForSimulation(ctx, gameID)
	if err != nil {
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
	stepStart := startTime
	for i, lvl := range game.Levels {
		code := "невозможно определить"
		if len(lvl.Questions) > 0 && len(lvl.Questions[0].Answers) > 0 {
			code = lvl.Questions[0].Answers[0].Code
		}
		// Имитация задержки: 100ms на уровень вместо 5s
		// Используем select с ctx.Done() чтобы не блокировать горутину пула при отмене
		delay := time.Duration(i+1) * 100 * time.Millisecond
		if delay > 500*time.Millisecond {
			delay = 500 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			timer.Stop()
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
		// L3 (PASS-9): если код определить нельзя — шаг не успешен (раньше
		// всегда Success: true, и «невозможно определить» выглядело прохождением).
		success := code != "невозможно определить"
		step := SimulateStep{LevelName: lvl.Name, Code: code, Duration: time.Since(stepStart), Success: success}
		result.Log = append(result.Log, step)
		result.LevelsPassed++
		stepStart = time.Now()
	}
	result.TotalTime = time.Since(startTime)
	return result, nil
}
