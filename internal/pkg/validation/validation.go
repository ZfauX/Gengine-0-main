// internal/pkg/validation/validation.go
package validation

import (
	"errors"
	"net/url"
	"regexp"
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
	if registrationDeadline != nil && startsAt != nil && !registrationDeadline.Before(*startsAt) {
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

// FieldErrors хранит ошибки валидации для каждого поля формы.
type FieldErrors map[string]string

// Add добавляет ошибку для указанного поля, если err не nil.
func (fe FieldErrors) Add(field string, err error) {
	if err != nil {
		fe[field] = err.Error()
	}
}

// HasErrors возвращает true, если есть хотя бы одна ошибка.
func (fe FieldErrors) HasErrors() bool {
	return len(fe) > 0
}

// Error возвращает первую ошибку для отображения в общем блоке (совместимость).
func (fe FieldErrors) Error() string {
	for _, v := range fe {
		return v
	}
	return ""
}

// ValidateEmail проверяет формат email адреса.
func ValidateEmail(email string) error {
	if email == "" {
		return errors.New("email не может быть пустым")
	}
	if len(email) > 254 {
		return errors.New("email слишком длинный (максимум 254 символа)")
	}
	if !strings.Contains(email, "@") {
		return errors.New("неверный формат email")
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return errors.New("неверный формат email")
	}
	if parts[0] == "" || parts[1] == "" {
		return errors.New("неверный формат email")
	}
	if !strings.Contains(parts[1], ".") {
		return errors.New("неверный формат домена в email")
	}
	return nil
}

// ValidatePasswordStrength проверяет надёжность пароля.
func ValidatePasswordStrength(password string) error {
	if len(password) < 8 {
		return errors.New("пароль должен быть не менее 8 символов")
	}
	if len(password) > 128 {
		return errors.New("пароль слишком длинный (максимум 128 символов)")
	}
	hasUpper := false
	hasLower := false
	hasDigit := false
	for _, r := range password {
		if r >= 'A' && r <= 'Z' {
			hasUpper = true
		} else if r >= 'a' && r <= 'z' {
			hasLower = true
		} else if r >= '0' && r <= '9' {
			hasDigit = true
		}
	}
	if !hasUpper {
		return errors.New("пароль должен содержать заглавные буквы")
	}
	if !hasLower {
		return errors.New("пароль должен содержать строчные буквы")
	}
	if !hasDigit {
		return errors.New("пароль должен содержать цифры")
	}
	return nil
}

// ValidateURL проверяет формат URL.
func ValidateURL(u string) error {
	if u == "" {
		return nil
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return errors.New("неверный формат URL")
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("URL должен содержать схему (http/https) и хост")
	}
	return nil
}

// ValidatePositiveInt проверяет, что значение больше нуля.
func ValidatePositiveInt(field string, value int) error {
	if value <= 0 {
		return errors.New(field + " должен быть положительным числом")
	}
	return nil
}

// ValidateEnum проверяет, что значение входит в список допустимых значений.
func ValidateEnum(field, value string, allowed []string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return errors.New(field + " должен быть одним из: " + strings.Join(allowed, ", "))
}

// ValidateRegex проверяет строку по регулярному выражению.
func ValidateRegex(pattern, field, value string) error {
	matched, err := regexp.MatchString(pattern, value)
	if err != nil {
		return errors.New("ошибка валидации: " + err.Error())
	}
	if !matched {
		return errors.New(field + " имеет неверный формат")
	}
	return nil
}
