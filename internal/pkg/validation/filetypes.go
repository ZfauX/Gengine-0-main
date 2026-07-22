// internal/pkg/validation/filetypes.go
package validation

import "strings"

// AllowedImageTypes — стандартный набор MIME-типов для изображений.
var AllowedImageTypes = []string{"image/jpeg", "image/png", "image/webp"}

// AllowedUploadTypes — стандартный набор MIME-типов для загрузки файлов ответов.
var AllowedUploadTypes = []string{"image/jpeg", "image/png", "image/gif", "application/pdf", "text/plain"}

// IsAllowedType проверяет, разрешён ли MIME-тип в списке allowed.
func IsAllowedType(mimeType string, allowed []string) bool {
	for _, t := range allowed {
		if strings.EqualFold(mimeType, t) {
			return true
		}
	}
	return false
}
