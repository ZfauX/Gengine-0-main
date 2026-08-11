-- migrations_squashed/000009_schema_tail.down.sql

-- ========== 000062_drop_password_reset_token_hash.down.sql ==========
-- 000062_drop_password_reset_token_hash.down.sql
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS token_hash varchar(64) NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_password_reset_token_hash ON password_reset_tokens(token_hash);

