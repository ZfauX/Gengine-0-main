// internal/domain/game/repository.go
package game

import (
	"context"
	"strings"
	"time"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/user"
	"gengine-0/internal/pkg/sqlutil"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GameRepository определяет контракт для работы с играми.
type GameRepository interface {
	Create(ctx context.Context, game *Game) error
	GetByID(ctx context.Context, id uint) (*Game, error)
	GetByIDPreloaded(ctx context.Context, id uint) (*Game, error)
	Update(ctx context.Context, game *Game) error
	Delete(ctx context.Context, id uint) error
	Save(ctx context.Context, game *Game) error
	// Новый метод для календаря
	ListByDateRange(ctx context.Context, from, to time.Time) ([]Game, error)
	// Read-хелперы для сервисного слоя (C1 — без прямого *gorm.DB в сервисах).
	GetPassingByUser(ctx context.Context, gameID, userID uint) (*GamePassing, error)
	GetFinishedPassingByGameAndTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error)
	GetLogsByGameID(ctx context.Context, gameID uint) ([]Log, error)
	GetLogsByGameIDPaginated(ctx context.Context, gameID uint, page, pageSize int) ([]Log, int64, error)
	GetGameSettingByGameID(ctx context.Context, gameID uint) (*GameSetting, error)
	UpsertGameSetting(ctx context.Context, settings *GameSetting) error
	IsTeamCaptain(ctx context.Context, teamID, userID uint) (bool, error)
	IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error)
	// Типизированные счётчики (A-5, pass 32) — заменяют escape-hatch Model(ctx).
	CountActivePassings(ctx context.Context, gameID uint) (int64, error)
	CountLevelsByGame(ctx context.Context, gameID uint) (int64, error)
	CountPublished(ctx context.Context) (int64, error)
	// CountPassingsInStatuses — количество прохождений игры в указанных статусах
	// (A-H2, pass 33: заменил raw Count в svc_play.ProcessSnapshot).
	CountPassingsInStatuses(ctx context.Context, gameID uint, statuses []GamePassingStatus) (int64, error)
	// AdminListGames — админ-листинг с фильтрами (A-5, pass 32): инкапсулирует
	// динамическую GORM-цепочку, которую раньше строил handler через Model(ctx).
	AdminListGames(ctx context.Context, query, status string, offset, limit int) ([]Game, int64, error)
	// RawScan выполняет raw SQL и сканирует в dest (A-H4, pass 33 — убирает
	// Model(ctx) из интерфейса). Используется листингом для оконных запросов.
	RawScan(ctx context.Context, dest any, query string, args ...any) error
	// Autocomplete выполняет поиск опубликованных публичных игр (A-H4).
	Autocomplete(ctx context.Context, query string, limit int) ([]Game, error)
	// SearchVectorExists проверяет наличие search_vector (A-H4).
	SearchVectorExists(ctx context.Context) (bool, error)
}

// GamePassingRepository — контракт для прохождений.
type GamePassingRepository interface {
	Create(ctx context.Context, passing *GamePassing) error
	GetByID(ctx context.Context, id uint) (*GamePassing, error)
	FindByGameAndTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error)
	FindActiveByGame(ctx context.Context, gameID uint) ([]GamePassing, error)
	UpdateStatus(ctx context.Context, id uint, status GamePassingStatus) error
	// A-H3 (pass 33): read-пути GamePassingService — ранее шли через
	// экспортированный DB *gorm.DB в сервисе.
	ListByGamePaginated(ctx context.Context, gameID uint, page, pageSize int) ([]GamePassing, int64, error)
	ListTestPassings(ctx context.Context, gameID uint) ([]GamePassing, error)
	Save(ctx context.Context, passing *GamePassing) error
}

// ---------- GORM implementations ----------

type gormGameRepo struct{ db *gorm.DB }

func NewGormGameRepo(db *gorm.DB) GameRepository { return &gormGameRepo{db} }

func (r *gormGameRepo) Create(ctx context.Context, game *Game) error {
	return r.db.WithContext(ctx).Create(game).Error
}
func (r *gormGameRepo) GetByID(ctx context.Context, id uint) (*Game, error) {
	var g Game
	err := r.db.WithContext(ctx).First(&g, id).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}
