// internal/domain/game/photo_handler.go
package game

import (
	"errors"
	"net/http"
	"slices"
	"strconv"

	apperr "gengine-0/internal/pkg/errors"
	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	csrf "github.com/utrack/gin-csrf"
	"gorm.io/gorm"
)

// PhotoHandler обрабатывает фотогалерею игр.
type PhotoHandler struct {
	gameService  GameServiceInterface
	coAuthorSvc  CoAuthorServiceInterface
	photoService *PhotoService
	storage      storage.FileStorage
}

// NewPhotoHandler создаёт новый PhotoHandler.
func NewPhotoHandler(
	gameService GameServiceInterface,
	coAuthorSvc CoAuthorServiceInterface,
	photoService *PhotoService,
	storage storage.FileStorage,
) *PhotoHandler {
	return &PhotoHandler{
		gameService:  gameService,
		coAuthorSvc:  coAuthorSvc,
		photoService: photoService,
		storage:      storage,
	}
}

// PhotosPage отображает страницу фотогалереи.
func (h *PhotoHandler) PhotosPage(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID игры")
		return
	}
	userID := c.GetUint("userID")

	var photos []Photo
	if h.photoService != nil {
		photos, err = h.photoService.List(uint(gameID))
		if err != nil {
			log.Error().Err(err).Int("game_id", gameID).Msg("GameHandler.PhotosPage: failed to list photos")
		}
	}
	isManager, err := h.coAuthorSvc.IsUserManager(c.Request.Context(), uint(gameID), userID)
	if err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("PhotoHandler.PhotosPage: failed to check manager")
		isManager = false
	}

	render.Page(c, http.StatusOK, "games-photos.html", gin.H{
		"GameID":        gameID,
		"Photos":        photos,
		"CurrentUserID": userID,
		"IsManager":     isManager,
		"csrf":          csrf.GetToken(c),
	})
}

// UploadPhoto загружает новое фото в галерею игры.
func (h *PhotoHandler) UploadPhoto(c *gin.Context) {
	gameID, err := strconv.Atoi(c.Param("id"))
	if err != nil || gameID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID игры",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")

	if err := limitRequestBody(c, 10*1024*1024); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
			"code":  "bad_request",
		})
		return
	}

	file, header, err := c.Request.FormFile("photo")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Файл не выбран",
			"code":  "bad_request",
		})
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > 10*1024*1024 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Размер файла не должен превышать 10 МБ",
			"code":  "bad_request",
		})
		return
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}
	contentType := header.Header.Get("Content-Type")
	if !slices.Contains(allowedTypes, contentType) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Допустимы только JPEG, PNG и WebP",
			"code":  "bad_request",
		})
		return
	}

	webPath, err := h.storage.Save("uploads/photos", file, header.Filename, userID, 10*1024*1024, allowedTypes)
	if err != nil {
		log.Error().Err(err).Str("filename", header.Filename).Msg("UploadPhoto: failed to save file")
		appErr := apperr.Wrap(err, "PhotoHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	photo := &Photo{
		GameID: uint(gameID),
		UserID: userID,
		Path:   webPath,
	}
	if err := h.photoService.Create(photo); err != nil {
		log.Error().Err(err).Int("game_id", gameID).Msg("UploadPhoto: failed to create photo record")
		if delErr := h.storage.Delete(webPath); delErr != nil {
			log.Error().Err(delErr).Str("path", webPath).Msg("UploadPhoto: failed to delete uploaded file")
		}
		appErr := apperr.Wrap(err, "PhotoHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "photo": photo})
}

// DeletePhoto удаляет фото из галереи.
func (h *PhotoHandler) DeletePhoto(c *gin.Context) {
	photoID, err := strconv.Atoi(c.Param("photo_id"))
	if err != nil || photoID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID фото",
			"code":  "bad_request",
		})
		return
	}
	userID := c.GetUint("userID")

	photo, err := h.photoService.GetByID(uint(photoID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
				"error": "Фото не найдено",
				"code":  "not_found",
			})
		} else {
			log.Error().Err(err).Int("photo_id", photoID).Msg("DeletePhoto: failed to get photo")
			appErr := apperr.Wrap(err, "PhotoHandler")
			c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
				"error": appErr.Message,
				"code":  appErr.Code,
			})
		}
		return
	}

	isOwner := photo.UserID == userID
	isManager, err := h.coAuthorSvc.IsUserManager(c.Request.Context(), photo.GameID, userID)
	if err != nil {
		log.Error().Err(err).Int("photo_id", photoID).Msg("DeletePhoto: failed to check manager")
		appErr := apperr.Wrap(err, "PhotoHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if !isOwner && !isManager {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "Нет прав на удаление",
			"code":  "forbidden",
		})
		return
	}

	if err := h.photoService.Delete(photo.ID, userID); err != nil {
		log.Error().Err(err).Uint("photo_id", photo.ID).Msg("DeletePhoto: failed to delete record")
		appErr := apperr.Wrap(err, "PhotoHandler")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	if err := h.storage.Delete(photo.Path); err != nil {
		log.Error().Err(err).Str("path", photo.Path).Msg("DeletePhoto: failed to delete file")
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
