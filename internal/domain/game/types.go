// internal/domain/game/types.go
package game

// SubmitCodeInput – ввод кода.
type SubmitCodeInput struct {
	Code string `form:"code" binding:"required"`
}

// SubmitTestCodeInput – ввод кода в тестовом режиме.
type SubmitTestCodeInput struct {
	Code string `form:"code" binding:"required"`
}
