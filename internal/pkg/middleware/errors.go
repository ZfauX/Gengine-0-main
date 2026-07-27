package middleware

import "errors"

var (
	ErrAuthRequired       = errors.New("требуется аутентификация")
	ErrInvalidToken       = errors.New("невалидный токен")
	ErrAccessDenied       = errors.New("доступ запрещён")
	ErrInsufficientRights = errors.New("недостаточно прав")
	ErrInternalServer     = errors.New("внутренняя ошибка сервера")

	ErrRateLimitGlobal        = errors.New("слишком много запросов")
	ErrRateLimitLogin         = errors.New("слишком много попыток входа, попробуйте позже")
	ErrRateLimitRegister      = errors.New("слишком много попыток регистрации, попробуйте позже")
	ErrRateLimitCode          = errors.New("слишком частый ввод кодов")
	ErrRateLimitSSE           = errors.New("слишком много SSE-подключений")
	ErrRateLimitPasswordReset = errors.New("слишком много попыток сброса пароля, попробуйте позже")

	ErrInvalidGameID = errors.New("неверный game_id")
)
