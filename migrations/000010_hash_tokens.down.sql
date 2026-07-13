-- Откат: возвращаем старое название колонки.
-- Существующие хеши не могут быть преобразованы обратно в токены.

ALTER TABLE password_reset_tokens RENAME COLUMN token_hash TO token;
ALTER TABLE email_verification_tokens RENAME COLUMN token_hash TO token;
