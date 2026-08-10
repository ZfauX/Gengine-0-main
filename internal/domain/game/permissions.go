package game

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// PermissionSlice — срез прав соавтора, хранимый в JSONB-колонке.
// Кастомный тип с Value()/Scan(), т.к. GORM не умеет сериализовать []string
// в jsonb автоматически (иначе ошибка "column permissions is of type jsonb
// but expression is of type record").
type PermissionSlice []string

// Value реализует driver.Valuer — сериализует срез в JSON.
func (p PermissionSlice) Value() (driver.Value, error) {
	if p == nil {
		return nil, nil
	}
	return json.Marshal(p)
}

// Scan реализует sql.Scanner — читает JSONB в срез строк.
func (p *PermissionSlice) Scan(value any) error {
	if value == nil {
		*p = nil
		return nil
	}
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, p)
	case string:
		return json.Unmarshal([]byte(v), p)
	default:
		return fmt.Errorf("PermissionSlice.Scan: unsupported type %T", value)
	}
}
