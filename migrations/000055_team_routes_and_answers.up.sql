-- 000055_team_routes_and_answers.up.sql
-- Фаза 3 (Эпик C): маршруты команд, индивидуальный старт, разные ответы.

-- C-3: индивидуальное время старта команды (NULL = общее время игры).
ALTER TABLE game_passings
    ADD COLUMN IF NOT EXISTS start_time TIMESTAMP WITH TIME ZONE;

-- C-1/C-2: маршрут команды — порядок прохождения уровней.
-- game_passing_levels(game_passing_id, level_id, order_index).
CREATE TABLE IF NOT EXISTS game_passing_levels (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    game_passing_id INTEGER NOT NULL REFERENCES game_passings(id) ON DELETE CASCADE,
    level_id INTEGER NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    order_index INTEGER NOT NULL DEFAULT 0,
    UNIQUE(game_passing_id, level_id)
);
CREATE INDEX IF NOT EXISTS idx_gpl_passing_order ON game_passing_levels(game_passing_id, order_index);

-- C-4: разные ответы на уровень для конкретной команды.
-- level_team_answers(level_id, team_id, code, hint) — если запись есть,
-- для команды ожидается именно этот код.
CREATE TABLE IF NOT EXISTS level_team_answers (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    level_id INTEGER NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    code TEXT NOT NULL,
    hint TEXT NOT NULL DEFAULT '',
    UNIQUE(level_id, team_id)
);
CREATE INDEX IF NOT EXISTS idx_lta_team ON level_team_answers(team_id);

-- C-5: кто именно в команде отправил код (для итогов «на человека»).
ALTER TABLE attempts
    ADD COLUMN IF NOT EXISTS user_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_attempts_user ON attempts(user_id);
