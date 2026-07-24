package i18n

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

type Lang string

const (
	LangRU Lang = "ru"
	LangEN Lang = "en"
)

type Translator struct {
	ru map[string]string
	en map[string]string
}

func NewTranslator(ru, en map[string]string) *Translator {
	return &Translator{ru: ru, en: en}
}

var Default *Translator

func T(key string) string {
	if Default == nil {
		return key
	}
	return Default.T(LangRU, key)
}

func TF(key string, args ...any) string {
	if Default == nil {
		return key
	}
	return Default.TF(LangRU, key, args...)
}

func (t *Translator) T(lang Lang, key string) string {
	switch lang {
	case LangEN:
		if v, ok := t.en[key]; ok {
			return v
		}
		fallthrough
	default:
		if v, ok := t.ru[key]; ok {
			return v
		}
		return key
	}
}

func (t *Translator) TF(lang Lang, key string, args ...any) string {
	return fmt.Sprintf(t.T(lang, key), args...)
}

func Middleware(lang Lang) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("lang", string(lang))
		c.Next()
	}
}

func FromCtx(c *gin.Context) Lang {
	v, exists := c.Get("lang")
	if !exists {
		return LangRU
	}
	s, ok := v.(string)
	if !ok {
		return LangRU
	}
	return Lang(s)
}
