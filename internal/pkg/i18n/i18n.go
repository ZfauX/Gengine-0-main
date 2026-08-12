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
	// P-L1 (PASS-8): fast-path без fmt.Sprintf — TF вызывается в шаблонах
	// тысячи раз на страницу; Sprintf с 0-1 аргументом — дорогой рефлексивный
	// механизм вместо конкатенации.
	if len(args) == 0 {
		return t.T(lang, key)
	}
	return fmt.Sprintf(t.T(lang, key), args...)
}

func Middleware(defaultLang Lang) gin.HandlerFunc {
	return func(c *gin.Context) {
		lang := defaultLang
		if cookie, err := c.Cookie("lang"); err == nil {
			switch cookie {
			case "ru":
				lang = LangRU
			case "en":
				lang = LangEN
			}
		}
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
