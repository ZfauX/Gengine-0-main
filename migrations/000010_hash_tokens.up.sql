-- Переименовываем колонку token в token_hash и хешируем существующие токены.
-- Это необходимо для безопасности: токены больше не хранятся в открытом виде.

ALTER TABLE password_reset_tokens RENAME COLUMN token TO token_hash;
ALTER TABLE email_verification_tokens RENAME COLUMN token TO token_hash;
