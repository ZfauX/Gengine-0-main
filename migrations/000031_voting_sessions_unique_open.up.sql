-- 000031_voting_sessions_unique_open.up.sql
-- Частичный уникальный индекс: не может быть двух ОТКРЫТЫХ сессий голосования
-- для одного (прохождение, уровень). Закрытые сессии могут накапливаться.
-- Закрывает check-then-insert гонку в StartVoting (B8).
CREATE UNIQUE INDEX IF NOT EXISTS idx_blackbox_voting_sessions_open
    ON blackbox_voting_sessions(game_passing_id, level_id)
    WHERE is_open = true;
