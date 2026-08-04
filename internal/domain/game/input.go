// internal/domain/game/handler_types.go
package game

import "mime/multipart"

type CreateGameInput struct {
	Name                 string                `form:"name" binding:"required,min=3,max=100"`
	Description          string                `form:"description" binding:"max=2000"`
	MaxTeamNumber        int                   `form:"max_team_number" binding:"required,min=1,max=100"`
	Visibility           string                `form:"visibility" binding:"required,oneof=public private"`
	StartsAt             string                `form:"starts_at"`
	RegistrationDeadline string                `form:"registration_deadline"`
	IsDraft              bool                  `form:"is_draft"`
	CoverFile            *multipart.FileHeader `form:"cover"`
}

type UpdateGameInput struct {
	Name                 string                `form:"name" binding:"required,min=3,max=100"`
	Description          string                `form:"description" binding:"max=2000"`
	MaxTeamNumber        int                   `form:"max_team_number" binding:"required,min=1,max=100"`
	Visibility           string                `form:"visibility" binding:"required,oneof=public private"`
	StartsAt             string                `form:"starts_at"`
	RegistrationDeadline string                `form:"registration_deadline"`
	IsDraft              bool                  `form:"is_draft"`
	CoverFile            *multipart.FileHeader `form:"cover"`
	DeleteCover          bool                  `form:"delete_cover"`
}

type ApplyInput struct {
	TeamID uint `form:"team_id" binding:"required,gt=0"`
}

type DisqualifyInput struct {
	TeamID uint `form:"team_id" binding:"required,gt=0"`
}

type AddCoAuthorInput struct {
	UserID uint `form:"user_id" binding:"required,gt=0"`
}