func (r *gormGameRepo) GetByIDPreloaded(ctx context.Context, id uint) (*Game, error) {
	var g Game
	// P-5 (pass 33) + F1 (pass 34): явный LEFT JOIN для Author — GORM
	// Joins("Author") генерирует INNER JOIN, и игра с удалённым/отсутствующим
	// автором исчезала бы из результата (404). GameSetting через Preload
	// (has-one может отсутствовать — JOIN потерял бы строку).
	err := r.db.WithContext(ctx).
		Joins("LEFT JOIN users ON users.id = games.author_id").
		Preload("GameSetting").
		First(&g, id).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}
func (r *gormGameRepo) Update(ctx context.Context, game *Game) error {
	return r.db.WithContext(ctx).Save(game).Error
}
func (r *gormGameRepo) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Game{}, id).Error
}
func (r *gormGameRepo) Save(ctx context.Context, game *Game) error {
	return r.db.WithContext(ctx).Save(game).Error
}

// RawScan выполняет raw SQL и сканирует результат (A-H4, pass 33).
func (r *gormGameRepo) RawScan(ctx context.Context, dest any, query string, args ...any) error {
	return r.db.WithContext(ctx).Raw(query, args...).Scan(dest).Error
}

// Autocomplete ищет опубликованные публичные игры по поисковому вектору/ILIKE
// (A-H4, pass 33 — ранее raw-запрос через Model(ctx) в листинге).
func (r *gormGameRepo) Autocomplete(ctx context.Context, query string, limit int) ([]Game, error) {
	var items []Game
	err := r.db.WithContext(ctx).
		Select("id, name").
		Where("is_draft = false AND visibility = 'public' AND (search_vector @@ plainto_tsquery('russian', ?) OR name ILIKE ?)",
			query, "%"+sqlutil.EscapeLike(query)+"%").
		Limit(limit).
		Find(&items).Error
	return items, err
}

// SearchVectorExists проверяет наличие search_vector в таблице games
// (A-H4, pass 33).
func (r *gormGameRepo) SearchVectorExists(ctx context.Context) (bool, error) {
	var exists bool
	err := r.db.WithContext(ctx).
		Raw("SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='games' AND column_name='search_vector')").
		Scan(&exists).Error
	if err != nil {
		return false, err
	}
	return exists, nil
}

// ListByDateRange возвращает опубликованные публичные игры за указанный период.
func (r *gormGameRepo) ListByDateRange(ctx context.Context, from, to time.Time) ([]Game, error) {
	var games []Game
	err := r.db.WithContext(ctx).
		Preload("Author").
		Where("is_draft = false AND visibility = 'public' AND starts_at BETWEEN ? AND ?", from, to).
		Order("starts_at ASC").
		Find(&games).Error
	return games, err
}

func (r *gormGameRepo) GetPassingByUser(ctx context.Context, gameID, userID uint) (*GamePassing, error) {
	var passing GamePassing
	err := r.db.WithContext(ctx).
		Joins("JOIN team_members ON team_members.team_id = game_passings.team_id").
		Where("game_passings.game_id = ? AND game_passings.status IN (?,?) AND team_members.user_id = ?",
			gameID, StatusAccepted, StatusStarted, userID).
		Order("game_passings.id ASC").
		First(&passing).Error
	if err != nil {
		return nil, err
	}
	return &passing, nil
}

func (r *gormGameRepo) GetFinishedPassingByGameAndTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error) {
	var passing GamePassing
	err := r.db.WithContext(ctx).
		Where("game_id = ? AND team_id = ? AND status = ?", gameID, teamID, StatusFinished).
		First(&passing).Error
	if err != nil {
		return nil, err
	}
	return &passing, nil
}

func (r *gormGameRepo) GetLogsByGameID(ctx context.Context, gameID uint) ([]Log, error) {
	var logs []Log
	err := r.db.WithContext(ctx).
		Joins("JOIN game_passings ON game_passings.id = logs.game_passing_id").
		Where("game_passings.game_id = ?", gameID).
		Order("logs.created_at ASC").
		Find(&logs).Error
	return logs, err
}

