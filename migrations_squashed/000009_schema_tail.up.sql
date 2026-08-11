-- migrations_squashed/000009_schema_tail.up.sql
-- Полный слепок изменений 000062.

-- ========== 000062_drop_password_reset_token_hash.up.sql ==========
-- 000062_drop_password_reset_token_hash.up.sql
-- DEEP-REVIEW LOW #27 (pass 46): PasswordResetToken.TokenHash не использовался —
-- в письме шёл ResetCode, а TokenHash хранил хэш неиспользуемого rawToken.
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS token_hash;

