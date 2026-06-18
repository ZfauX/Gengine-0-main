package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func RequirePermission(authorizer GameAuthorizer, requiredRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		gameID, err := strconv.Atoi(c.Param("game_id"))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "неверный game_id"})
			return
		}
		userID := c.GetUint("userID")
		if userID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "требуется аутентификация"})
			return
		}
		ok, _ := authorizer.IsUserManager(uint(gameID), userID)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "недостаточно прав"})
			return
		}
		c.Next()
	}
}