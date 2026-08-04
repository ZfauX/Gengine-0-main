-- 000026_add_refresh_token_family.down.sql
DROP INDEX IF EXISTS idx_refresh_tokens_family;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS family_id;
