-- 000018_add_missing_columns.up.sql
-- Добавление колонок, отсутствующих в миграциях, но присутствующих в GORM-моделях.
-- Исправляет расхождения, найденные при ревью миграций.

-- ========== email_verification_tokens ==========
-- verification_code используется в service.go (Create) и repository.go (GetTokenByCode)
ALTER TABLE email_verification_tokens ADD COLUMN IF NOT EXISTS verification_code VARCHAR(8) NOT NULL DEFAULT '';
ALTER TABLE email_verification_tokens ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE email_verification_tokens ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;

CREATE UNIQUE INDEX IF NOT EXISTS idx_email_verification_tokens_verification_code
    ON email_verification_tokens(verification_code);
CREATE INDEX IF NOT EXISTS idx_email_verification_tokens_deleted_at
    ON email_verification_tokens(deleted_at);

-- ========== password_reset_tokens ==========
-- updated_at используется GORM autoUpdateTime, deleted_at — для soft delete (DeleteToken)
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_deleted_at
    ON password_reset_tokens(deleted_at);

-- ========== notifications ==========
-- updated_at используется GORM autoUpdateTime, deleted_at — для soft delete
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX IF NOT EXISTS idx_notifications_deleted_at
    ON notifications(deleted_at);
