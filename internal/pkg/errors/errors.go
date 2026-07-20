// internal/pkg/errors/errors.go
// Пакет структурированных ошибок с кодами, HTTP-статусами и локализацией.
package errors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrorCode — уникальный код ошибки для логирования и локализации.
type ErrorCode string

const (
	// Core errors
	ErrInternal        ErrorCode = "internal_error"
	ErrBadRequest      ErrorCode = "bad_request"
	ErrValidationError ErrorCode = "validation_error"
	ErrUnauthorized    ErrorCode = "unauthorized"
	ErrForbidden       ErrorCode = "forbidden"
	ErrNotFound        ErrorCode = "not_found"
	ErrConflict        ErrorCode = "conflict"
	ErrRateLimit       ErrorCode = "rate_limit"
	ErrConfiguration   ErrorCode = "configuration_error"

	// Domain: game
	ErrGameNotFound           ErrorCode = "game_not_found"
	ErrNotGameAuthor          ErrorCode = "not_game_author"
	ErrNotGameManager         ErrorCode = "not_game_manager"
	ErrGameDraft              ErrorCode = "game_draft"
	ErrGameNotStarted         ErrorCode = "game_not_started"
	ErrGameFinished           ErrorCode = "game_finished"
	ErrGameRegistrationClosed ErrorCode = "registration_closed"

	// Domain: gameplay
	ErrWrongCode         ErrorCode = "wrong_code"
	ErrNoAttemptsLeft    ErrorCode = "no_attempts_left"
	ErrNoHintsLeft       ErrorCode = "no_hints_left"
	ErrTimeExpired       ErrorCode = "time_expired"
	ErrLevelNotAvailable ErrorCode = "level_not_available"

	// Domain: team
	ErrTeamFull      ErrorCode = "team_full"
	ErrAlreadyJoined ErrorCode = "already_joined"
	ErrNotTeamMember ErrorCode = "not_team_member"
	ErrTeamNotFound  ErrorCode = "team_not_found"

	// Domain: auth
	ErrEmailAlreadyUsed   ErrorCode = "email_already_used"
	ErrInvalidCredentials ErrorCode = "invalid_credentials"
	ErrAccountNotVerified ErrorCode = "account_not_verified"
	ErrInvalidOTP         ErrorCode = "invalid_otp"
	ErrInvalidToken       ErrorCode = "invalid_token"
	ErrTokenExpired       ErrorCode = "token_expired"
	ErrCSRFInvalid        ErrorCode = "csrf_invalid"

	// Domain: file
	ErrFileTooLarge     ErrorCode = "file_too_large"
	ErrFileUploadFailed ErrorCode = "file_upload_failed"
	ErrFileNotFound     ErrorCode = "file_not_found"
	ErrInvalidFileType  ErrorCode = "invalid_file_type"
)

// ruMessages — русскоязычные сообщения для каждого кода ошибки.
var ruMessages = map[ErrorCode]string{
	ErrInternal:               "Внутренняя ошибка сервера",
	ErrBadRequest:             "Неверный запрос",
	ErrValidationError:        "Ошибка валидации данных",
	ErrUnauthorized:           "Необходима авторизация",
	ErrForbidden:              "Доступ запрещён",
	ErrNotFound:               "Ресурс не найден",
	ErrConflict:               "Конфликт данных",
	ErrRateLimit:              "Слишком много запросов. Подождите",
	ErrConfiguration:          "Ошибка конфигурации сервера",
	ErrGameNotFound:           "Игра не найдена",
	ErrNotGameAuthor:          "У вас нет прав для этого действия",
	ErrNotGameManager:         "Недостаточно прав для управления игрой",
	ErrGameDraft:              "Игра в черновике и недоступна",
	ErrGameNotStarted:         "Игра ещё не началась",
	ErrGameFinished:           "Игра завершена",
	ErrGameRegistrationClosed: "Регистрация закрыта",
	ErrWrongCode:              "Неверный код. Попробуйте ещё раз",
	ErrNoAttemptsLeft:         "Попытки исчерпаны",
	ErrNoHintsLeft:            "Подсказки исчерпаны",
	ErrTimeExpired:            "Время вышло",
	ErrLevelNotAvailable:      "Уровень недоступен",
	ErrTeamFull:               "Команда заполнена",
	ErrAlreadyJoined:          "Вы уже в этой команде",
	ErrNotTeamMember:          "Вы не участник этой команды",
	ErrTeamNotFound:           "Команда не найдена",
	ErrEmailAlreadyUsed:       "Email уже зарегистрирован",
	ErrInvalidCredentials:     "Неверный логин или пароль",
	ErrAccountNotVerified:     "Аккаунт не подтверждён",
	ErrInvalidOTP:             "Неверный код подтверждения",
	ErrInvalidToken:           "Неверный токен",
	ErrTokenExpired:           "Токен истёк",
	ErrCSRFInvalid:            "CSRF-токен недействителен",
	ErrFileTooLarge:           "Файл слишком большой",
	ErrFileUploadFailed:       "Ошибка загрузки файла",
	ErrFileNotFound:           "Файл не найден",
	ErrInvalidFileType:        "Неподдерживаемый формат файла",
}

