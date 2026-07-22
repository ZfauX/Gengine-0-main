package sqlutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEscapeLike_NoSpecial(t *testing.T) {
	assert.Equal(t, "hello", EscapeLike("hello"))
}

func TestEscapeLike_Percent(t *testing.T) {
	assert.Equal(t, `\%`, EscapeLike("%"))
}

func TestEscapeLike_Underscore(t *testing.T) {
	assert.Equal(t, `\_`, EscapeLike("_"))
}

func TestEscapeLike_Mixed(t *testing.T) {
	assert.Equal(t, `test\%\_value`, EscapeLike("test%_value"))
}

func TestEscapeLike_Empty(t *testing.T) {
	assert.Equal(t, "", EscapeLike(""))
}

func TestBuildLikePattern(t *testing.T) {
	assert.Equal(t, "%test%", BuildLikePattern("test"))
}

func TestBuildLikePattern_WithSpecial(t *testing.T) {
	assert.Equal(t, `%test\_pattern%`, BuildLikePattern("test_pattern"))
}

func TestBuildInCondition_Empty(t *testing.T) {
	cond, args := BuildInCondition([]uint{})
	assert.Equal(t, "1=0", cond)
	assert.Nil(t, args)
}

func TestBuildInCondition_Single(t *testing.T) {
	cond, args := BuildInCondition([]uint{42})
	assert.Equal(t, "(?)", cond)
	assert.Equal(t, []any{uint(42)}, args)
}

func TestBuildInCondition_Multiple(t *testing.T) {
	cond, args := BuildInCondition([]uint{1, 2, 3})
	assert.Equal(t, "(?,?,?)", cond)
	assert.Equal(t, []any{uint(1), uint(2), uint(3)}, args)
}

func TestNewPaginatedQuery_Defaults(t *testing.T) {
	b := NewPaginatedQuery("SELECT * FROM users")
	q, args := b.Build()
	assert.Contains(t, q, "ORDER BY id DESC")
	assert.Empty(t, args)
}

func TestPaginatedQueryBuilder_AddWhere(t *testing.T) {
	b := NewPaginatedQuery("SELECT * FROM users")
	b.AddWhere("name = ?", "john")
	q, args := b.Build()
	assert.Contains(t, q, "WHERE name = ?")
	assert.Equal(t, []any{"john"}, args)
}

func TestPaginatedQueryBuilder_MultipleWhere(t *testing.T) {
	b := NewPaginatedQuery("SELECT * FROM users")
	b.AddWhere("name = ?", "john")
	b.AddWhere("age > ?", 18)
	q, args := b.Build()
	assert.Contains(t, q, "WHERE name = ? AND age > ?")
	assert.Equal(t, []any{"john", 18}, args)
}

func TestPaginatedQueryBuilder_AddOrder(t *testing.T) {
	b := NewPaginatedQuery("SELECT * FROM users")
	b.AddOrder("created_at ASC")
	q, _ := b.Build()
	assert.Contains(t, q, "ORDER BY created_at ASC")
}

func TestPaginatedQueryBuilder_SetPagination(t *testing.T) {
	b := NewPaginatedQuery("SELECT * FROM users")
	b.SetPagination(2, 10)
	q, args := b.Build()
	assert.Contains(t, q, "LIMIT ? OFFSET ?")
	assert.Equal(t, []any{10, 10}, args) // offset = (2-1)*10 = 10
}

func TestPaginatedQueryBuilder_SetPagination_DefaultPage(t *testing.T) {
	b := NewPaginatedQuery("SELECT * FROM users")
	b.SetPagination(0, 10)
	_, args := b.Build()
	assert.Equal(t, []any{10, 0}, args) // page defaults to 1, offset = 0
}

func TestPaginatedQueryBuilder_SetPagination_MaxPerPage(t *testing.T) {
	b := NewPaginatedQuery("SELECT * FROM users")
	b.SetPagination(1, 200)
	_, args := b.Build()
	assert.Equal(t, []any{100, 0}, args) // perPage capped at 100
}

func TestPaginatedQueryBuilder_DefaultPerPage(t *testing.T) {
	b := NewPaginatedQuery("SELECT * FROM users")
	b.SetPagination(1, 0)
	_, args := b.Build()
	assert.Equal(t, []any{20, 0}, args) // perPage defaults to 20
}

func TestPaginatedQueryBuilder_FullBuild(t *testing.T) {
	b := NewPaginatedQuery("SELECT * FROM users")
	b.AddWhere("status = ?", "active")
	b.AddOrder("name ASC")
	b.SetPagination(3, 25)
	q, args := b.Build()

	assert.Contains(t, q, "SELECT sub.*, COUNT(*) OVER() AS total_count FROM (")
	assert.Contains(t, q, "SELECT * FROM users")
	assert.Contains(t, q, "WHERE status = ?")
	assert.Contains(t, q, "ORDER BY name ASC")
	assert.Contains(t, q, "LIMIT ? OFFSET ?")
	assert.Equal(t, []any{"active", 25, 50}, args) // offset = (3-1)*25 = 50
}
