-- 000024_add_games_view.up.sql
-- Персональное предпочтение: вид списка игр (table | cards) на сервере,
-- вместо localStorage на клиенте (стратегическая персонализация).
ALTER TABLE users ADD COLUMN IF NOT EXISTS games_view varchar(10) NOT NULL DEFAULT 'table';
