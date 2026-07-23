// internal/pkg/errors/errors.go
// Пакет структурированных ошибок с кодами, HTTP-статусами и локализацией.
package errors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
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
	ErrTwoFactorRequired  ErrorCode = "two_factor_required"

	// Domain: file
	ErrFileTooLarge     ErrorCode = "file_too_large"
	ErrFileUploadFailed ErrorCode = "file_upload_failed"
	ErrFileNotFound     ErrorCode = "file_not_found"
	ErrInvalidFileType  ErrorCode = "invalid_file_type"

	// Domain: export
	ErrExportNotReady ErrorCode = "export_not_ready"
	ErrExportFailed   ErrorCode = "export_failed"

	// Domain: monitor
	ErrVotingClosed     ErrorCode = "voting_closed"
	ErrAlreadyVoted     ErrorCode = "already_voted"
	ErrVotingNotStarted ErrorCode = "voting_not_started"
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
	ErrTwoFactorRequired:      "Требуется двухфакторная аутентификация",
	ErrExportNotReady:         "Экспорт ещё не готов",
	ErrExportFailed:           "Ошибка экспорта данных",
	ErrVotingClosed:           "Голосование закрыто",
	ErrAlreadyVoted:           "Вы уже проголосовали",
	ErrVotingNotStarted:       "Голосование ещё не началось",
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
	case ErrTwoFactorRequired:
		return http.StatusUnauthorized
	case ErrExportNotReady, ErrExportFailed:
		return http.StatusServiceUnavailable
	case ErrVotingClosed, ErrAlreadyVoted, ErrVotingNotStarted:
		return http.StatusConflict
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
func (el *ErrorList) Add(field, message string) {
	if *el == nil {
		*el = make(ErrorList)
	}
	(*el)[field] = message
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

// --- Helper-функции для проверки ошибок ---

// IsGameError проверяет, является ли ошибка связанной с игрой.
func IsGameError(err error) bool {
	return IsCode(err, ErrGameNotFound) || IsCode(err, ErrNotGameAuthor) || IsCode(err, ErrNotGameManager) ||
		IsCode(err, ErrGameDraft) || IsCode(err, ErrGameNotStarted) || IsCode(err, ErrGameFinished)
}

// IsTeamError проверяет, является ли ошибка связанной с командой.
func IsTeamError(err error) bool {
	return IsCode(err, ErrTeamFull) || IsCode(err, ErrAlreadyJoined) || IsCode(err, ErrNotTeamMember) || IsCode(err, ErrTeamNotFound)
}

// IsAuthError проверяет, является ли ошибка связанной с аутентификацией.
func IsAuthError(err error) bool {
	return IsCode(err, ErrInvalidCredentials) || IsCode(err, ErrAccountNotVerified) || IsCode(err, ErrInvalidOTP) ||
		IsCode(err, ErrInvalidToken) || IsCode(err, ErrTokenExpired) || IsCode(err, ErrTwoFactorRequired)
}

// IsVotingError проверяет, является ли ошибка связанной с голосованием.
func IsVotingError(err error) bool {
	return IsCode(err, ErrVotingClosed) || IsCode(err, ErrAlreadyVoted) || IsCode(err, ErrVotingNotStarted)
}

// IsExportError проверяет, является ли ошибка связанной с экспортом.
func IsExportError(err error) bool {
	return IsCode(err, ErrExportNotReady) || IsCode(err, ErrExportFailed)
}

// enMessages — англоязычные сообщения для каждого кода ошибки.
var enMessages = map[ErrorCode]string{
	ErrInternal:               "Internal server error",
	ErrBadRequest:             "Bad request",
	ErrValidationError:        "Validation error",
	ErrUnauthorized:           "Authorization required",
	ErrForbidden:              "Access denied",
	ErrNotFound:               "Resource not found",
	ErrConflict:               "Data conflict",
	ErrRateLimit:              "Too many requests. Please wait",
	ErrConfiguration:          "Server configuration error",
	ErrGameNotFound:           "Game not found",
	ErrNotGameAuthor:          "You do not have permission for this action",
	ErrNotGameManager:         "Insufficient permissions to manage the game",
	ErrGameDraft:              "Game is a draft and not available",
	ErrGameNotStarted:         "Game has not started yet",
	ErrGameFinished:           "Game has finished",
	ErrGameRegistrationClosed: "Registration is closed",
	ErrWrongCode:              "Wrong code. Try again",
	ErrNoAttemptsLeft:         "No attempts left",
	ErrNoHintsLeft:            "No hints left",
	ErrTimeExpired:            "Time expired",
	ErrLevelNotAvailable:      "Level not available",
	ErrTeamFull:               "Team is full",
	ErrAlreadyJoined:          "You are already in this team",
	ErrNotTeamMember:          "You are not a member of this team",
	ErrTeamNotFound:           "Team not found",
	ErrEmailAlreadyUsed:       "Email is already registered",
	ErrInvalidCredentials:     "Invalid login or password",
	ErrAccountNotVerified:     "Account not verified",
	ErrInvalidOTP:             "Invalid verification code",
	ErrInvalidToken:           "Invalid token",
	ErrTokenExpired:           "Token expired",
	ErrCSRFInvalid:            "CSRF token is invalid",
	ErrFileTooLarge:           "File is too large",
	ErrFileUploadFailed:       "File upload failed",
	ErrFileNotFound:           "File not found",
	ErrInvalidFileType:        "Unsupported file format",
	ErrTwoFactorRequired:      "Two-factor authentication required",
	ErrExportNotReady:         "Export is not ready yet",
	ErrExportFailed:           "Data export failed",
	ErrVotingClosed:           "Voting is closed",
	ErrAlreadyVoted:           "You have already voted",
	ErrVotingNotStarted:       "Voting has not started yet",
}

// ErrorText возвращает локализованное сообщение по коду ошибки.
func ErrorText(code ErrorCode) string {
	if msg, ok := ruMessages[code]; ok {
		return msg
	}
	if msg, ok := enMessages[code]; ok {
		return msg
	}
	return string(code)
}

// ─── Helper-функции для логирования ──────────────────────────────

// LogIfError логирует ошибку и возвращает её.
// Используется когда ошибка должна быть возвращена вызывающему,
// но также нужна запись в лог для отладки в production.
func LogIfError(err error, msg string) error {
	if err != nil {
		log.Err(err).Msg(msg)
	}
	return err
}

// LogSilently логирует ошибку без возврата.
// Используется в cleanup-коде, где ошибка не критична.
func LogSilently(err error, msg string) {
	if err != nil {
		log.Err(err).Msg(msg)
	}
}

// LogAndReturn логирует ошибку и возвращает обёрнутую ошибку.
// Полезно когда нужно добавить контекст к существующей ошибке.
func LogAndReturn(err error, msg string) error {
	if err != nil {
		log.Err(err).Msg(msg)
		return fmt.Errorf("%s: %w", msg, err)
	}
	return nil
}
