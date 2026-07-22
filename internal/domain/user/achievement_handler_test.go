// internal/domain/user/achievement_handler_test.go
package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAchievementHandler_New(t *testing.T) {
	handler := NewAchievementHandler(nil)
	assert.NotNil(t, handler)
}
