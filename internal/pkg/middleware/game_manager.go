package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// GameManager проверяет, что пользователь является автором или соавтором игры (любая роль).
func GameManager(authorizer GameAuthorizer) gin.HandlerFunc {
	return func(c *gin.Context) {
		gameID, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		userID := c.GetUint("userID")
		if userID == 0 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		ok, err := authorizer.IsUserManager(c.Request.Context(), uint(gameID), userID)
		if err != nil || !ok {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Set("isGameManager", true)
		c.Next()
	}
}
