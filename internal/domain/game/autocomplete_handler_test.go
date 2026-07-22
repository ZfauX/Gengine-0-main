package game

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAutocompleteHandler_Games_EmptyQuery(t *testing.T) {
	h := &AutocompleteHandler{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/search/games?q=", nil)

	h.Games(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAutocompleteHandler_Games_ShortQuery(t *testing.T) {
	h := &AutocompleteHandler{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/search/games?q=a", nil)

	h.Games(c)

	assert.Equal(t, http.StatusOK, w.Code)
}
