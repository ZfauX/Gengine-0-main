// internal/pkg/render/localize.go
package render

import (
	"github.com/gin-gonic/gin"
)

// errKeyMap сопоставляет русские sentinel-ошибки сервисного слоя с i18n-ключами.
// Позволяет локализовать ошибки на границе рендеринга без изменения сервисного слоя.
var errKeyMap = map[string]string{
	// auth / token
	"неверный email или пароль":                         "auth.invalid_credentials",
	"аккаунт заблокирован":                              "auth.account_locked",
	"невалидный токен":                                  "auth.invalid_token",
	"токен истёк":                                       "auth.token_expired",
	"невалидный или отозванный refresh-токен":           "handler.unauthorized",
	"refresh-токен истёк":                               "auth.token_expired",
	"код недействителен или истёк":                      "auth.invalid_token",
	"код истёк":                                         "auth.token_expired",
	"неверный текущий пароль":                           "handler.wrong_password",
	"использование refresh-токена как access запрещено": "handler.forbidden",

	// access / permissions
	"доступ запрещён":                           "handler.forbidden",
	"недостаточно прав":                         "handler.no_rights",
	"только автор может выполнить это действие": "handler.only_author",
	"нет прав": "handler.no_rights",
	"внутренняя ошибка сервера": "handler.internal_error",

	// game
	"игра не найдена":                      "handler.game_not_found",
	"игра не содержит уровней":             "handler.game_empty",
	"игра не активна":                      "handler.game_not_active",
	"игра не запущена":                     "handler.game_not_started",
	"игра уже началась":                    "handler.game_already_started",
	"игра уже опубликована":                "handler.game_already_published",
	"игра уже в турнире":                   "handler.game_in_tournament",
	"нельзя опубликовать игру без уровней": "handler.game_no_levels",
	"нельзя редактировать игру с активными прохождениями": "handler.game_active_passings",
	"некорректный id игры": "handler.invalid_game_id",

	// team
	"команда не найдена":                      "handler.team_not_found",
	"команда уже участвует в турнире":         "handler.team_in_tournament",
	"вы не являетесь участником этой команды": "handler.not_team_member",
	"невозможно удалить капитана":             "handler.cannot_remove_captain",
	"вы не можете принять это приглашение":    "handler.invite_denied",
	"вы не можете отклонить это приглашение":  "handler.invite_denied",

	// level / question / answer
	"уровень не найден":                         "handler.level_not_found",
	"вопрос не найден":                          "handler.question_not_found",
	"ответ не найден":                           "handler.answer_not_found",
	"должен остаться хотя бы один вариант кода": "handler.must_have_answer",
	"некуда двигать":                            "handler.no_move",
	"неверное направление":                      "handler.invalid_direction",
	"неверный тип уровня":                       "handler.invalid_level_type",

	// gameplay
	"нет активного уровня":                              "handler.no_active_level",
	"лимит подсказок исчерпан":                          "handler.no_hints_left",
	"игра завершена":                                    "handler.game_finished",
	"завершённый уровень не найден":                     "handler.finished_level_not_found",
	"на этом уровне ожидается файл, а не текстовый код": "handler.file_expected",

	// voting
	"голосование уже активно":        "handler.voting_active",
	"голосование уже было проведено": "handler.voting_completed",
	"голосование закрыто":            "handler.voting_closed",
	"ваш голос уже учтён":            "handler.vote_already_cast",
	"недопустимый вариант ответа":    "handler.invalid_option",

	// team application / passing
	"заявка уже подана":                    "handler.application_exists",
	"достигнут лимит команд на игру":       "handler.team_limit_reached",
	"игра ещё не принята или уже началась": "handler.game_not_accepted",
	"команда не в игре или уже завершила":  "handler.team_not_in_game",

	// review
	"вы не можете оставить отзыв":                 "handler.cannot_review",
	"комментарий не может превышать 500 символов": "handler.comment_too_long",

	// follow
	"нельзя подписаться на самого себя": "handler.cannot_follow_self",

	// misc
	"достигнут лимит": "handler.rate_limited",
}

// LocalizeError переводит сообщение ошибки на язык текущего запроса.
// Если ошибка известна — возвращает локализованное сообщение,
// иначе возвращает оригинальное сообщение (fallback).
func LocalizeError(c *gin.Context, errMsg string) string {
	if key, ok := errKeyMap[errMsg]; ok {
		return Tr(c, key)
	}
	return errMsg
}

// LocalizeErr переводит error в локализованное сообщение.
func LocalizeErr(c *gin.Context, err error) string {
	if err == nil {
		return ""
	}
	return LocalizeError(c, err.Error())
}
