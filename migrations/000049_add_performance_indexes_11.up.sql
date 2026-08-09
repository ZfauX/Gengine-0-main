-- 000049_add_performance_indexes_11.up.sql
-- Индексы из pass 43 (P-43-5):

-- TeamRepository.SearchPaginated/CountSearch фильтруют teams.name ILIKE
-- (admin-поиск команд); users.name уже имеет pg_trgm (000023), teams.name — нет,
-- поэтому поиск по имени команды делал seq-scan. pg_trgm уже создан в 000023.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_teams_name_trgm
    ON teams USING gin(name gin_trgm_ops);
