// internal/pkg/middleware/error_handler.go
package middleware

import (
	"net/http"

	"gengine-0/internal/pkg/errors"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// ErrorHandler обрабатывает паники и возвращает JSON-ошибку.
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error().Interface("panic", r).Msg("Panic recovered")
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": ErrInternalServer,
					"code":  "internal_error",
				})
			}
		}()

		c.Next()

		// Проверяем, есть ли ошибка в контексте
		if len(c.Errors) > 0 {
			err := c.Errors.Last().Err
			if appErr, ok := err.(*errors.AppError); ok {
				c.AbortWithStatusJSON(appErr.HTTPStatus, gin.H{
					"error":   appErr.Message,
					"code":    appErr.Code,
					"details": appErr.Details,
				})
				return
			}
			log.Error().Err(err).Msg("Unhandled error")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": ErrInternalServer,
				"code":  "internal_error",
			})
		}
	}
}
