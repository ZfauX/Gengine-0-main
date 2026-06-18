// internal/pkg/assets/fonts/embed.go
package fonts

import _ "embed"

// DejaVuSans содержит байты обычного шрифта.
//
//go:embed DejaVuSans.ttf
var DejaVuSans []byte

// DejaVuSansBold содержит байты жирного шрифта.
//
//go:embed DejaVuSans-Bold.ttf
var DejaVuSansBold []byte