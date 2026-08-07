// internal/pkg/i18n/i18n_keys_test.go
// D5: автотест, что каждый i18n-ключ, используемый в шаблонах и Go-коде,
// существует в обоих словарях (ru/en). Тест TestAllKeysHaveEN покрывает
// только словарь↔словарь и не ловит пропущенные ключи в шаблонах (F3).
package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot возвращает корень репозитория (на два уровня выше internal/pkg/i18n).
func repoRoot() string {
	dir, _ := os.Getwd()
	return filepath.Join(dir, "..", "..", "..")
}

var (
	// templateKeyRE ловит {{ T $.Lang "key" }} и {{ TF $.Lang "key" ... }}.
	templateKeyRE = regexp.MustCompile(`\bT(?:F)?\s+\$\.Lang\s+"([a-z0-9_.]+)"`)
	// goKeyRE ловит i18n.T("key"), i18n.TF("key"), render.Tr(c, "key"), render.TF(c, "key").
	goKeyRE = regexp.MustCompile(`\b(?:i18n\.T(?:F)?|render\.Tr|render\.TF)\([^)]*"([a-z0-9_.]+)"`)
)

// collectTemplateKeys сканирует все HTML-шаблоны доменов.
func collectTemplateKeys(t *testing.T) map[string]bool {
	t.Helper()
	root := repoRoot()
	pattern := filepath.Join(root, "internal", "domain", "*", "templates", "*.html")
	files, err := filepath.Glob(pattern)
	require.NoError(t, err)
	require.NotEmpty(t, files, "не найдены шаблоны по %s", pattern)

	keys := map[string]bool{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range templateKeyRE.FindAllStringSubmatch(string(data), -1) {
			keys[m[1]] = true
		}
	}
	return keys
}

// collectGoKeys сканирует Go-код (без самого пакета i18n).
func collectGoKeys(t *testing.T) map[string]bool {
	t.Helper()
	root := repoRoot()
	keys := map[string]bool{}
	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Пропускаем сам пакет i18n — там ключи определяются, а не используются.
		if strings.Contains(path, string(filepath.Separator)+"i18n"+string(filepath.Separator)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range goKeyRE.FindAllStringSubmatch(string(data), -1) {
			keys[m[1]] = true
		}
		return nil
	})
	require.NoError(t, err)
	return keys
}

func TestAllUsedKeysExistInBothDictionaries(t *testing.T) {
	templateKeys := collectTemplateKeys(t)
	goKeys := collectGoKeys(t)

	assert.NotEmpty(t, templateKeys, "должны быть найдены ключи в шаблонах")
	assert.NotEmpty(t, goKeys, "должны быть найдены ключи в Go-коде")

	// Проверяем оба источника: ключ должен быть и в ru, и в en.
	check := func(keys map[string]bool, source string) {
		for key := range keys {
			_, okRU := ruMessages[key]
			_, okEN := enMessages[key]
			assert.True(t, okRU, "RU message missing for key used in %s: %s", source, key)
			assert.True(t, okEN, "EN message missing for key used in %s: %s", source, key)
		}
	}
	check(templateKeys, "templates")
	check(goKeys, "Go code")
}

func TestSampleTemplateKeysExist(t *testing.T) {
	// Точки-контроль ключей, которые реально используются в шаблонах.
	for _, key := range []string{
		"coauthor.delete_confirm", // F3 — был пропущен ранее
		"calendar.month_jan",
		"monitor.page_title",
		"logs.page",
		"gamechat.title",
		"game.show_delete_undo",
	} {
		assert.Contains(t, ruMessages, key, "RU key missing: %s", key)
		assert.Contains(t, enMessages, key, "EN key missing: %s", key)
	}
}
