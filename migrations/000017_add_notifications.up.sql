CREATE TABLE IF NOT EXISTS notifications (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id BIGINT NOT NULL REFERENCES users(id),
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    link TEXT NOT NULL DEFAULT '',
    read BOOLEAN NOT NULL DEFAULT false,
    read_at TIMESTAMPTZ,
    game_id BIGINT,
    team_id BIGINT
);
CREATE INDEX IF NOT EXISTS idx_notifications_user_read ON notifications(user_id, read);