// GetLogsByGameIDPaginated возвращает страницу логов игры (H6, pass 30 —
// перенесено из svc_facade: сырой SQL должен жить в репозитории).
// P-4 (pass 33): COUNT(*) OVER() вместо отдельного Count — один запрос.
func (r *gormGameRepo) GetLogsByGameIDPaginated(ctx context.Context, gameID uint, page, pageSize int) ([]Log, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	} else if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	type logRow struct {
		Log
		TotalCount int64
	}
	var rows []logRow
	err := r.db.WithContext(ctx).
		Select("logs.*, COUNT(*) OVER() AS total_count").
		Joins("JOIN game_passings ON game_passings.id = logs.game_passing_id").
		Where("game_passings.game_id = ?", gameID).
		Order("logs.created_at ASC").
		Limit(pageSize).Offset(offset).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	total := int64(0)
	logs := make([]Log, 0, len(rows))
	for i := range rows {
		if i == 0 {
			total = rows[i].TotalCount
		}
		logs = append(logs, rows[i].Log)
	}
	return logs, total, nil
}

func (r *gormGameRepo) GetGameSettingByGameID(ctx context.Context, gameID uint) (*GameSetting, error) {
	var settings GameSetting
	err := r.db.WithContext(ctx).Where("game_id = ?", gameID).First(&settings).Error
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpsertGameSetting сохраняет или обновляет настройки одним upsert-запросом
// (H6, pass 30). Единый OnConflict (B4): update-then-insert имел гонку.
func (r *gormGameRepo) UpsertGameSetting(ctx context.Context, settings *GameSetting) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "game_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"allow_hints":                 settings.AllowHints,
			"hint_penalty_seconds":        settings.HintPenaltySeconds,
			"max_hints":                   settings.MaxHints,
			"per_level_time_limit":        settings.PerLevelTimeLimit,
			"hide_answers_until_finished": settings.HideAnswersUntilFinished,
			"auto_start":                  settings.AutoStart,
		}),
	}).Create(settings).Error
}

func (r *gormGameRepo) IsTeamCaptain(ctx context.Context, teamID, userID uint) (bool, error) {
	var capt struct{ CaptainID uint }
	err := r.db.WithContext(ctx).Table("teams").Select("captain_id").Where("id = ?", teamID).First(&capt).Error
	if err != nil {
		return false, err
	}
	return capt.CaptainID == userID, nil
}

// IsTeamMember проверяет членство пользователя в команде (A-1, pass 31:
// использовалось напрямую в SSE-хендлере через raw-запрос).
func (r *gormGameRepo) IsTeamMember(ctx context.Context, teamID, userID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("team_members").
		Where("team_id = ? AND user_id = ?", teamID, userID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// CountActivePassings — количество активных прохождений игры
// (A-5, pass 32: типизированная замена Model(ctx).Model(&GamePassing{})).
func (r *gormGameRepo) CountActivePassings(ctx context.Context, gameID uint) (int64, error) {
	return r.CountPassingsInStatuses(ctx, gameID, []GamePassingStatus{StatusStarted, StatusTesting})
}

// CountPassingsInStatuses — количество прохождений игры в заданных статусах
// (A-H2, pass 33).
func (r *gormGameRepo) CountPassingsInStatuses(ctx context.Context, gameID uint, statuses []GamePassingStatus) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&GamePassing{}).
		Where("game_id = ? AND status IN ?", gameID, statuses).
		Count(&count).Error
	return count, err
}

// CountLevelsByGame — количество уровней игры (A-5, pass 32).
func (r *gormGameRepo) CountLevelsByGame(ctx context.Context, gameID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&level.Level{}).Where("game_id = ?", gameID).Count(&count).Error
	return count, err
}

// CountPublished — количество опубликованных игр (метрики, A-5 pass 32).
func (r *gormGameRepo) CountPublished(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Game{}).Where("is_draft = false").Count(&count).Error
	return count, err
}