// AppError — структурированная ошибка с кодом, HTTP-статусом и полями.
type AppError struct {
	HTTPStatus int          `json:"-"`
	Code       ErrorCode    `json:"code"`
	Message    string       `json:"message"`
	Details    any          `json:"details,omitempty"`
	Fields     []FieldError `json:"fields,omitempty"`
}

// FieldError — ошибка конкретного поля формы.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error реализует интерфейс error.
func (e *AppError) Error() string {
	return e.Message
}

// MessageFor возвращает локализованное сообщение.
func (e *AppError) MessageFor(lang string) string {
	if lang == "ru" {
		if msg, ok := ruMessages[e.Code]; ok {
			return msg
		}
	}
	return e.Message
}

// JSONResponse возвращает структуру для JSON-ответа клиенту.
func (e *AppError) JSONResponse(lang string) map[string]any {
	resp := map[string]any{
		"error":  e.Message,
		"code":   e.Code,
		"status": e.HTTPStatus,
	}
	if lang == "ru" {
		resp["error"] = e.MessageFor("ru")
	}
	if e.Details != nil {
		resp["details"] = e.Details
	}
	if len(e.Fields) > 0 {
		resp["fields"] = e.Fields
	}
	return resp
}

// MarshalJSON для json-сериализации.
func (e *AppError) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.JSONResponse(""))
}

// HTTPStatusForErrorCode возвращает HTTP-статус по коду ошибки.
func HTTPStatusForErrorCode(code ErrorCode) int {
	switch code {
	case ErrInternal:
		return http.StatusInternalServerError
	case ErrBadRequest, ErrValidationError:
		return http.StatusBadRequest
	case ErrUnauthorized, ErrInvalidToken, ErrTokenExpired:
		return http.StatusUnauthorized
	case ErrForbidden:
		return http.StatusForbidden
	case ErrNotFound, ErrGameNotFound, ErrTeamNotFound, ErrFileNotFound:
		return http.StatusNotFound
	case ErrConflict, ErrEmailAlreadyUsed, ErrAlreadyJoined:
		return http.StatusConflict
	case ErrRateLimit:
		return http.StatusTooManyRequests
	case ErrConfiguration:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// --- Конструкторы ---

// New создаёт ошибку с кодом.
func New(code ErrorCode, message string) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		HTTPStatus: HTTPStatusForErrorCode(code),
	}
}

// NotFound создаёт ошибку "не найдено".
func NotFound(resource string) *AppError {
	return &AppError{
		Code:       ErrNotFound,
		Message:    resource + " не найден",
		HTTPStatus: http.StatusNotFound,
	}
}

