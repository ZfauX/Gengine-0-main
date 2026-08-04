-- 000026_add_refresh_token_family.up.sql
-- Семьи refresh-токенов: при reuse (повторном использовании уже отозванного
-- токена) отзывается вся семья — защита от кражи сессии.
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS family_id varchar(64) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_family ON refresh_tokens(family_id);
