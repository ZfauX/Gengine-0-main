package middleware

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// TeamCaptainOrGameAuthor проверяет, что пользователь является капитаном команды или автором игры.
func TeamCaptainOrGameAuthor(checker TeamAccessChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		teamID, err := strconv.Atoi(c.Param("team_id"))
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		userID := c.GetUint("userID")
		if !checker.CanManageTeam(uint(teamID), userID) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}