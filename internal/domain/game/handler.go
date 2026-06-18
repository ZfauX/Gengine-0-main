// internal/domain/game/handler.go
package game

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/utrack/gin-csrf"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gengine-0/internal/domain/level"
	"gengine-0/internal/domain/team"
	"gengine-0/internal/pkg/storage"
	ws "gengine-0/internal/pkg/websocket"
)

// ---------- Входные структуры для валидации ----------

type ApplyInput struct {
	TeamID uint `form:"team_id" binding:"required,gt=0"`
}

type DisqualifyInput struct {
	TeamID uint `form:"team_id" binding:"required,gt=0"`
}

type AddCoAuthorInput struct {
	UserID uint `form:"user_id" binding:"required,gt=0"`
}

type SubmitCodeInput struct {
	Code string `form:"code" binding:"required"`
}

type SubmitTestCodeInput struct {
	Code string `form:"code" binding:"required"`
}

// ---------- Вспомогательные типы для FullPreview ----------
type levelPreview struct {
	ID          uint              `json:"id"`
	Position    int               `json:"position"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Questions   []questionPreview `json:"questions"`
}

type questionPreview struct {
	Text    string   `json:"text"`
	Hint    string   `json:"hint"`
	Answers []string `json:"answers"`
}

// ---------- Обработчики ----------

type GameHandler struct {
	gameService     *GameService
	passingService  *GamePassingService
	coAuthorService *CoAuthorService
	noteService     *NoteService
	simulateService *SimulateService
	photoService    *PhotoService         // сервис для фотографий
	storage         storage.FileStorage
	hub             *ws.RoomHub
}

func NewGameHandler(
	gameService *GameService,
	passingService *GamePassingService,
	coAuthorService *CoAuthorService,
	noteService *NoteService,
	simulateService *SimulateService,
	photoService *PhotoService,
	storage storage.FileStorage,
	hub *ws.RoomHub,
) *GameHandler {
	return &GameHandler{
		gameService:     gameService,
		passingService:  passingService,
		coAuthorService: coAuthorService,
		noteService:     noteService,
		simulateService: simulateService,
		photoService:    photoService,
		storage:         storage,
		hub:             hub,
	}
}

// List отображает список игр с фильтрацией и пагинацией.
func (h *GameHandler) List(c *gin.Context) {
	userID := c.GetUint("userID")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if page < 1 { page = 1 }
	if perPage < 1 || perPage > 100 { perPage = 20 }

	sortField := c.DefaultQuery("sort", "created_at")
	sortOrder := c.DefaultQuery("order", "desc")

	filter := GameFilter{
		Status:   c.Query("status"),
		Search:   c.Query("search"),
		DateFrom: c.Query("date_from"),
		DateTo:   c.Query("date_to"),
		ViewerID: userID,
	}
	if authorIDStr := c.Query("author_id"); authorIDStr != "" {
		if id, err := strconv.Atoi(authorIDStr); err == nil {
			uid := uint(id)
			filter.AuthorID = &uid
		}
	}

	sort := &GameSort{Field: sortField, Order: SortOrder(sortOrder)}

	games, total, err := h.gameService.ListFilteredPaginated(filter, sort, page, perPage)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	totalPages := (total + int64(perPage) - 1) / int64(perPage)
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "games-list.html",
		"Games":         games,
		"CurrentUserID": userID,
		"Filter":        filter,
		"Page":          page,
		"PerPage":       perPage,
		"TotalPages":    totalPages,
		"Total":         total,
	})
}

// Show отображает одну игру.
func (h *GameHandler) Show(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(uint(id), userID)
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	isManager, _ := h.coAuthorService.IsUserManager(uint(id), userID)
	reviews, _ := h.gameService.reviewService.ListByGame(uint(id))
	avgRating, reviewsCount, _ := h.gameService.reviewService.GetAverageRating(uint(id))

	canApply := !g.IsDraft && g.StartsAt != nil && g.StartsAt.After(time.Now())

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "games-show.html",
		"Game":          g,
		"CurrentUserID": userID,
		"IsManager":     isManager,
		"Reviews":       reviews,
		"AvgRating":     avgRating,
		"ReviewsCount":  reviewsCount,
		"CanApply":      canApply,
		"csrf":          csrf.GetToken(c),
	})
}

// NewForm отображает форму создания игры.
func (h *GameHandler) NewForm(c *gin.Context) {
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "games-new.html",
		"csrf":         csrf.GetToken(c),
	})
}

// Create создаёт новую игру.
func (h *GameHandler) Create(c *gin.Context) {
	userID := c.GetUint("userID")
	var g Game
	if err := c.ShouldBind(&g); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "games-new.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	file, header, err := c.Request.FormFile("cover")
	if err == nil {
		defer func() { _ = file.Close() }()
		if header.Size > 5*1024*1024 {
			c.HTML(http.StatusOK, "layout.html", gin.H{
				"ContentBlock": "games-new.html",
				"Error":        "Размер файла не должен превышать 5 МБ",
				"csrf":         csrf.GetToken(c),
			})
			return
		}

		allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
		contentType := header.Header.Get("Content-Type")
		allowed := false
		for _, t := range allowedTypes {
			if t == contentType { allowed = true; break }
		}
		if !allowed {
			c.HTML(http.StatusOK, "layout.html", gin.H{
				"ContentBlock": "games-new.html",
				"Error":        "Допустимы только JPEG, PNG и WebP",
				"csrf":         csrf.GetToken(c),
			})
			return
		}

		webPath, err := h.storage.Save("uploads/covers", file, header.Filename, userID, 5*1024*1024, allowedTypes)
		if err != nil {
			log.Error().Err(err).Str("filename", header.Filename).Msg("Create game: failed to save cover")
			c.HTML(http.StatusOK, "layout.html", gin.H{
				"ContentBlock": "games-new.html",
				"Error":        "Ошибка сохранения обложки",
				"csrf":         csrf.GetToken(c),
			})
			return
		}
		g.CoverPath = webPath
	}

	if err := h.gameService.Create(&g, userID); err != nil {
		if g.CoverPath != "" {
			if delErr := h.storage.Delete(g.CoverPath); delErr != nil {
				log.Error().Err(delErr).Str("path", g.CoverPath).Msg("Create game: failed to delete orphaned cover")
			}
		}
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "games-new.html",
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games")
}

// EditForm отображает форму редактирования игры.
func (h *GameHandler) EditForm(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(uint(id), userID)
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	isManager, _ := h.coAuthorService.IsUserManager(uint(id), userID)
	if !isManager {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "games-edit.html",
		"Game":         g,
		"csrf":         csrf.GetToken(c),
	})
}

// Update обновляет игру.
func (h *GameHandler) Update(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var updated Game
	if err := c.ShouldBind(&updated); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "games-edit.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	updated.ID = uint(id)

	oldGame, err := h.gameService.GetByID(uint(id), userID)
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	if deleteCover := c.PostForm("delete_cover"); deleteCover == "1" {
		if oldGame.CoverPath != "" {
			if err := h.storage.Delete(oldGame.CoverPath); err != nil {
				log.Error().Err(err).Str("path", oldGame.CoverPath).Msg("Update game: failed to delete old cover")
			}
		}
		updated.CoverPath = ""
	} else {
		file, header, err := c.Request.FormFile("cover")
		if err == nil {
			defer func() { _ = file.Close() }()
			if header.Size > 5*1024*1024 {
				c.HTML(http.StatusOK, "layout.html", gin.H{
					"ContentBlock": "games-edit.html",
					"Error":        "Размер файла не должен превышать 5 МБ",
					"csrf":         csrf.GetToken(c),
				})
				return
			}

			allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
			contentType := header.Header.Get("Content-Type")
			allowed := false
			for _, t := range allowedTypes {
				if t == contentType { allowed = true; break }
			}
			if !allowed {
				c.HTML(http.StatusOK, "layout.html", gin.H{
					"ContentBlock": "games-edit.html",
					"Error":        "Допустимы только JPEG, PNG и WebP",
					"csrf":         csrf.GetToken(c),
				})
				return
			}

			webPath, err := h.storage.Save("uploads/covers", file, header.Filename, userID, 5*1024*1024, allowedTypes)
			if err != nil {
				log.Error().Err(err).Str("filename", header.Filename).Msg("Update game: failed to save new cover")
				c.HTML(http.StatusOK, "layout.html", gin.H{
					"ContentBlock": "games-edit.html",
					"Error":        "Ошибка сохранения обложки",
					"csrf":         csrf.GetToken(c),
				})
				return
			}
			if oldGame.CoverPath != "" {
				if err := h.storage.Delete(oldGame.CoverPath); err != nil {
					log.Error().Err(err).Str("path", oldGame.CoverPath).Msg("Update game: failed to delete old cover after upload")
				}
			}
			updated.CoverPath = webPath
		} else {
			updated.CoverPath = oldGame.CoverPath
		}
	}

	if err := h.gameService.Update(uint(id), &updated, userID); err != nil {
		c.HTML(http.StatusForbidden, "games/edit.html", gin.H{"Error": err.Error(), "Game": &updated, "csrf": csrf.GetToken(c)})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}

// Delete удаляет игру (только владелец).
func (h *GameHandler) Delete(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	if err := h.gameService.Delete(uint(id), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games")
}

// Publish публикует черновик игры.
func (h *GameHandler) Publish(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	if err := h.gameService.Publish(uint(id), userID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}

// ----- Прохождения и заявки -----

// ListPassings отображает все заявки и прохождения игры.
func (h *GameHandler) ListPassings(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	passings, err := h.passingService.ListByGame(uint(gameID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "game_passings-list.html",
		"GameID":       gameID,
		"Passings":     passings,
		"UserID":       userID,
		"csrf":         csrf.GetToken(c),
	})
}

// ApplyForm отображает форму подачи заявки.
func (h *GameHandler) ApplyForm(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	teams, _ := h.passingService.teamService.GetTeamsByCaptain(userID)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "game_passings-apply.html",
		"GameID":       gameID,
		"Teams":        teams,
		"csrf":         csrf.GetToken(c),
	})
}

// Apply подаёт заявку на игру.
func (h *GameHandler) Apply(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var input ApplyInput
	if err := c.ShouldBind(&input); err != nil {
		teams, _ := h.passingService.teamService.GetTeamsByCaptain(userID)
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "game_passings-apply.html",
			"GameID":       gameID,
			"Teams":        teams,
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if err := h.passingService.Apply(uint(gameID), input.TeamID, userID); err != nil {
		teams, _ := h.passingService.teamService.GetTeamsByCaptain(userID)
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "game_passings-apply.html",
			"GameID":       gameID,
			"Teams":        teams,
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id"))
}

// UpdatePassingStatus изменяет статус заявки (принять/отклонить).
func (h *GameHandler) UpdatePassingStatus(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("passing_id"))
	status := GamePassingStatus(c.PostForm("status"))

	if err := h.passingService.UpdateStatus(uint(passingID), status, c.GetUint("userID")); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/passings")
}

// StartGame запускает игру для конкретного прохождения.
func (h *GameHandler) StartGame(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("passing_id"))

	if err := h.passingService.StartGame(uint(passingID), c.GetUint("userID")); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/monitor")
}

// ForceFinish принудительно завершает игру.
func (h *GameHandler) ForceFinish(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))

	if err := h.gameService.ForceFinishGame(uint(gameID)); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/results")
}

// DisqualifyTeam дисквалифицирует команду.
func (h *GameHandler) DisqualifyTeam(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))

	var input DisqualifyInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": "Неверные данные: " + err.Error()})
		return
	}

	if err := h.gameService.DisqualifyTeam(uint(gameID), input.TeamID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/monitor")
}

// ----- Соавторы -----

// ManageCoAuthors отображает страницу управления соавторами.
func (h *GameHandler) ManageCoAuthors(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	coAuthors, err := h.coAuthorService.List(uint(gameID))
	if err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", nil)
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "co_authors-manage.html",
		"GameID":       gameID,
		"CoAuthors":    coAuthors,
		"csrf":         csrf.GetToken(c),
	})
}

// AddCoAuthor добавляет соавтора.
func (h *GameHandler) AddCoAuthor(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	ownerID := c.GetUint("userID")

	var input AddCoAuthorInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": "Неверные данные: " + err.Error()})
		return
	}

	if err := h.coAuthorService.Add(uint(gameID), input.UserID, ownerID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/co-authors")
}

// RemoveCoAuthor удаляет соавтора.
func (h *GameHandler) RemoveCoAuthor(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID, _ := strconv.Atoi(c.Param("user_id"))
	ownerID := c.GetUint("userID")

	if err := h.coAuthorService.Remove(uint(gameID), uint(userID), ownerID); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/co-authors")
}

// ----- Заметки автора -----

// Notes отображает заметки к игре (JSON API).
func (h *GameHandler) Notes(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")
	notes, err := h.noteService.ListByGame(uint(gameID), userID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"notes": notes})
}

// CreateNote создаёт новую заметку.
func (h *GameHandler) CreateNote(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")
	var input struct {
		LevelID *uint  `json:"level_id"`
		Text    string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	note, err := h.noteService.Create(uint(gameID), input.LevelID, userID, input.Text)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"note": note})
}

// DeleteNote удаляет заметку.
func (h *GameHandler) DeleteNote(c *gin.Context) {
	noteID, _ := strconv.Atoi(c.Param("note_id"))
	userID := c.GetUint("userID")
	if err := h.noteService.Delete(uint(noteID), userID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ----- Симуляция -----

// Simulate запускает симуляцию прохождения игры.
func (h *GameHandler) Simulate(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")
	result, err := h.simulateService.Simulate(uint(gameID), userID)
	if err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "simulate-results.html",
		"GameID":       gameID,
		"Result":       result,
	})
}

// ---------- Новые страницы ----------

// SettingsPage отображает страницу настроек игры.
func (h *GameHandler) SettingsPage(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(uint(gameID), userID)
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	var settings GameSetting
	if err := h.gameService.DB.Where("game_id = ?", g.ID).First(&settings).Error; err != nil {
		settings = GameSetting{
			GameID:                  g.ID,
			AllowHints:              true,
			HintPenaltySeconds:      300,
			MaxHints:                3,
			PerLevelTimeLimit:       0,
			HideAnswersUntilFinished: false,
			AutoStart:               false,
		}
	}

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "games-settings.html",
		"Game":         g,
		"Settings":     settings,
		"csrf":         csrf.GetToken(c),
	})
}

// SaveSettings сохраняет настройки игры.
func (h *GameHandler) SaveSettings(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var settings GameSetting
	if err := c.ShouldBind(&settings); err != nil {
		g, _ := h.gameService.GetByID(uint(gameID), userID)
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "games-settings.html",
			"Game":         g,
			"Settings":     settings,
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	g, err := h.gameService.GetByID(uint(gameID), userID)
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}
	isManager, _ := h.coAuthorService.IsUserManager(g.ID, userID)
	if !isManager {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	settings.GameID = g.ID
	if err := h.gameService.DB.Save(&settings).Error; err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "games-settings.html",
			"Game":         g,
			"Settings":     settings,
			"Error":        "Ошибка сохранения: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	c.Redirect(http.StatusFound, "/games/"+c.Param("id")+"/settings")
}

// TestPage отображает страницу управления тестовыми прохождениями.
func (h *GameHandler) TestPage(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	g, err := h.gameService.GetByID(uint(gameID), userID)
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", nil)
		return
	}

	var testPassings []GamePassing
	h.gameService.DB.Where("game_id = ? AND status = ?", g.ID, StatusTesting).Find(&testPassings)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "games-test.html",
		"Game":         g,
		"TestPassings": testPassings,
		"csrf":         csrf.GetToken(c),
	})
}

// PhotosPage отображает страницу фотогалереи.
func (h *GameHandler) PhotosPage(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	var photos []Photo
	if h.photoService != nil {
		photos, _ = h.photoService.List(uint(gameID))
	}
	isManager, _ := h.coAuthorService.IsUserManager(uint(gameID), userID)

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":  "games-photos.html",
		"GameID":        gameID,
		"Photos":        photos,
		"CurrentUserID": userID,
		"IsManager":     isManager,
		"csrf":          csrf.GetToken(c),
	})
}

// UploadPhoto загружает новое фото в галерею игры.
func (h *GameHandler) UploadPhoto(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	file, header, err := c.Request.FormFile("photo")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Файл не выбран"})
		return
	}
	defer func() { _ = file.Close() }()   // errcheck исправлен

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
	webPath, err := h.storage.Save("uploads/photos", file, header.Filename, userID, 10*1024*1024, allowedTypes)
	if err != nil {
		log.Error().Err(err).Str("filename", header.Filename).Msg("UploadPhoto: failed to save file")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	photo := &Photo{
		GameID: uint(gameID),
		UserID: userID,
		Path:   webPath,
	}
	if err := h.photoService.Create(photo); err != nil {
		log.Error().Err(err).Msg("UploadPhoto: failed to create photo record")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Не удалось сохранить фото"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "photo": photo})
}

// DeletePhoto удаляет фото из галереи.
func (h *GameHandler) DeletePhoto(c *gin.Context) {
	photoID, _ := strconv.Atoi(c.Param("photo_id"))
	userID := c.GetUint("userID")

	var photo Photo
	if err := h.gameService.DB.First(&photo, photoID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Фото не найдено"})
		return
	}

	isOwner := photo.UserID == userID
	isManager, _ := h.coAuthorService.IsUserManager(photo.GameID, userID)

	if !isOwner && !isManager {
		c.JSON(http.StatusForbidden, gin.H{"error": "Нет прав на удаление"})
		return
	}

	// Удаляем файл
	if err := h.storage.Delete(photo.Path); err != nil {
		log.Error().Err(err).Str("path", photo.Path).Msg("DeletePhoto: failed to delete file")
	}

	// Удаляем запись (два аргумента: photoID и userID)
	if err := h.photoService.Delete(photo.ID, userID); err != nil {
		log.Error().Err(err).Uint("photo_id", photo.ID).Msg("DeletePhoto: failed to delete record")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Не удалось удалить фото"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// FullPreview возвращает структуру игры для быстрого просмотра.
func (h *GameHandler) FullPreview(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	_, err := h.gameService.GetByID(uint(gameID), userID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Нет доступа"})
		return
	}

	var levels []level.Level
	h.gameService.DB.Preload("Questions.Answers").Where("game_id = ?", gameID).Order("position ASC").Find(&levels)

	var result []levelPreview
	for _, lvl := range levels {
		lp := levelPreview{
			ID:          lvl.ID,
			Position:    lvl.Position,
			Name:        lvl.Name,
			Description: lvl.Description,
		}
		for _, q := range lvl.Questions {
			qp := questionPreview{Text: q.Text, Hint: q.Hint}
			for _, a := range q.Answers {
				qp.Answers = append(qp.Answers, a.Code)
			}
			lp.Questions = append(lp.Questions, qp)
		}
		result = append(result, lp)
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}

// ---------- Игровой процесс (бывший GameplayHandler) ----------

type GameplayHandler struct {
	gameService    *GameService
	attemptService *AttemptService
	progressSvc    *LevelProgressService
	monitorService *MonitorService
	hub            *ws.RoomHub
	storage        storage.FileStorage
	db             *gorm.DB
}

func NewGameplayHandler(
	gameService *GameService,
	attemptSvc *AttemptService,
	progressSvc *LevelProgressService,
	monitorSvc *MonitorService,
	hub *ws.RoomHub,
	store storage.FileStorage,
	db *gorm.DB,
) *GameplayHandler {
	return &GameplayHandler{
		gameService:    gameService,
		attemptService: attemptSvc,
		progressSvc:    progressSvc,
		monitorService: monitorSvc,
		hub:            hub,
		storage:        store,
		db:             db,
	}
}

// ShowGame отображает страницу прохождения уровня для команды.
func (h *GameplayHandler) ShowGame(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("passing_id"))
	userID := c.GetUint("userID")

	progress, err := GetCurrentProgress(h.db, uint(passingID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", gin.H{"Error": "Нет активного уровня"})
		return
	}

	var passing GamePassing
	if err := h.db.Preload("Team").First(&passing, passingID).Error; err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}
	if !h.isTeamMember(passing.TeamID, userID) {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": "Вы не являетесь участником этой команды"})
		return
	}

	var settings GameSetting
	timeLimitSec := 0
	if err := h.db.Where("game_id = ?", passing.GameID).First(&settings).Error; err == nil {
		if settings.PerLevelTimeLimit > 0 {
			elapsed := time.Since(progress.StartedAt)
			limit := time.Duration(settings.PerLevelTimeLimit) * time.Minute
			remaining := limit - elapsed
			if remaining < 0 { remaining = 0 }
			timeLimitSec = int(remaining.Seconds())
		}
	}

	var attempts []Attempt
	h.db.Where("level_progress_id = ?", progress.ID).Order("created_at DESC").Find(&attempts)

	hideAnswers := settings.HideAnswersUntilFinished && passing.Status != StatusFinished

	votingActive := h.db.Where("game_passing_id = ? AND level_id = ? AND is_open = true", passingID, progress.LevelID).First(&gameBlackboxVotingSession{}).Error == nil

	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock":    "gameplay-show.html",
		"PassingID":       passingID,
		"Level":           progress.Level,
		"Attempts":        attempts,
		"TimeLimitSeconds": timeLimitSec,
		"HideAnswers":     hideAnswers,
		"VotingActive":    votingActive,
		"TeamID":          passing.TeamID,
		"csrf":            csrf.GetToken(c),
	})
}

// SubmitCode обрабатывает ввод текстового кода.
func (h *GameplayHandler) SubmitCode(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("passing_id"))
	userID := c.GetUint("userID")

	if !h.isUserInPassing(uint(passingID), userID) {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	var input SubmitCodeInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "gameplay-show.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	attempt, err := h.gameService.SubmitCode(uint(passingID), userID, input.Code)
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "gameplay-show.html",
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if attempt.Success {
		c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id"))
	} else {
		c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id")+"?error=wrong_code")
	}
}

// UseHint использует подсказку для текущего уровня.
func (h *GameplayHandler) UseHint(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("passing_id"))
	if err := h.gameService.UseHint(uint(passingID), c.GetUint("userID")); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "gameplay-show.html",
			"Error":        err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id"))
}

// SubmitFile обрабатывает файловый ответ.
func (h *GameplayHandler) SubmitFile(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("passing_id"))
	userID := c.GetUint("userID")

	if !h.isUserInPassing(uint(passingID), userID) {
		c.HTML(http.StatusForbidden, "errors/403.html", nil)
		return
	}

	file, header, err := c.Request.FormFile("answer_file")
	if err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "gameplay-show.html",
			"Error":        "Файл не выбран",
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > 10*1024*1024 {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "gameplay-show.html",
			"Error":        "Размер файла не должен превышать 10 МБ",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/gif", "application/pdf", "text/plain"}
	webPath, err := h.storage.Save("uploads/answers", file, header.Filename, userID, 10*1024*1024, allowedTypes)
	if err != nil {
		log.Error().Err(err).Str("filename", header.Filename).Msg("SubmitFile: failed to save file")
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "gameplay-show.html",
			"Error":        "Ошибка сохранения файла",
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	_, err = h.gameService.SubmitFile(uint(passingID), userID, webPath)
	if err != nil {
		log.Error().Err(err).Uint("passing", uint(passingID)).Msg("SubmitFile: service error")
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "gameplay-show.html",
			"Error":        "Не удалось сохранить попытку",
			"csrf":         csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/game/"+c.Param("passing_id"))
}

// ---------- Тестовое прохождение ----------

// StartTesting инициирует тестовое прохождение.
func (h *GameplayHandler) StartTesting(c *gin.Context) {
	gameID, _ := strconv.Atoi(c.Param("id"))
	userID := c.GetUint("userID")

	passing, err := h.gameService.StartTesting(uint(gameID), userID)
	if err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+strconv.Itoa(int(passing.ID)))
}

// ShowTestGame отображает страницу тестового прохождения.
func (h *GameplayHandler) ShowTestGame(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("passing_id"))
	progress, err := GetCurrentProgress(h.db, uint(passingID))
	if err != nil {
		c.HTML(http.StatusNotFound, "errors/404.html", gin.H{"Error": "Уровень не найден"})
		return
	}
	c.HTML(http.StatusOK, "layout.html", gin.H{
		"ContentBlock": "gameplay-test.html",
		"PassingID":    passingID,
		"Level":        progress.Level,
		"csrf":         csrf.GetToken(c),
	})
}

// SubmitTestCode обрабатывает ввод кода в тестовом режиме.
func (h *GameplayHandler) SubmitTestCode(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("passing_id"))

	var input SubmitTestCodeInput
	if err := c.ShouldBind(&input); err != nil {
		c.HTML(http.StatusOK, "layout.html", gin.H{
			"ContentBlock": "gameplay-test.html",
			"Error":        "Неверные данные: " + err.Error(),
			"csrf":         csrf.GetToken(c),
		})
		return
	}

	if _, err := h.gameService.SubmitTestCode(uint(passingID), c.GetUint("userID"), input.Code); err != nil {
		c.HTML(http.StatusInternalServerError, "errors/500.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+c.Param("passing_id"))
}

// SkipTestLevel пропускает уровень в тестовом режиме.
func (h *GameplayHandler) SkipTestLevel(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("passing_id"))
	if err := h.gameService.SkipLevelTest(uint(passingID), c.GetUint("userID")); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/testing/"+c.Param("passing_id"))
}

// ---------- Ручное подтверждение автором ----------

// AcceptAnswer принимает ответ.
func (h *GameplayHandler) AcceptAnswer(c *gin.Context) {
	passingID, _ := strconv.Atoi(c.Param("passing_id"))
	if err := h.gameService.AcceptBlackboxAnswer(uint(passingID), c.GetUint("userID")); err != nil {
		c.HTML(http.StatusForbidden, "errors/403.html", gin.H{"Error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, "/games/"+c.Query("game_id")+"/monitor")
}

// ---------- Вспомогательные методы ----------

func (h *GameplayHandler) isTeamMember(teamID uint, userID uint) bool {
	var t team.Team
	if err := h.db.First(&t, teamID).Error; err != nil {
		return false
	}
	if t.CaptainID == userID {
		return true
	}
	var count int64
	h.db.Table("team_members").Where("team_id = ? AND user_id = ?", teamID, userID).Count(&count)
	return count > 0
}

func (h *GameplayHandler) isUserInPassing(passingID uint, userID uint) bool {
	var passing GamePassing
	if err := h.db.First(&passing, passingID).Error; err != nil {
		return false
	}
	return h.isTeamMember(passing.TeamID, userID)
}