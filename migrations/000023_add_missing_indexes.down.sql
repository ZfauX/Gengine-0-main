-- 000023_add_missing_indexes.down.sql
DROP INDEX IF EXISTS idx_blackbox_votes_session_voter;
DROP INDEX IF EXISTS idx_users_email_trgm;
DROP INDEX IF EXISTS idx_users_name_trgm;
DROP INDEX IF EXISTS idx_invitations_user_status;
DROP INDEX IF EXISTS idx_logs_game_passing_created;
DROP INDEX IF EXISTS idx_attempts_created_at;
