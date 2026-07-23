// Package feature предоставляет механизм переключения функциональности (feature flags)
// на основе переменных окружения. Позволяет включать/отключать экспериментальные
// возможности приложения без перекомпиляции.
package feature

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// FeatureFlag представляет собой имя функционального флага.
type FeatureFlag string

// Предопределённые функциональные флаги.
const (
	FeatureNewSSE          FeatureFlag = "NEW_SSE"          // новая реализация Server-Sent Events
	FeatureNewUI           FeatureFlag = "NEW_UI"           // обновлённый интерфейс пользователя
	FeatureExperimentalAPI FeatureFlag = "EXPERIMENTAL_API" // экспериментальные API-эндпоинты
	FeatureStrictConfig    FeatureFlag = "STRICT_CONFIG"    // строгая валидация конфигурации
	FeatureTelemetry       FeatureFlag = "TELEMETRY"        // сбор телеметрии
)

// defaultFlags содержит значения флагов по умолчанию (все выключены).
var defaultFlags = map[FeatureFlag]bool{
	FeatureNewSSE:          false,
	FeatureNewUI:           false,
	FeatureExperimentalAPI: false,
	FeatureStrictConfig:    false,
	FeatureTelemetry:       false,
}

var (
	// cache хранит результаты проверки флагов для ускорения повторных вызовов.
	cache sync.Map
)

// envName возвращает имя переменной окружения для указанного флага.
func envName(flag FeatureFlag) string {
	return fmt.Sprintf("FEATURE_%s", strings.ToUpper(string(flag)))
}

// IsEnabled проверяет, включён ли функциональный флаг.
// Сначала проверяет переменную окружения FEATURE_<NAME>.
// Если переменная не установлена, возвращает значение по умолчанию.
func IsEnabled(flag FeatureFlag) bool {
	// Проверяем кеш
	if v, ok := cache.Load(flag); ok {
		if val, ok := v.(bool); ok {
			return val
		}
		cache.Delete(flag)
	}

	// Проверяем переменную окружения
	if val, ok := os.LookupEnv(envName(flag)); ok {
		enabled, err := strconv.ParseBool(val)
		if err == nil {
			cache.Store(flag, enabled)
			return enabled
		}
	}

	// Возвращаем значение по умолчанию
	defaultVal := defaultFlags[flag]
	cache.Store(flag, defaultVal)
	return defaultVal
}

// SetEnabled принудительно устанавливает значение флага (для тестов).
func SetEnabled(flag FeatureFlag, enabled bool) {
	cache.Store(flag, enabled)
}

// Reset очищает кеш флагов (для тестов).
func Reset() {
	cache = sync.Map{}
}
