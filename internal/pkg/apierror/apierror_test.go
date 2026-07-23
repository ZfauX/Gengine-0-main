package apierror

import (
	"encoding/json"
	"testing"
)

func TestNewAPIError(t *testing.T) {
	err := NewAPIError(400, "bad request")
	if err.Code != 400 {
		t.Errorf("expected code 400, got %d", err.Code)
	}
	if err.Message != "bad request" {
		t.Errorf("expected message 'bad request', got '%s'", err.Message)
	}
}

func TestNewAPIErrorWithDetail(t *testing.T) {
	err := NewAPIErrorWithDetail(400, "validation failed", "email is required")
	if err.Code != 400 {
		t.Errorf("expected code 400, got %d", err.Code)
	}
	if err.Detail != "email is required" {
		t.Errorf("expected detail 'email is required', got '%s'", err.Detail)
	}
}

func TestAPIError_Error(t *testing.T) {
	err := NewAPIError(404, "not found")
	if err.Error() != "not found" {
		t.Errorf("expected 'not found', got '%s'", err.Error())
	}
}

func TestAPIError_ToJSON(t *testing.T) {
	err := NewAPIErrorWithDetail(400, "bad request", "invalid id")
	data := err.ToJSON()

	var parsed APIError
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	if parsed.Code != 400 || parsed.Message != "bad request" || parsed.Detail != "invalid id" {
		t.Errorf("unexpected parsed values: %+v", parsed)
	}
}

func TestBadRequest(t *testing.T) {
	err := BadRequest("invalid input")
	if err.Code != 400 {
		t.Errorf("expected 400, got %d", err.Code)
	}
}

func TestUnauthorized(t *testing.T) {
	err := Unauthorized("login required")
	if err.Code != 401 {
		t.Errorf("expected 401, got %d", err.Code)
	}
}

func TestForbidden(t *testing.T) {
	err := Forbidden("access denied")
	if err.Code != 403 {
		t.Errorf("expected 403, got %d", err.Code)
	}
}

func TestNotFound(t *testing.T) {
	err := NotFound("user not found")
	if err.Code != 404 {
		t.Errorf("expected 404, got %d", err.Code)
	}
}

func TestConflict(t *testing.T) {
	err := Conflict("already exists")
	if err.Code != 409 {
		t.Errorf("expected 409, got %d", err.Code)
	}
}

func TestInternalServerError(t *testing.T) {
	err := InternalServerError("oops")
	if err.Code != 500 {
		t.Errorf("expected 500, got %d", err.Code)
	}
}

func TestValidationError(t *testing.T) {
	err := ValidationError("name is required")
	if err.Code != 400 {
		t.Errorf("expected 400, got %d", err.Code)
	}
	if err.Detail != "name is required" {
		t.Errorf("expected detail 'name is required', got '%s'", err.Detail)
	}
}

func TestErrorResponse_ToAPIError(t *testing.T) {
	resp := ErrorResponse{
		"code":    float64(403),
		"message": "forbidden",
		"detail":  "admin only",
	}
	err := resp.ToAPIError()
	if err.Code != 403 || err.Message != "forbidden" || err.Detail != "admin only" {
		t.Errorf("unexpected APIError: %+v", err)
	}
}

func TestErrorResponse_ToAPIError_NoCode(t *testing.T) {
	resp := ErrorResponse{"message": "oops"}
	err := resp.ToAPIError()
	if err.Code != 500 {
		t.Errorf("expected default 500, got %d", err.Code)
	}
}

func TestErrorResponse_ToAPIError_Empty(t *testing.T) {
	resp := ErrorResponse{}
	err := resp.ToAPIError()
	if err.Code != 500 {
		t.Errorf("expected default 500, got %d", err.Code)
	}
}
