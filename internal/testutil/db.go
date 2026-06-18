// internal/testutil/db.go
package testutil

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func SetupTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}

	if len(models) > 0 {
		if err := db.AutoMigrate(models...); err != nil {
			t.Fatalf("migration failed: %v", err)
		}
	}

	return db
}