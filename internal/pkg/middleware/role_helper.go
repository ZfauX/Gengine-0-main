// Package middleware предоставляет вспомогательные функции для работы с gin.Context.
package middleware

// GetRole возвращает роль пользователя из контекста.
// Возвращает роль и true, если роль установлена, или "user" и false, если нет.
func GetRole(c IsAdminChecker) (string, bool) {
	role, exists := c.Get("role")
	if !exists {
		return "user", false
	}
	roleStr, ok := role.(string)
	if !ok {
		return "user", false
	}
	return roleStr, true
}

// IsAdminChecker — интерфейс для получения и установки данных в gin.Context.
// Позволяет использовать функции IsAdmin/SetIsAdmin как с *gin.Context, так и с тестовыми моками.
type IsAdminChecker interface {
	Get(key string) (interface{}, bool)
	Set(key string, value interface{})
}

// IsAdmin проверяет, является ли пользователь администратором.
// Возвращает true, если в контексте установлена роль "admin".
func IsAdmin(c IsAdminChecker) bool {
	role, exists := GetRole(c)
	return exists && role == "admin"
}

// SetIsAdmin устанавливает флаг IsAdmin в контекст на основе роли.
// Вызывается после AuthRequired, чтобы не дублировать логику проверки.
func SetIsAdmin(c IsAdminChecker) {
	if IsAdmin(c) {
		c.Set("IsAdmin", true)
	}
}
