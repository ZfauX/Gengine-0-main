-- 000057_player_locations.up.sql
-- G-1..G-4 (pass 45): позиции игроков (водителей) во время игры.
CREATE TABLE IF NOT EXISTS player_locations (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    game_passing_id BIGINT NOT NULL REFERENCES game_passings(id) ON DELETE CASCADE,
    team_id BIGINT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    accuracy DOUBLE PRECISION NOT NULL DEFAULT 0,
    UNIQUE(game_passing_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_player_loc_team ON player_locations(team_id);
