package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

func RequirePermission(authorizer GameAuthorizer, requiredRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		gameID, err := strconv.Atoi(c.Param("game_id"))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": ErrInvalidGameID.Error()})
			return
		}
		userID := c.GetUint("userID")
		if userID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": ErrAuthRequired.Error()})
			return
		}
		ok, err := authorizer.IsUserManager(c.Request.Context(), uint(gameID), userID)
		if err != nil {
			log.Error().Err(err).Uint("game_id", uint(gameID)).Uint("user_id", userID).Msg("RequirePermission: error checking permissions")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": ErrInternalServer.Error()})
			return
		}
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": ErrInsufficientRights.Error()})
			return
		}
		c.Next()
	}
}