// AdminListGames — админ-листинг с фильтрами (A-5, pass 32; P-3, pass 33:
// COUNT(*) OVER() + JOIN users — один запрос вместо Count+Find+Preload).
func (r *gormGameRepo) AdminListGames(ctx context.Context, query, status string, offset, limit int) ([]Game, int64, error) {
	var b strings.Builder
	b.WriteString(`
		SELECT games.*, users.name AS author__name, COUNT(*) OVER() AS total_count
		FROM games
		LEFT JOIN users ON users.id = games.author_id
		WHERE 1=1`)
	args := []any{}
	if query != "" {
		like := sqlutil.BuildLikePattern(query)
		b.WriteString(` AND (games.name ILIKE ? OR users.name ILIKE ?)`)
		args = append(args, like, like)
	}
	switch status {
	case "draft":
		b.WriteString(` AND games.is_draft = true`)
	case "published":
		b.WriteString(` AND games.is_draft = false`)
	}
	b.WriteString(` ORDER BY games.id DESC LIMIT ? OFFSET ?`)
	args = append(args, limit, offset)

	type gameRow struct {
		Game
		AuthorName string `gorm:"column:author__name"`
		TotalCount int64
	}
	var rows []gameRow
	if err := r.db.WithContext(ctx).Raw(b.String(), args...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	total := int64(0)
	games := make([]Game, 0, len(rows))
	for i := range rows {
		if i == 0 {
			total = rows[i].TotalCount
		}
		if rows[i].AuthorName != "" {
			rows[i].Author = user.User{Name: rows[i].AuthorName}
		}
		games = append(games, rows[i].Game)
	}
	return games, total, nil
}

type gormGamePassingRepo struct{ db *gorm.DB }

func NewGormGamePassingRepo(db *gorm.DB) GamePassingRepository { return &gormGamePassingRepo{db} }

func (r *gormGamePassingRepo) Create(ctx context.Context, passing *GamePassing) error {
	return r.db.WithContext(ctx).Create(passing).Error
}
func (r *gormGamePassingRepo) GetByID(ctx context.Context, id uint) (*GamePassing, error) {
	var p GamePassing
	err := r.db.WithContext(ctx).First(&p, id).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}
func (r *gormGamePassingRepo) FindByGameAndTeam(ctx context.Context, gameID, teamID uint) (*GamePassing, error) {
	var p GamePassing
	err := r.db.WithContext(ctx).Where("game_id = ? AND team_id = ?", gameID, teamID).First(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}
func (r *gormGamePassingRepo) FindActiveByGame(ctx context.Context, gameID uint) ([]GamePassing, error) {
	var passings []GamePassing
	err := r.db.WithContext(ctx).Where("game_id = ? AND status = ?", gameID, StatusStarted).Find(&passings).Error
	return passings, err
}
func (r *gormGamePassingRepo) UpdateStatus(ctx context.Context, id uint, status GamePassingStatus) error {
	return r.db.WithContext(ctx).Model(&GamePassing{}).Where("id = ?", id).Update("status", status).Error
}

// ListByGamePaginated возвращает страницу прохождений игры с JOIN команды и
// капитана (A-H3, pass 33 — перенесено из GamePassingService).
func (r *gormGamePassingRepo) ListByGamePaginated(ctx context.Context, gameID uint, page, pageSize int) ([]GamePassing, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	} else if pageSize > 100 {
		pageSize = 100
	}
	var total int64
	if err := r.db.WithContext(ctx).Model(&GamePassing{}).Where("game_id = ?", gameID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var passings []GamePassing
	if err := r.db.WithContext(ctx).
		Joins("Team").Joins("Team.Captain").
		Where("game_id = ?", gameID).
		Order("game_passings.created_at DESC").Offset(offset).Limit(pageSize).
		Find(&passings).Error; err != nil {
		return nil, 0, err
	}
	return passings, total, nil
}

// ListTestPassings возвращает тестовые прохождения игры (A-H3, pass 33).
func (r *gormGamePassingRepo) ListTestPassings(ctx context.Context, gameID uint) ([]GamePassing, error) {
	var passings []GamePassing
	err := r.db.WithContext(ctx).Where("game_id = ? AND status = ?", gameID, StatusTesting).Find(&passings).Error
	return passings, err
}

func (r *gormGamePassingRepo) Save(ctx context.Context, passing *GamePassing) error {
	return r.db.WithContext(ctx).Save(passing).Error
}
