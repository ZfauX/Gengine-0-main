package errors

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	t.Parallel()
	e := New(ErrNotFound, "game not found")
	assert.Equal(t, ErrNotFound, e.Code)
	assert.Equal(t, "game not found", e.Message)
	assert.Equal(t, http.StatusNotFound, e.HTTPStatus)
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	e := NotFound("Game")
	assert.Equal(t, ErrNotFound, e.Code)
	assert.Equal(t, "Game не найден", e.Message)
	assert.Equal(t, http.StatusNotFound, e.HTTPStatus)
}

func TestBadRequest(t *testing.T) {
	t.Parallel()
	e := BadRequest("invalid input")
	assert.Equal(t, ErrBadRequest, e.Code)
	assert.Equal(t, "invalid input", e.Message)
	assert.Equal(t, http.StatusBadRequest, e.HTTPStatus)
}

func TestBadRequest_Default(t *testing.T) {
	t.Parallel()
	e := BadRequest("")
	assert.Equal(t, "Неверный запрос", e.Message)
}

func TestValidationError(t *testing.T) {
	t.Parallel()
	e := ValidationError(FieldError{Field: "name", Message: "required"})
	assert.Equal(t, ErrValidationError, e.Code)
	assert.Equal(t, http.StatusBadRequest, e.HTTPStatus)
	assert.Len(t, e.Fields, 1)
	assert.Equal(t, "name", e.Fields[0].Field)
}

func TestValidationError_NoFields(t *testing.T) {
	t.Parallel()
	e := ValidationError()
	assert.Equal(t, "Ошибка валидации", e.Message)
	assert.Empty(t, e.Fields)
}

func TestInternal(t *testing.T) {
	t.Parallel()
	e := Internal("server error")
	assert.Equal(t, ErrInternal, e.Code)
	assert.Equal(t, http.StatusInternalServerError, e.HTTPStatus)
}

func TestInternal_Default(t *testing.T) {
	t.Parallel()
	e := Internal("")
	assert.Equal(t, "Внутренняя ошибка сервера", e.Message)
}

func TestForbidden(t *testing.T) {
	t.Parallel()
	e := Forbidden("access denied")
	assert.Equal(t, ErrForbidden, e.Code)
	assert.Equal(t, http.StatusForbidden, e.HTTPStatus)
}

func TestForbidden_Default(t *testing.T) {
	t.Parallel()
	e := Forbidden("")
	assert.Equal(t, "Доступ запрещён", e.Message)
}

func TestUnauthorized(t *testing.T) {
	t.Parallel()
	e := Unauthorized("login required")
	assert.Equal(t, ErrUnauthorized, e.Code)
	assert.Equal(t, http.StatusUnauthorized, e.HTTPStatus)
}

func TestRateLimit(t *testing.T) {
	t.Parallel()
	e := RateLimit("too fast")
	assert.Equal(t, ErrRateLimit, e.Code)
	assert.Equal(t, http.StatusTooManyRequests, e.HTTPStatus)
}

func TestAppError_Error(t *testing.T) {
	t.Parallel()
	e := New(ErrBadRequest, "bad")
	assert.Equal(t, "bad", e.Error())
}

func TestAppError_ErrorInterface(t *testing.T) {
	t.Parallel()
	var err error = New(ErrNotFound, "missing")
	assert.Equal(t, "missing", err.Error())
}

func TestAppError_MessageFor_RU(t *testing.T) {
	t.Parallel()
	e := New(ErrNotFound, "not found")
	assert.Equal(t, "Ресурс не найден", e.MessageFor("ru"))
}

func TestAppError_MessageFor_Other(t *testing.T) {
	t.Parallel()
	e := New(ErrNotFound, "not found")
	assert.Equal(t, "not found", e.MessageFor("en"))
}

func TestAppError_MessageFor_UnknownCode(t *testing.T) {
	t.Parallel()
	e := New(ErrorCode("custom_code"), "custom message")
	assert.Equal(t, "custom message", e.MessageFor("ru"))
	assert.Equal(t, "custom message", e.MessageFor("en"))
}

func TestAppError_JSONResponse(t *testing.T) {
	t.Parallel()
	e := New(ErrNotFound, "missing")
	resp := e.JSONResponse("")
	assert.Equal(t, "missing", resp["error"])
	assert.Equal(t, ErrNotFound, resp["code"])
	assert.Equal(t, http.StatusNotFound, resp["status"])
}

