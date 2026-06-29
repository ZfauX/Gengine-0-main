-- Удаление индексов (для отката)

DROP INDEX IF EXISTS idx_users_email;
DROP INDEX IF EXISTS idx_users_role;
DROP INDEX IF EXISTS idx_users_deleted_at;

DROP INDEX IF EXISTS idx_games_author_id;
DROP INDEX IF EXISTS idx_games_is_draft;
DROP INDEX IF EXISTS idx_games_visibility;
DROP INDEX IF EXISTS idx_games_starts_at;
DROP INDEX IF EXISTS idx_games_registration_deadline;
DROP INDEX IF EXISTS idx_games_deleted_at;

DROP INDEX IF EXISTS idx_levels_game_id;
DROP INDEX IF EXISTS idx_levels_parent_id;
DROP INDEX IF EXISTS idx_levels_group_id;
DROP INDEX IF EXISTS idx_levels_deleted_at;

DROP INDEX IF EXISTS idx_questions_level_id;
DROP INDEX IF EXISTS idx_answers_question_id;

DROP INDEX IF EXISTS idx_teams_captain_id;
DROP INDEX IF EXISTS idx_team_members_user_id;

DROP INDEX IF EXISTS idx_invitations_team_id;
DROP INDEX IF EXISTS idx_invitations_user_id;
DROP INDEX IF EXISTS idx_invitations_status;

DROP INDEX IF EXISTS idx_game_passings_game_id;
DROP INDEX IF EXISTS idx_game_passings_team_id;
DROP INDEX IF EXISTS idx_game_passings_status;
DROP INDEX IF EXISTS idx_game_passings_deleted_at;

DROP INDEX IF EXISTS idx_level_progresses_game_passing_id;
DROP INDEX IF EXISTS idx_level_progresses_level_id;
DROP INDEX IF EXISTS idx_level_progresses_deleted_at;

DROP INDEX IF EXISTS idx_attempts_level_progress_id;

DROP INDEX IF EXISTS idx_co_authors_game_id;
DROP INDEX IF EXISTS idx_co_authors_user_id;

DROP INDEX IF EXISTS idx_notes_game_id;
DROP INDEX IF EXISTS idx_notes_user_id;

DROP INDEX IF EXISTS idx_reviews_game_id;
DROP INDEX IF EXISTS idx_reviews_user_id;

DROP INDEX IF EXISTS idx_photos_game_id;
DROP INDEX IF EXISTS idx_photos_user_id;

DROP INDEX IF EXISTS idx_follows_follower_id;
DROP INDEX IF EXISTS idx_follows_author_id;

DROP INDEX IF EXISTS idx_chat_rooms_game_id;
DROP INDEX IF EXISTS idx_chat_rooms_team_id;
DROP INDEX IF EXISTS idx_chat_rooms_passing_id;

DROP INDEX IF EXISTS idx_chat_messages_room_id;
DROP INDEX IF EXISTS idx_chat_messages_user_id;
DROP INDEX IF EXISTS idx_chat_messages_created_at;

DROP INDEX IF EXISTS idx_blackbox_voting_sessions_game_passing_id;
DROP INDEX IF EXISTS idx_blackbox_voting_sessions_level_id;

DROP INDEX IF EXISTS idx_blackbox_votes_session_id;
DROP INDEX IF EXISTS idx_blackbox_votes_voter_id;

DROP INDEX IF EXISTS idx_logs_game_passing_id;
DROP INDEX IF EXISTS idx_logs_level_id;

DROP INDEX IF EXISTS idx_tournaments_author_id;
DROP INDEX IF EXISTS idx_tournaments_deleted_at;

DROP INDEX IF EXISTS idx_tournament_games_tournament_id;
DROP INDEX IF EXISTS idx_tournament_games_game_id;

DROP INDEX IF EXISTS idx_tournament_teams_tournament_id;
DROP INDEX IF EXISTS idx_tournament_teams_team_id;

DROP INDEX IF EXISTS idx_tournament_results_tournament_id;
DROP INDEX IF EXISTS idx_tournament_results_team_id;

DROP INDEX IF EXISTS idx_audit_logs_user_id;
DROP INDEX IF EXISTS idx_audit_logs_action;
DROP INDEX IF EXISTS idx_audit_logs_object_type;
DROP INDEX IF EXISTS idx_audit_logs_created_at;