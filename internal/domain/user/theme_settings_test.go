// internal/domain/user/theme_settings_test.go
package user

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestThemeSettings_Defaults проверяет значения по умолчанию и парсинг пустой строки.
func TestThemeSettings_Defaults(t *testing.T) {
	def := DefaultThemeSettings()
	assert.True(t, def.AutoTheme)
	assert.Equal(t, "20:00", def.DarkFrom)
	assert.Equal(t, "07:00", def.DarkTo)

	ts, err := ParseThemeSettings("")
	require.NoError(t, err)
	assert.Equal(t, def, ts)
}

// TestThemeSettings_RoundTrip проверяет сериализацию/десериализацию JSON.
func TestThemeSettings_RoundTrip(t *testing.T) {
	ts := ThemeSettings{AutoTheme: false, DarkFrom: "22:00", DarkTo: "06:00"}
	jsonStr, err := MarshalThemeSettings(ts)
	require.NoError(t, err)

	parsed, err := ParseThemeSettings(jsonStr)
	require.NoError(t, err)
	assert.Equal(t, ts, parsed)
}

// TestThemeSettings_ServiceSaveLoad проверяет сохранение и чтение из БД.
func TestThemeSettings_ServiceSaveLoad(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	u := &User{Email: "theme@test.com", Password: "pass", Name: "Theme", Role: "user"}
	require.NoError(t, db.Create(u).Error)

	svc := NewProfileService(db)

	// По умолчанию — дефолты
	ts, err := svc.GetThemeSettings(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, ts.AutoTheme)

	// Сохраняем кастомные
	custom := ThemeSettings{AutoTheme: true, DarkFrom: "21:30", DarkTo: "08:00"}
	require.NoError(t, svc.SaveThemeSettings(ctx, u.ID, custom))

	// Читаем — должно совпасть
	got, err := svc.GetThemeSettings(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, custom, got)
}

// TestThemeSettings_ServiceCustomDisabled проверяет выключенную автосмену.
func TestThemeSettings_ServiceCustomDisabled(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	u := &User{Email: "theme2@test.com", Password: "pass", Name: "Theme2", Role: "user"}
	require.NoError(t, db.Create(u).Error)

	svc := NewProfileService(db)
	require.NoError(t, svc.SaveThemeSettings(ctx, u.ID, ThemeSettings{AutoTheme: false}))

	got, err := svc.GetThemeSettings(ctx, u.ID)
	require.NoError(t, err)
	assert.False(t, got.AutoTheme)
	assert.Equal(t, "", got.DarkFrom)
	assert.Equal(t, "", got.DarkTo)
}
