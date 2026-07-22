// internal/domain/user/dashboard_handler_test.go
package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDashboardHandler_New(t *testing.T) {
	handler := NewDashboardHandler(nil, nil)
	assert.NotNil(t, handler)
}