func TestAppError_JSONResponse_RU(t *testing.T) {
	t.Parallel()
	e := New(ErrNotFound, "not found")
	resp := e.JSONResponse("ru")
	assert.Equal(t, "Ресурс не найден", resp["error"])
}

func TestAppError_JSONResponse_WithFields(t *testing.T) {
	t.Parallel()
	e := ValidationError(FieldError{Field: "email", Message: "invalid"})
	resp := e.JSONResponse("")
	assert.Contains(t, resp, "fields")
}

func TestAppError_JSONResponse_WithDetails(t *testing.T) {
	t.Parallel()
	e := New(ErrBadRequest, "bad")
	e.Details = map[string]string{"key": "value"}
	resp := e.JSONResponse("")
	assert.Equal(t, map[string]string{"key": "value"}, resp["details"])
}

func TestAppError_MarshalJSON(t *testing.T) {
	t.Parallel()
	e := New(ErrNotFound, "missing")
	data, err := e.MarshalJSON()
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"code":"not_found"`)
}

func TestHTTPStatusForErrorCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code   ErrorCode
		status int
	}{
		{ErrInternal, http.StatusInternalServerError},
		{ErrBadRequest, http.StatusBadRequest},
		{ErrValidationError, http.StatusBadRequest},
		{ErrUnauthorized, http.StatusUnauthorized},
		{ErrForbidden, http.StatusForbidden},
		{ErrNotFound, http.StatusNotFound},
		{ErrConflict, http.StatusConflict},
		{ErrRateLimit, http.StatusTooManyRequests},
		{ErrConfiguration, http.StatusServiceUnavailable},
		{ErrTwoFactorRequired, http.StatusUnauthorized},
		{ErrExportNotReady, http.StatusServiceUnavailable},
		{ErrVotingClosed, http.StatusConflict},
		{ErrorCode("unknown"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.status, HTTPStatusForErrorCode(tc.code), "code: %s", tc.code)
	}
}

func TestWithFields(t *testing.T) {
	t.Parallel()
	e := New(ErrBadRequest, "bad")
	e2 := e.WithFields(FieldError{Field: "name", Message: "required"})
	assert.Len(t, e2.Fields, 1)
	assert.Len(t, e.Fields, 0) // original unchanged
}

func TestWithFields_Append(t *testing.T) {
	t.Parallel()
	e := New(ErrBadRequest, "bad").WithFields(FieldError{Field: "a", Message: "1"})
	e2 := e.WithFields(FieldError{Field: "b", Message: "2"})
	assert.Len(t, e2.Fields, 2)
}

func TestWrap_WithAppError(t *testing.T) {
	t.Parallel()
	inner := New(ErrNotFound, "original")
	wrapped := Wrap(inner, "context")
	assert.Equal(t, ErrNotFound, wrapped.Code)
	assert.Equal(t, "context: original", wrapped.Message)
}

func TestWrap_WithStdError(t *testing.T) {
	t.Parallel()
	inner := errors.New("std error")
	wrapped := Wrap(inner, "wrapping")
	assert.Equal(t, ErrInternal, wrapped.Code)
	assert.Equal(t, "wrapping: std error", wrapped.Message)
}

func TestWrap_Nil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, Wrap(nil, "context"))
}

func TestIsNotFound(t *testing.T) {
	t.Parallel()
	assert.True(t, IsNotFound(New(ErrNotFound, "x")))
	assert.False(t, IsNotFound(New(ErrInternal, "x")))
	assert.False(t, IsNotFound(errors.New("std")))
}

func TestIsForbidden(t *testing.T) {
	t.Parallel()
	assert.True(t, IsForbidden(New(ErrForbidden, "x")))
	assert.False(t, IsForbidden(New(ErrNotFound, "x")))
}

func TestIsValidationError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsValidationError(New(ErrValidationError, "x")))
	assert.False(t, IsValidationError(New(ErrBadRequest, "x")))
}

func TestIsAppError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsAppError(New(ErrInternal, "x")))
	assert.False(t, IsAppError(errors.New("std")))
}

func TestExtractAppError(t *testing.T) {
	t.Parallel()
	e := ExtractAppError(New(ErrNotFound, "x"))
	assert.NotNil(t, e)
	assert.Equal(t, ErrNotFound, e.Code)

	assert.Nil(t, ExtractAppError(errors.New("std")))
}

func TestFieldErrorf(t *testing.T) {
	t.Parallel()
	fe := FieldErrorf("name", "must be at least %d characters", 3)
	assert.Equal(t, "name", fe.Field)
	assert.Equal(t, "must be at least 3 characters", fe.Message)
}

func TestIsCode(t *testing.T) {
	t.Parallel()
	assert.True(t, IsCode(New(ErrNotFound, "x"), ErrNotFound))
	assert.False(t, IsCode(New(ErrNotFound, "x"), ErrInternal))
	assert.False(t, IsCode(errors.New("std"), ErrNotFound))
}

func TestHasFieldError(t *testing.T) {
	t.Parallel()
	e := ValidationError(FieldError{Field: "email", Message: "invalid"})
	assert.True(t, HasFieldError(e, "email"))
	assert.False(t, HasFieldError(e, "name"))
	assert.False(t, HasFieldError(errors.New("std"), "x"))
}

func TestGetFieldError(t *testing.T) {
	t.Parallel()
	e := ValidationError(FieldError{Field: "email", Message: "invalid email"})
	assert.Equal(t, "invalid email", GetFieldError(e, "email"))
	assert.Equal(t, "", GetFieldError(e, "name"))
	assert.Equal(t, "", GetFieldError(errors.New("std"), "x"))
}

func TestErrorList_Add(t *testing.T) {
	t.Parallel()
	var el ErrorList
	el.Add("name", "required")
	assert.True(t, el.Has("name"))
	assert.Equal(t, "required", el["name"])
}

func TestErrorList_Add_Nil(t *testing.T) {
	t.Parallel()
	var el ErrorList
	el.Add("a", "1")
	el.Add("b", "2")
	assert.Equal(t, "1", el["a"])
	assert.Equal(t, "2", el["b"])
}

func TestErrorList_Has(t *testing.T) {
	t.Parallel()
	el := ErrorList{"a": "1"}
	assert.True(t, el.Has("a"))
	assert.False(t, el.Has("b"))
}

func TestErrorList_Has_Nil(t *testing.T) {
	t.Parallel()
	var el ErrorList
	assert.False(t, el.Has("a"))
}

func TestErrorList_ToAppError(t *testing.T) {
	t.Parallel()
	el := ErrorList{"name": "required", "email": "invalid"}
	e := el.ToAppError()
	assert.NotNil(t, e)
	assert.Equal(t, ErrValidationError, e.Code)
	assert.Len(t, e.Fields, 2)
}

func TestErrorList_ToAppError_Empty(t *testing.T) {
	t.Parallel()
	var el ErrorList
	assert.Nil(t, el.ToAppError())
}

func TestSanitizeMessageForLog(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "login failed", SanitizeMessageForLog("login failed"))
}

func TestSanitizeMessageForLog_Sensitive(t *testing.T) {
	t.Parallel()
	result := SanitizeMessageForLog("password=secret")
	assert.Contains(t, result, "***")
	assert.NotContains(t, result, "secret")
}

func TestSanitizeMessageForLog_Token(t *testing.T) {
	t.Parallel()
	result := SanitizeMessageForLog("token=abc123")
	assert.Contains(t, result, "***")
}

func TestIsGameError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsGameError(New(ErrGameNotFound, "")))
	assert.True(t, IsGameError(New(ErrGameDraft, "")))
	assert.False(t, IsGameError(New(ErrTeamFull, "")))
}

func TestIsTeamError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsTeamError(New(ErrTeamFull, "")))
	assert.True(t, IsTeamError(New(ErrAlreadyJoined, "")))
	assert.False(t, IsTeamError(New(ErrGameNotFound, "")))
}

func TestIsAuthError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsAuthError(New(ErrInvalidCredentials, "")))
	assert.True(t, IsAuthError(New(ErrTokenExpired, "")))
	assert.False(t, IsAuthError(New(ErrNotFound, "")))
}

func TestIsVotingError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsVotingError(New(ErrVotingClosed, "")))
	assert.False(t, IsVotingError(New(ErrNotFound, "")))
}

func TestIsExportError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsExportError(New(ErrExportNotReady, "")))
	assert.False(t, IsExportError(New(ErrNotFound, "")))
}

func TestErrorText(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Ресурс не найден", ErrorText(ErrNotFound))
}

func TestErrorText_Unknown(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "custom_code", ErrorText(ErrorCode("custom_code")))
}
