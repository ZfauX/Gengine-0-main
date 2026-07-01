// internal/pkg/errors/errors.go
package errors

import (
	"net/http"
)

// AppError представляет структурированную ошибку для API.
type AppError struct {
	HTTPStatus int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Details    any    `json:"details,omitempty"`
}

func (e *AppError) Error() string {
	return e.Message
}

// Конструкторы для распространённых ошибок
func NewValidationError(message string, details any) *AppError {
	return &AppError{
		HTTPStatus: http.StatusBadRequest,
		Code:       "validation_error",
		Message:    message,
		Details:    details,
	}
}

func NewNotFoundError(resource string) *AppError {
	return &AppError{
		HTTPStatus: http.StatusNotFound,
		Code:       "not_found",
		Message:    resource + " не найдено",
	}
}

func NewUnauthorizedError(message string) *AppError {
	return &AppError{
		HTTPStatus: http.StatusUnauthorized,
		Code:       "unauthorized",
		Message:    message,
	}
}

func NewForbiddenError(message string) *AppError {
	return &AppError{
		HTTPStatus: http.StatusForbidden,
		Code:       "forbidden",
		Message:    message,
	}
}

func NewInternalError(err error) *AppError {
	msg := "Внутренняя ошибка сервера"
	if err != nil {
		msg = err.Error()
	}
	return &AppError{
		HTTPStatus: http.StatusInternalServerError,
		Code:       "internal_error",
		Message:    msg,
	}
}

func NewConflictError(message string) *AppError {
	return &AppError{
		HTTPStatus: http.StatusConflict,
		Code:       "conflict",
		Message:    message,
	}
}

func NewBadRequestError(message string) *AppError {
	return &AppError{
		HTTPStatus: http.StatusBadRequest,
		Code:       "bad_request",
		Message:    message,
	}
}