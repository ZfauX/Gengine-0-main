package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestT_DefaultRussian(t *testing.T) {
	assert.Equal(t, "Неверный ID игры", T("generic.invalid_game_id"))
	assert.Equal(t, "Внутренняя ошибка сервера", T("generic.server_error"))
	assert.Equal(t, "Неверный email или пароль", T("auth.email_not_found"))
	assert.Equal(t, "Игра не найдена", T("game.not_found"))
}

func TestT_KeyNotFound(t *testing.T) {
	assert.Equal(t, "nonexistent_key", T("nonexistent_key"))
}

func TestTF(t *testing.T) {
	assert.Equal(t, "Уровень с позицией 5 уже существует в этой игре", TF("level.position_exists", 5))
	assert.Equal(t, "Аккаунт заблокирован до 12:00:00 (осталось 5m)", TF("auth.account_locked", "12:00:00", "5m"))
}

func TestTranslatorT_RU(t *testing.T) {
	tr := NewTranslator(ruMessages, enMessages)
	assert.Equal(t, "Игра не найдена", tr.T(LangRU, "game.not_found"))
	assert.Equal(t, "Game not found", tr.T(LangEN, "game.not_found"))
}

func TestTranslatorT_FallbackToRU(t *testing.T) {
	tr := NewTranslator(ruMessages, nil)
	assert.Equal(t, "Игра не найдена", tr.T(LangEN, "game.not_found"))
	assert.Equal(t, "nonexistent", tr.T(LangEN, "nonexistent"))
}

func TestTranslatorT_KeyNotFound(t *testing.T) {
	tr := NewTranslator(ruMessages, enMessages)
	assert.Equal(t, "unknown", tr.T(LangRU, "unknown"))
}

func TestTranslatorTF(t *testing.T) {
	tr := NewTranslator(ruMessages, enMessages)
	assert.Equal(t, "Уровень с позицией 3 уже существует в этой игре", tr.TF(LangRU, "level.position_exists", 3))
	assert.Equal(t, "A level with position 3 already exists in this game", tr.TF(LangEN, "level.position_exists", 3))
}

func TestMiddleware_SetsLang(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware(LangEN))
	r.GET("/test", func(c *gin.Context) {
		assert.Equal(t, LangEN, FromCtx(c))
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestFromCtx_Default(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		assert.Equal(t, LangRU, FromCtx(c))
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAllKeysHaveEN(t *testing.T) {
	for k := range ruMessages {
		_, ok := enMessages[k]
		assert.True(t, ok, "EN message missing for key: %s", k)
	}
}

func TestAllKeysHaveRU(t *testing.T) {
	for k := range enMessages {
		_, ok := ruMessages[k]
		assert.True(t, ok, "RU message missing for key: %s", k)
	}
}
