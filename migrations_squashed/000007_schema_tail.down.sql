-- migrations_squashed/000007_schema_tail.down.sql
-- Откат изменений 000037-000059.

-- ========== 000059_chat_personal_rooms.down.sql ==========
-- 000059_chat_personal_rooms.down.sql
ALTER TABLE chat_rooms DROP COLUMN IF EXISTS user1_id;
ALTER TABLE chat_rooms DROP COLUMN IF EXISTS user2_id;

-- ========== 000058_payments.down.sql ==========
-- 000058_payments.down.sql
DROP TABLE IF EXISTS payments;

-- ========== 000057_player_locations.down.sql ==========
-- 000057_player_locations.down.sql
DROP TABLE IF EXISTS player_locations;

-- ========== 000056_notify_game_days.down.sql ==========
-- 000056_notify_game_days.down.sql
ALTER TABLE users DROP COLUMN IF EXISTS notify_game_days;

-- ========== 000055_team_routes_and_answers.down.sql ==========
-- 000055_team_routes_and_answers.down.sql
DROP TABLE IF EXISTS level_team_answers;
DROP TABLE IF EXISTS game_passing_levels;
ALTER TABLE game_passings DROP COLUMN IF EXISTS start_time;
ALTER TABLE attempts DROP COLUMN IF EXISTS user_id;

-- ========== 000054_chat_room_permissions.down.sql ==========
-- 000054_chat_room_permissions.down.sql
DROP TABLE IF EXISTS chat_room_members;
DROP INDEX IF EXISTS idx_chat_rooms_type;
DROP INDEX IF EXISTS idx_chat_rooms_owner;
ALTER TABLE chat_rooms DROP COLUMN IF EXISTS room_type;
ALTER TABLE chat_rooms DROP COLUMN IF EXISTS owner_id;

-- ========== 000053_team_member_roles.down.sql ==========
-- 000053_team_member_roles.down.sql
ALTER TABLE team_members
    DROP COLUMN IF EXISTS role,
    DROP COLUMN IF EXISTS group_type,
    DROP COLUMN IF EXISTS field_role;

-- ========== 000052_coauthor_permissions.down.sql ==========
-- 000052_coauthor_permissions.down.sql
ALTER TABLE co_authors DROP COLUMN IF EXISTS permissions;

-- ========== 000051_add_performance_indexes_13.down.sql ==========
-- 000051_add_performance_indexes_13.down.sql
DROP INDEX IF EXISTS idx_external_logins_provider_external_unique;
DROP INDEX IF EXISTS idx_blackbox_voting_sessions_passing_level;

-- ========== 000050_add_performance_indexes_12.down.sql ==========
-- 000050_add_performance_indexes_12.down.sql
DROP INDEX IF EXISTS idx_level_progresses_unfinished_started;

-- ========== 000049_add_performance_indexes_11.down.sql ==========
-- 000049_add_performance_indexes_11.down.sql
DROP INDEX IF EXISTS idx_teams_name_trgm;

-- ========== 000048_add_performance_indexes_10.down.sql ==========
-- 000048_add_performance_indexes_10.down.sql
DROP INDEX IF EXISTS idx_attempts_progress_is_file_code;
DROP INDEX IF EXISTS idx_attempts_progress_is_file_path;
DROP INDEX IF EXISTS idx_team_members_user_id;
DROP INDEX IF EXISTS idx_games_draft_visibility_created;

-- ========== 000047_add_performance_indexes_9.down.sql ==========
-- 000047_add_performance_indexes_9.down.sql
DROP INDEX IF EXISTS idx_tournament_results_tournament_team;

-- ========== 000046_logs_game_id_denormalization.down.sql ==========
-- 000046_logs_game_id_denormalization.down.sql
DROP INDEX IF EXISTS idx_logs_game_created;
ALTER TABLE logs DROP COLUMN IF EXISTS game_id;

-- ========== 000045_add_performance_indexes_8.down.sql ==========
-- 000045_add_performance_indexes_8.down.sql
DROP INDEX IF EXISTS idx_level_progresses_passing_level;

-- ========== 000044_add_performance_indexes_7.down.sql ==========
-- 000044_add_performance_indexes_7.down.sql
DROP INDEX CONCURRENTLY IF EXISTS idx_notifications_read_at_read;
DROP INDEX CONCURRENTLY IF EXISTS idx_games_visibility_draft_author;

-- ========== 000043_add_performance_indexes_6.down.sql ==========
-- 000043_add_performance_indexes_6.down.sql
DROP INDEX IF EXISTS idx_notifications_user_created;
DROP INDEX IF EXISTS idx_games_author_draft_created;

-- ========== 000042_add_performance_indexes_5.down.sql ==========
-- 000042_add_performance_indexes_5.down.sql
DROP INDEX IF EXISTS idx_notes_game_created;
DROP INDEX IF EXISTS idx_photos_game_created;
DROP INDEX IF EXISTS idx_teams_name;

-- ========== 000041_add_lock_count.down.sql ==========
-- 000041_add_lock_count.down.sql
ALTER TABLE users DROP COLUMN IF EXISTS lock_count;

-- ========== 000040_add_performance_indexes_4.down.sql ==========
-- 000040_add_performance_indexes_4.down.sql
DROP INDEX IF EXISTS idx_audit_logs_user_created;
DROP INDEX IF EXISTS idx_audit_logs_action_created;
DROP INDEX IF EXISTS idx_invitations_user_status_created;

-- ========== 000039_add_performance_indexes_3.down.sql ==========
-- 000039_add_performance_indexes_3.down.sql
DROP INDEX IF EXISTS idx_team_members_team_id;
DROP INDEX IF EXISTS idx_game_passings_team_status;
DROP INDEX IF EXISTS idx_level_progresses_passing_unfinished_created;

-- ========== 000038_add_performance_indexes_2.down.sql ==========
-- 000038_add_performance_indexes_2.down.sql
DROP INDEX IF EXISTS idx_external_logins_provider_external;
DROP INDEX IF EXISTS idx_external_logins_user_id;
DROP INDEX IF EXISTS idx_player_ratings_score;
DROP INDEX IF EXISTS idx_teams_name_trgm;

-- ========== 000037_add_performance_indexes.down.sql ==========
-- 000037_add_performance_indexes.down.sql
DROP INDEX IF EXISTS idx_tournament_results_tournament_score;
DROP INDEX IF EXISTS idx_chat_messages_room_created;
DROP INDEX IF EXISTS idx_level_progresses_unfinished_started;
DROP INDEX IF EXISTS idx_games_draft_visibility_name;
DROP INDEX IF EXISTS idx_games_draft_visibility_starts;
DROP INDEX IF EXISTS idx_reviews_game_created;

