-- 000048_add_performance_indexes_10.up.sql
-- Индексы из pass 42 (P-04):

-- Vote (monitor/service.go) ищет попытки по (level_progress_id, is_file, code)
-- / (level_progress_id, is_file, file_path) для проверки варианта ответа.
-- Существующий idx_attempts_level_progress_id даёт residual-фильтр по code/
-- file_path на каждой строке прогресса уровня; составной индекс покрывает
-- выборку. (Композит blackbox_votes(session_id, voter_id) уже есть в 000023.)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_attempts_progress_is_file_code
    ON attempts(level_progress_id, is_file, code);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_attempts_progress_is_file_path
    ON attempts(level_progress_id, is_file, file_path);

-- P-06 (pass 42): GetPassingByUser JOIN team_members ON team_id WHERE
-- team_members.user_id = ? (горячий gameplay-экран). Существующий
-- idx_team_members_team_id заставляет сканировать всех членов совпавших команд.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_team_members_user_id
    ON team_members(user_id);

-- P-05 (pass 42): ListFilteredPaginated по умолчанию сортирует
-- ORDER BY games.created_at DESC; существующие idx_games_draft_visibility_name/
-- starts_at не покрывают дефолтный порядок → полная сортировка множества.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_games_draft_visibility_created
    ON games(is_draft, visibility, created_at DESC);
