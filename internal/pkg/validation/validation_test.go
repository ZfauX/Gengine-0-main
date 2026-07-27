package validation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidateString_Valid(t *testing.T) {
	t.Parallel()
	assert.NoError(t, ValidateString("name", "hello", 2, 10))
}

func TestValidateString_Empty(t *testing.T) {
	t.Parallel()
	err := ValidateString("name", "   ", 2, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "не может быть пустым")
}

func TestValidateString_TooShort(t *testing.T) {
	t.Parallel()
	err := ValidateString("name", "ab", 3, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "не менее 3")
}

func TestValidateString_TooLong(t *testing.T) {
	t.Parallel()
	err := ValidateString("name", "hello world", 2, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "не может превышать 10")
}

func TestValidateString_ExactLength(t *testing.T) {
	t.Parallel()
	assert.NoError(t, ValidateString("name", "abc", 3, 3))
}

func TestValidatePositiveUint_Zero(t *testing.T) {
	t.Parallel()
	err := ValidatePositiveUint("age", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "положительным числом")
}

func TestValidatePositiveUint_Valid(t *testing.T) {
	t.Parallel()
	assert.NoError(t, ValidatePositiveUint("age", 1))
	assert.NoError(t, ValidatePositiveUint("age", 100))
}

func TestValidateGameDates_BothNil(t *testing.T) {
	t.Parallel()
	assert.NoError(t, ValidateGameDates(nil, nil))
}

func TestValidateGameDates_DeadlineNil(t *testing.T) {
	t.Parallel()
	future := time.Now().Add(time.Hour)
	assert.NoError(t, ValidateGameDates(&future, nil))
}

func TestValidateGameDates_StartNil(t *testing.T) {
	t.Parallel()
	future := time.Now().Add(time.Hour)
	assert.NoError(t, ValidateGameDates(nil, &future))
}

func TestValidateGameDates_DeadlineInPast(t *testing.T) {
	t.Parallel()
	past := time.Now().Add(-time.Hour)
	err := ValidateGameDates(nil, &past)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "крайний срок регистрации")
}

func TestValidateGameDates_StartInPast(t *testing.T) {
	t.Parallel()
	past := time.Now().Add(-time.Hour)
	err := ValidateGameDates(&past, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "дата начала")
}

func TestValidateGameDates_DeadlineAfterStart(t *testing.T) {
	t.Parallel()
	start := time.Now().Add(2 * time.Hour)
	deadline := time.Now().Add(3 * time.Hour)
	err := ValidateGameDates(&start, &deadline)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "крайний срок регистрации")
}

func TestValidateGameDates_ValidBoth(t *testing.T) {
	t.Parallel()
	deadline := time.Now().Add(time.Hour)
	start := deadline.Add(time.Hour)
	assert.NoError(t, ValidateGameDates(&start, &deadline))
}

func TestValidateStartDate_Nil(t *testing.T) {
	t.Parallel()
	assert.True(t, ValidateStartDate(nil))
}

func TestValidateStartDate_Future(t *testing.T) {
	t.Parallel()
	future := time.Now().Add(time.Hour)
	assert.True(t, ValidateStartDate(&future))
}

func TestValidateStartDate_Past(t *testing.T) {
	t.Parallel()
	past := time.Now().Add(-time.Hour)
	assert.False(t, ValidateStartDate(&past))
}

func TestFieldErrors_Add(t *testing.T) {
	t.Parallel()
	fe := make(FieldErrors)
	fe.Add("name", assert.AnError)
	assert.True(t, fe.HasErrors())
	assert.Equal(t, assert.AnError.Error(), fe["name"])
}

func TestFieldErrors_Add_NilError(t *testing.T) {
	t.Parallel()
	fe := make(FieldErrors)
	fe.Add("name", nil)
	assert.False(t, fe.HasErrors())
}

func TestFieldErrors_HasErrors(t *testing.T) {
	t.Parallel()
	fe := make(FieldErrors)
	assert.False(t, fe.HasErrors())
	fe["a"] = "err"
	assert.True(t, fe.HasErrors())
}

func TestFieldErrors_Error(t *testing.T) {
	t.Parallel()
	fe := make(FieldErrors)
	assert.Equal(t, "", fe.Error())
	fe["a"] = "first"
	fe["b"] = "second"
	assert.NotEmpty(t, fe.Error())
}

func TestFieldErrors_Error_Empty(t *testing.T) {
	t.Parallel()
	fe := make(FieldErrors)
	assert.Equal(t, "", fe.Error())
}

func TestIsAllowedType_Match(t *testing.T) {
	t.Parallel()
	assert.True(t, IsAllowedType("image/png", AllowedImageTypes))
	assert.True(t, IsAllowedType("image/jpeg", AllowedImageTypes))
	assert.True(t, IsAllowedType("image/webp", AllowedImageTypes))
}

func TestIsAllowedType_NoMatch(t *testing.T) {
	t.Parallel()
	assert.False(t, IsAllowedType("image/gif", AllowedImageTypes))
	assert.False(t, IsAllowedType("application/pdf", AllowedImageTypes))
}

func TestIsAllowedType_CaseInsensitive(t *testing.T) {
	t.Parallel()
	assert.True(t, IsAllowedType("IMAGE/PNG", AllowedImageTypes))
	assert.True(t, IsAllowedType("Image/JPEG", AllowedImageTypes))
}

func TestIsAllowedType_UploadTypes(t *testing.T) {
	t.Parallel()
	allowed := AllowedUploadTypes
	assert.True(t, IsAllowedType("image/jpeg", allowed))
	assert.True(t, IsAllowedType("image/png", allowed))
	assert.True(t, IsAllowedType("image/gif", allowed))
	assert.True(t, IsAllowedType("application/pdf", allowed))
	assert.True(t, IsAllowedType("text/plain", allowed))
	assert.False(t, IsAllowedType("image/webp", allowed))
	assert.False(t, IsAllowedType("", allowed))
}

func TestIsAllowedType_EmptyAllowed(t *testing.T) {
	t.Parallel()
	assert.False(t, IsAllowedType("image/png", []string{}))
}

func TestIsAllowedType_EmptyMIME(t *testing.T) {
	t.Parallel()
	assert.False(t, IsAllowedType("", AllowedImageTypes))
}
