-- 000023_add_missing_indexes.up.sql
-- Индексы для горячих запросов (производительность)

-- Мониторинг: фильтр "последние 5 минут" по attempts.created_at
CREATE INDEX IF NOT EXISTS idx_attempts_created_at ON attempts(created_at);

-- Логи игры: сортировка по created_at внутри прохождения
CREATE INDEX IF NOT EXISTS idx_logs_game_passing_created ON logs(game_passing_id, created_at);

-- Приглашения: фильтр по (user_id, status)
CREATE INDEX IF NOT EXISTS idx_invitations_user_status ON invitations(user_id, status);

-- Поиск пользователей в админке: ILIKE по name и email (pg_trgm)
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_users_name_trgm ON users USING gin(name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_users_email_trgm ON users USING gin(email gin_trgm_ops);

-- Голосование: одна команда = один голос за сессию (дубликаты невозможны на уровне БД)
CREATE UNIQUE INDEX IF NOT EXISTS idx_blackbox_votes_session_voter ON blackbox_votes(session_id, voter_id);
