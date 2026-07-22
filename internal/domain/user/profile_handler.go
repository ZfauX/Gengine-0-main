// internal/domain/user/profile_handler.go
package user

import (
	"errors"
	"net/http"

	"gengine-0/internal/pkg/render"
	"gengine-0/internal/pkg/sanitize"
	"gengine-0/internal/pkg/storage"
	"gengine-0/internal/pkg/validation"

	csrf "gengine-0/internal/pkg/csrf"
	apperrors "gengine-0/internal/pkg/errors"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type ProfileHandler struct {
	db         *gorm.DB
	storage    storage.FileStorage
	authSvc    *AuthService
	profileSvc *ProfileService
	userSvc    *UserService
}

func NewProfileHandler(db *gorm.DB, st storage.FileStorage, authSvc *AuthService, profileSvc *ProfileService, userSvc *UserService) *ProfileHandler {
	return &ProfileHandler{
		db:         db,
		storage:    st,
		authSvc:    authSvc,
		profileSvc: profileSvc,
		userSvc:    userSvc,
	}
}

func (h *ProfileHandler) Show(c *gin.Context) {
	userID := c.GetUint("userID")
	var user User
	if err := h.db.Preload("Achievements").First(&user, userID).Error; err != nil {
		render.RenderErrorPage(c, http.StatusNotFound)
		return
	}
	render.Page(c, http.StatusOK, "profile-show.html", gin.H{
		"User":          user.ToPublic(),
		"Achievements":  user.Achievements,
		"CurrentUserID": userID,
		"csrf":          csrf.GetToken(c),
	})
}

func (h *ProfileHandler) PublicProfile(c *gin.Context) {
	var req UserIDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		render.RenderError(c, http.StatusBadRequest, "Неверный ID пользователя")
		return
	}

	userID := req.ID
	currentUserID := c.GetUint("userID")

	var user User
	if err := h.db.Preload("Achievements").First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			render.RenderErrorPage(c, http.StatusNotFound)
		} else {
			log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to get user")
			render.RenderErrorPage(c, http.StatusInternalServerError)
		}
		return
	}
	if user.ProfileVisibility == "hidden" {
		render.RenderError(c, http.StatusForbidden, "Профиль скрыт")
		return
	}

	stats, err := h.profileSvc.GetPublicProfileStats(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to get stats")
		stats = &UserStats{GamesCreated: 0, GamesPlayed: 0, Wins: 0, Rating: 0}
	}

	isFollowing := false
	if currentUserID != 0 && currentUserID != userID {
		isFollowing, err = h.profileSvc.IsFollowing(c.Request.Context(), currentUserID, userID)
		if err != nil {
			log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to check follow")
		}
	}

	recentGames, err := h.profileSvc.GetRecentGames(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("PublicProfile: failed to get recent games")
		recentGames = []RecentGame{}
	}

	pubUser := user.ToPublic()
	render.Page(c, http.StatusOK, "profile-public.html", gin.H{
		"ProfileUser":   &pubUser,
		"Achievements":  user.Achievements,
		"CurrentUserID": currentUserID,
		"IsOwner":       currentUserID == userID,
		"GamesCreated":  stats.GamesCreated,
		"GamesPlayed":   stats.GamesPlayed,
		"Wins":          stats.Wins,
		"Rating":        stats.Rating,
		"IsFollowing":   isFollowing,
		"RecentGames":   recentGames,
		"csrf":          csrf.GetToken(c),
	})
}

func (h *ProfileHandler) UploadAvatar(c *gin.Context) {
	userID := c.GetUint("userID")
	if userID == 0 {
		log.Warn().Msg("UploadAvatar: user not authenticated")
		c.Redirect(http.StatusFound, "/profile")
		return
	}

	file, header, err := c.Request.FormFile("avatar")
	if err != nil {
		log.Warn().Err(err).Uint("user", userID).Msg("UploadAvatar: no file provided")
		c.Redirect(http.StatusFound, "/profile")
		return
	}
	defer func() { _ = file.Close() }()

	log.Info().
		Uint("user_id", userID).
		Str("filename", header.Filename).
		Int64("size", header.Size).
		Str("content_type", header.Header.Get("Content-Type")).
		Msg("UploadAvatar: received file")

	if header.Size > avatarMaxSize {
		appErr := apperrors.BadRequest("Размер файла не должен превышать 2 МБ")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	allowedTypes := []string{"image/jpeg", "image/png", "image/webp"}

	webPath, err := h.storage.Save("uploads/avatars", file, header.Filename, userID, avatarMaxSize, allowedTypes)
	if err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: storage save failed")
		appErr := apperrors.Wrap(err, "UploadAvatar: storage save failed")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	log.Info().Uint("user_id", userID).Str("path", webPath).Msg("UploadAvatar: file saved")

	if err := h.userSvc.UpdateAvatarPath(c.Request.Context(), userID, webPath); err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UploadAvatar: failed to update avatar_path")
		if delErr := h.storage.Delete(webPath); delErr != nil {
			log.Error().Err(delErr).Str("path", webPath).Msg("UploadAvatar: failed to delete uploaded file")
		}
		appErr := apperrors.Wrap(err, "UploadAvatar: failed to update avatar_path")
		c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
			"error": appErr.Message,
			"code":  appErr.Code,
		})
		return
	}

	log.Info().Uint("user_id", userID).Str("path", webPath).Msg("UploadAvatar: avatar updated successfully")
	c.Redirect(http.StatusFound, "/profile")
}

func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	userID := c.GetUint("userID")

	var input UpdateProfileInput
	errs := validation.FieldErrors{}
	if err := c.ShouldBind(&input); err != nil {
		errs.Add("name", validation.ValidateString("Имя", input.Name, 1, 128))
		errs.Add("email", validation.ValidateString("Email", input.Email, 1, 255))
		if !errs.HasErrors() {
			errs.Add("form", errors.New("некорректные данные"))
		}
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Errors": errs,
			"Error":  errs.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}

	cleanName := sanitize.StripHTML(input.Name)
	cleanEmail := sanitize.StripHTML(input.Email)

	if err := h.profileSvc.UpdateProfile(c.Request.Context(), userID, cleanName, cleanEmail); err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("UpdateProfile: failed to update")
		errs.Add("email", err)
		if !errs.HasErrors() {
			errs.Add("form", err)
		}
		render.Page(c, http.StatusInternalServerError, "profile-show.html", gin.H{
			"Errors": errs,
			"Error":  errs.Error(),
			"csrf":   csrf.GetToken(c),
		})
		return
	}
	c.Redirect(http.StatusFound, "/profile")
}

func (h *ProfileHandler) ChangePassword(c *gin.Context) {
	userID := c.GetUint("userID")

	var input ChangePasswordInput
	if err := c.ShouldBind(&input); err != nil {
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Error": "Некорректные данные: " + err.Error(),
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := h.userSvc.ChangePassword(c.Request.Context(), userID, input.OldPassword, input.NewPassword); err != nil {
		log.Error().Err(err).Uint("user", userID).Msg("ChangePassword: failed to update")
		render.Page(c, http.StatusBadRequest, "profile-show.html", gin.H{
			"Error": "Неверный текущий пароль",
			"csrf":  csrf.GetToken(c),
		})
		return
	}

	if err := h.authSvc.RevokeAllUserTokens(c.Request.Context(), userID); err != nil {
		log.Error().Err(err).Uint("user_id", userID).Msg("ChangePassword: failed to revoke refresh tokens")
	}

	setSecureCookie(c, "refresh_token", "", -1, "/auth/refresh")

	c.Redirect(http.StatusFound, "/profile")
}