// BadRequest создаёт ошибку "неверный запрос".
func BadRequest(message string) *AppError {
	if message == "" {
		message = "Неверный запрос"
	}
	return &AppError{
		Code:       ErrBadRequest,
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// ValidationError создаёт ошибку валидации с полями.
func ValidationError(fields ...FieldError) *AppError {
	msg := "Ошибка валидации"
	if len(fields) > 0 {
		msg = fmt.Sprintf("Ошибка валидации: %d полей", len(fields))
	}
	return &AppError{
		Code:       ErrValidationError,
		Message:    msg,
		HTTPStatus: http.StatusBadRequest,
		Fields:     fields,
	}
}

// Internal создаёт внутреннюю ошибку сервера.
func Internal(message string) *AppError {
	if message == "" {
		message = "Внутренняя ошибка сервера"
	}
	return &AppError{
		Code:       ErrInternal,
		Message:    message,
		HTTPStatus: http.StatusInternalServerError,
	}
}

// Forbidden создаёт ошибку "доступ запрещён".
func Forbidden(message string) *AppError {
	if message == "" {
		message = "Доступ запрещён"
	}
	return &AppError{
		Code:       ErrForbidden,
		Message:    message,
		HTTPStatus: http.StatusForbidden,
	}
}

// Unauthorized создаёт ошибку "неавторизован".
func Unauthorized(message string) *AppError {
	if message == "" {
		message = "Необходима авторизация"
	}
	return &AppError{
		Code:       ErrUnauthorized,
		Message:    message,
		HTTPStatus: http.StatusUnauthorized,
	}
}

// RateLimit создаёт ошибку ограничения частоты запросов.
func RateLimit(message string) *AppError {
	if message == "" {
		message = "Слишком много запросов"
	}
	return &AppError{
		Code:       ErrRateLimit,
		Message:    message,
		HTTPStatus: http.StatusTooManyRequests,
	}
}

// WithFields добавляет ошибки полей.
func (e *AppError) WithFields(fields ...FieldError) *AppError {
	cp := *e
	cp.Fields = append(cp.Fields, fields...)
	return &cp
}

// Wrap добавляет контекст к существующей ошибке (не мутирует оригинал).
func Wrap(err error, message string) *AppError {
	if err == nil {
		return nil
	}
	var e *AppError
	if errors.As(err, &e) {
		cp := *e
		cp.Message = message + ": " + e.Message
		return &cp
	}
	return &AppError{
		Code:       ErrInternal,
		Message:    message + ": " + err.Error(),
		HTTPStatus: http.StatusInternalServerError,
	}
}

// --- helpers ---

// IsNotFound проверяет, является ли ошибка "не найдено".
func IsNotFound(err error) bool {
	var e *AppError
	return errors.As(err, &e) && e.Code == ErrNotFound
}

// IsForbidden проверяет, является ли ошибка "доступ запрещён".
func IsForbidden(err error) bool {
	var e *AppError
	return errors.As(err, &e) && e.Code == ErrForbidden
}

// IsValidationError проверяет, является ли ошибка валидации.
func IsValidationError(err error) bool {
	var e *AppError
	return errors.As(err, &e) && e.Code == ErrValidationError
}

// IsAppError проверяет, является ли ошибка структурированной.
func IsAppError(err error) bool {
	var e *AppError
	return errors.As(err, &e)
}

// ExtractAppError извлекает AppError из ошибки, возвращает nil если не найден.
func ExtractAppError(err error) *AppError {
	var e *AppError
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// FieldErrorf создаёт FieldError с форматированным сообщением.
func FieldErrorf(field, format string, args ...any) FieldError {
	return FieldError{
		Field:   field,
		Message: fmt.Sprintf(format, args...),
	}
}

// IsCode проверяет, совпадает ли код ошибки с заданным.
func IsCode(err error, code ErrorCode) bool {
	var e *AppError
	if errors.As(err, &e) {
		return e.Code == code
	}
	return false
}

// HasFieldError проверяет, есть ли ошибка для данного поля.
func HasFieldError(err error, field string) bool {
	var e *AppError
	if !errors.As(err, &e) {
		return false
	}
	for _, f := range e.Fields {
		if f.Field == field {
			return true
		}
	}
	return false
}

// GetFieldError возвращает ошибку для конкретного поля.
func GetFieldError(err error, field string) string {
	var e *AppError
	if !errors.As(err, &e) {
		return ""
	}
	for _, f := range e.Fields {
		if f.Field == field {
			return f.Message
		}
	}
	return ""
}

// ErrorList — коллекция ошибок для batch-валидации.
type ErrorList map[string]string

// Add добавляет ошибку для поля.
func (el ErrorList) Add(field, message string) {
	if el == nil {
		el = make(ErrorList)
	}
	el[field] = message
}

// Has проверяет, есть ли ошибка для поля.
func (el ErrorList) Has(field string) bool {
	if el == nil {
		return false
	}
	_, ok := el[field]
	return ok
}

// ToAppError преобразует ErrorList в AppError.
func (el ErrorList) ToAppError() *AppError {
	if len(el) == 0 {
		return nil
	}
	fields := make([]FieldError, 0, len(el))
	for field, msg := range el {
		fields = append(fields, FieldError{Field: field, Message: msg})
	}
	return ValidationError(fields...)
}

// SanitizeMessageForLog возвращает безопасную для логов версию сообщения.
// Убирает чувствительные данные.
func SanitizeMessageForLog(msg string) string {
	sensitive := []string{"password", "token", "secret", "key"}
	for _, s := range sensitive {
		lower := strings.ToLower(s)
		replacer := strings.NewReplacer(
			lower, "***",
			strings.ToUpper(lower[:1])+lower[1:], "***",
		)
		msg = replacer.Replace(msg)
	}
	return msg
}
