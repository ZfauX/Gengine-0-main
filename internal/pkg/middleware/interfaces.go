package middleware

// TokenParser реализуется сервисом, умеющим проверять JWT.
// Уже объявлен в auth.go, поэтому здесь не дублируем.

// GameAuthorizer проверяет, является ли пользователь автором игры.
type GameAuthorizer interface {
	IsUserManager(gameID, userID uint) (bool, error)
}

// TeamAccessChecker проверяет права пользователя на управление командой.
type TeamAccessChecker interface {
	CanManageTeam(teamID, userID uint) bool
}