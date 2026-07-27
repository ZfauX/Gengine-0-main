-- 000005_schema.down.sql
-- Откат сводной миграции (в обратном порядке: 000018 → 000005)

-- ========== 000018: Недостающие колонки ==========
-- notifications
DROP INDEX IF EXISTS idx_notifications_deleted_at;
ALTER TABLE notifications DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE notifications DROP COLUMN IF EXISTS updated_at;

-- password_reset_tokens
DROP INDEX IF EXISTS idx_password_reset_tokens_deleted_at;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS updated_at;

-- email_verification_tokens
DROP INDEX IF EXISTS idx_email_verification_tokens_verification_code;
DROP INDEX IF EXISTS idx_email_verification_tokens_deleted_at;
ALTER TABLE email_verification_tokens DROP COLUMN IF EXISTS verification_code;
ALTER TABLE email_verification_tokens DROP COLUMN IF EXISTS updated_at;
ALTER TABLE email_verification_tokens DROP COLUMN IF EXISTS deleted_at;

-- ========== 000017: Уведомления ==========
DROP TABLE IF EXISTS notifications;

-- ========== 000016: WebAuthn ==========
DROP TABLE IF EXISTS webauthn_credentials;

-- ========== 000015: Поля сброса пароля ==========
DROP INDEX IF EXISTS idx_password_reset_tokens_used_at;
DROP INDEX IF EXISTS idx_password_reset_tokens_reset_code;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS used_at;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS reset_code;

-- ========== 000014: Блокировка при неудачных попытках входа ==========
DROP INDEX IF EXISTS idx_users_locked_until;
ALTER TABLE users DROP COLUMN IF EXISTS locked_until;
ALTER TABLE users DROP COLUMN IF EXISTS failed_login_attempts;

-- ========== 000013: pg_trgm расширение ==========
DROP INDEX IF EXISTS idx_games_name_trgm;
-- Расширение pg_trgm не удаляем, так как оно может использоваться в других местах
-- DROP EXTENSION IF EXISTS pg_trgm;

-- ========== 000012: Индекс user_achievements ==========
DROP INDEX IF EXISTS idx_user_achievements_user_id;

-- ========== 000011: Полнотекстовый поиск ==========
DROP TRIGGER IF EXISTS trg_games_search_vector ON games;
DROP FUNCTION IF EXISTS games_search_vector_update();
DROP INDEX IF EXISTS idx_games_search_vector;
ALTER TABLE games DROP COLUMN IF EXISTS search_vector;

-- ========== 000010: Хеширование токенов ==========
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'password_reset_tokens' AND column_name = 'token') THEN
        ALTER TABLE password_reset_tokens DROP COLUMN token;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'email_verification_tokens' AND column_name = 'token') THEN
        ALTER TABLE email_verification_tokens DROP COLUMN token;
    END IF;
END $$;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS token_hash;
ALTER TABLE email_verification_tokens DROP COLUMN IF EXISTS token_hash;

-- ========== 000009: 2FA ==========
ALTER TABLE users DROP COLUMN IF EXISTS two_factor_backup_codes;
ALTER TABLE users DROP COLUMN IF EXISTS two_factor_secret;
ALTER TABLE users DROP COLUMN IF EXISTS two_factor_enabled;

-- ========== 000008: Индексы производительности ==========
DROP INDEX IF EXISTS idx_game_passings_game_status;
DROP INDEX IF EXISTS idx_attempts_level_progress_created;
DROP INDEX IF EXISTS idx_games_draft_visibility_created;
DROP INDEX IF EXISTS idx_level_progresses_game_passing_finished;

-- ========== 000007: Индексы фильтрации игр ==========
DROP INDEX IF EXISTS idx_game_passings_game_status;
DROP INDEX IF EXISTS idx_reviews_game_id;
DROP INDEX IF EXISTS idx_games_draft_visibility;

-- ========== 000006: Таблица настроек уведомлений ==========
DROP INDEX IF EXISTS idx_notification_settings_user_id;
DROP TABLE IF EXISTS notification_settings;

-- ========== 000005: Индекс автора и статуса ==========
DROP INDEX IF EXISTS idx_games_author_status;
