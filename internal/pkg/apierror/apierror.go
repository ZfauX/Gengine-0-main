package apierror

import (
	"encoding/json"
)

type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

func (e APIError) Error() string {
	return e.Message
}

func (e APIError) ToJSON() []byte {
	b, _ := json.Marshal(e)
	return b
}

func NewAPIError(code int, message string) APIError {
	return APIError{Code: code, Message: message}
}

func NewAPIErrorWithDetail(code int, message, detail string) APIError {
	return APIError{Code: code, Message: message, Detail: detail}
}

func BadRequest(message string) APIError {
	return NewAPIError(400, message)
}

func Unauthorized(message string) APIError {
	return NewAPIError(401, message)
}

func Forbidden(message string) APIError {
	return NewAPIError(403, message)
}

func NotFound(message string) APIError {
	return NewAPIError(404, message)
}

func Conflict(message string) APIError {
	return NewAPIError(409, message)
}

func InternalServerError(message string) APIError {
	return NewAPIError(500, message)
}

func ValidationError(detail string) APIError {
	return NewAPIErrorWithDetail(400, "Validation failed", detail)
}

type ErrorResponse map[string]interface{}

func (e ErrorResponse) ToAPIError() APIError {
	if code, ok := e["code"].(float64); ok {
		var message, detail string
		if msg, ok := e["message"].(string); ok {
			message = msg
		}
		if d, ok := e["detail"].(string); ok {
			detail = d
		}
		return APIError{
			Code:    int(code),
			Message: message,
			Detail:  detail,
		}
	}
	return APIError{
		Code:    500,
		Message: "Internal Server Error",
		Detail:  "",
	}
}
