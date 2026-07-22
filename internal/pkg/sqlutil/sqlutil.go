// internal/pkg/sqlutil/sqlutil.go
package sqlutil

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// PaginatedQueryBuilder строит пагинированные запросы с оконной функцией COUNT(*) OVER().
// Позволяет избежать дублирования логики COUNT + SELECT в разных доменах.
type PaginatedQueryBuilder struct {
	baseSQL string
	where   string
	args    []any
	order   string
	limit   int
	offset  int
}

// NewPaginatedQuery создаёт новый билдер пагинированного запроса.
// baseSQL — основной SELECT без ORDER/LIMIT/OFFSET.
func NewPaginatedQuery(baseSQL string) *PaginatedQueryBuilder {
	return &PaginatedQueryBuilder{
		baseSQL: baseSQL,
		order:   "id DESC", // default
	}
}

// AddWhere добавляет условие WHERE с аргументами.
func (b *PaginatedQueryBuilder) AddWhere(where string, args ...any) *PaginatedQueryBuilder {
	if b.where != "" {
		b.where += " AND " + where
	} else {
		b.where = "WHERE " + where
	}
	b.args = append(b.args, args...)
	return b
}

// AddOrder задаёт ORDER BY с проверкой на SQL-инъекции.
// Допускаются только буквы, цифры, точки, запятые, пробелы, ASC, DESC, NULLS FIRST/LAST.
func (b *PaginatedQueryBuilder) AddOrder(order string) *PaginatedQueryBuilder {
	order = strings.TrimSpace(order)
	if order == "" {
		return b
	}
	for _, r := range order {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '.' && r != ',' && r != ' ' && r != '_' {
			return b
		}
	}
	b.order = order
	return b
}

// SetPagination задаёт пагинацию.
func (b *PaginatedQueryBuilder) SetPagination(page, perPage int) *PaginatedQueryBuilder {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	b.limit = perPage
	b.offset = (page - 1) * perPage
	return b
}

// Build формирует финальный SQL с COUNT(*) OVER() и LIMIT/OFFSET.
func (b *PaginatedQueryBuilder) Build() (string, []any) {
	sql := b.baseSQL
	if b.where != "" {
		sql += " " + b.where
	}
	sql += " ORDER BY " + b.order

	if b.limit > 0 {
		sql += " LIMIT ? OFFSET ?"
		b.args = append(b.args, b.limit, b.offset)
	}

	windowSQL := fmt.Sprintf("SELECT sub.*, COUNT(*) OVER() AS total_count FROM (%s) sub", sql)
	return windowSQL, b.args
}

// ScanWithTotal выполняет запрос и возвращает данные + total count.
// T — тип элемента в списке (например, Game, User).
func ScanWithTotal[T any](ctx context.Context, db *gorm.DB, query string, args []any) ([]T, int64, error) {
	var items []struct {
		Item       T
		TotalCount int64 `gorm:"column:total_count"`
	}

	if err := db.WithContext(ctx).Raw(query, args...).Scan(&items).Error; err != nil {
		return nil, 0, err
	}

	if len(items) == 0 {
		return []T{}, 0, nil
	}

	result := make([]T, len(items))
	var total int64
	for i, row := range items {
		result[i] = row.Item
		if i == 0 {
			total = row.TotalCount
		}
	}

	return result, total, nil
}

// EscapeLike экранирует спецсимволы LIKE (% и _) для безопасного поиска.
func EscapeLike(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 10)
	for _, char := range s {
		switch char {
		case '%', '_':
			b.WriteByte(92)
			b.WriteRune(char)
		default:
			b.WriteRune(char)
		}
	}
	return b.String()
}

// BuildLikePattern создаёт паттерн для ILIKE с экранированием.
func BuildLikePattern(search string) string {
	return "%" + EscapeLike(search) + "%"
}

// BuildInCondition формирует условие IN для среза.
// Возвращает "(?)" + args.
func BuildInCondition(values []uint) (string, []any) {
	if len(values) == 0 {
		return "1=0", nil // no match
	}
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		args[i] = v
	}
	return "(" + strings.Join(placeholders, ",") + ")", args
}
