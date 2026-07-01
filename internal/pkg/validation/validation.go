// internal/pkg/validation/validation.go
package validation

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// ValidateString проверяет, что строка не пустая и имеет длину в заданном диапазоне.
func ValidateString(field, value string, minLen, maxLen int) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New(field + " не может быть пустым")
	}
	if len(trimmed) < minLen {
		return errors.New(field + " должен содержать не менее " + strconv.Itoa(minLen) + " символов")
	}
	if len(trimmed) > maxLen {
		return errors.New(field + " не может превышать " + strconv.Itoa(maxLen) + " символов")
	}
	return nil
}

// ValidatePositiveUint проверяет, что значение больше нуля.
func ValidatePositiveUint(field string, value uint) error {
	if value == 0 {
		return errors.New(field + " должен быть положительным числом")
	}
	return nil
}

// ValidateGameDates проверяет корректность дат (дедлайн не позже старта, даты не в прошлом).
func ValidateGameDates(startsAt, registrationDeadline *time.Time) error {
	if registrationDeadline != nil && registrationDeadline.Before(time.Now()) {
		return errors.New("крайний срок регистрации не может быть в прошлом")
	}
	if startsAt != nil && startsAt.Before(time.Now()) {
		return errors.New("дата начала не может быть в прошлом")
	}
	if registrationDeadline != nil && startsAt != nil && registrationDeadline.After(*startsAt) {
		return errors.New("крайний срок регистрации не может быть позже даты начала")
	}
	return nil
}

// ValidateStartDate проверяет, что дата начала не в прошлом (кастомный валидатор для gin).
func ValidateStartDate(t *time.Time) bool {
	if t == nil {
		return true
	}
	return !t.Before(time.Now())
}
