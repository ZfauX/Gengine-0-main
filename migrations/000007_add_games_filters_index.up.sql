-- 000007_add_games_filters_index.up.sql
-- Добавление индексов для оптимизации фильтрации и сортировки игр

-- Индекс для фильтрации по is_draft и visibility (частый запрос в ListFilteredPaginated)
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility ON games (is_draft, visibility);

-- Индекс для сортировки по rating (через подзапрос или отдельную таблицу)
CREATE INDEX IF NOT EXISTS idx_reviews_game_id ON reviews (game_id);

-- Индекс для сортировки по participants
CREATE INDEX IF NOT EXISTS idx_game_passings_game_status ON game_passings (game_id, status);
