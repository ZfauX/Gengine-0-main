package middleware

const (
	ErrAuthRequired       = "требуется аутентификация"
	ErrInvalidToken       = "невалидный токен"
	ErrAccessDenied       = "доступ запрещён"
	ErrInsufficientRights = "недостаточно прав"
	ErrInternalServer     = "внутренняя ошибка сервера"

	ErrRateLimitGlobal   = "слишком много запросов"
	ErrRateLimitLogin    = "слишком много попыток входа, попробуйте позже"
	ErrRateLimitRegister = "слишком много попыток регистрации, попробуйте позже"
	ErrRateLimitCode     = "слишком частый ввод кодов"
	ErrRateLimitSSE      = "слишком много SSE-подключений"

	ErrInvalidGameID = "неверный game_id"
)
