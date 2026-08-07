-- 000037_add_performance_indexes.down.sql
DROP INDEX IF EXISTS idx_tournament_results_tournament_score;
DROP INDEX IF EXISTS idx_chat_messages_room_created;
DROP INDEX IF EXISTS idx_level_progresses_unfinished_started;
DROP INDEX IF EXISTS idx_games_draft_visibility_name;
DROP INDEX IF EXISTS idx_games_draft_visibility_starts;
DROP INDEX IF EXISTS idx_reviews_game_created;
