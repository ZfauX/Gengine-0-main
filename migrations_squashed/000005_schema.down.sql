-- 000005_schema.down.sql
-- Р С›РЎвЂљР С”Р В°РЎвЂљ РЎРѓР Р†Р С•Р Т‘Р Р…Р С•Р в„– Р СР С‘Р С–РЎР‚Р В°РЎвЂ Р С‘Р С‘ (Р Р† Р С•Р В±РЎР‚Р В°РЎвЂљР Р…Р С•Р С Р С—Р С•РЎР‚РЎРЏР Т‘Р С”Р Вµ: 000020 РІвЂ вЂ™ 000005)

-- ========== 000023: missing performance indexes ==========
DROP INDEX IF EXISTS idx_users_email_trgm;
DROP INDEX IF EXISTS idx_users_name_trgm;
DROP INDEX IF EXISTS idx_invitations_user_status;
DROP INDEX IF EXISTS idx_logs_game_passing_created;
DROP INDEX IF EXISTS idx_attempts_created_at;

-- ========== 000022: theme settings ==========
ALTER TABLE users DROP COLUMN IF EXISTS theme_settings;

-- ========== 000020: push_subscriptions, email_queues, device_id index ==========
DROP INDEX IF EXISTS idx_refresh_tokens_device;
ALTER TABLE email_queues ADD COLUMN IF NOT EXISTS to_address TEXT;
UPDATE email_queues SET to_address = recipient WHERE to_address IS NULL AND recipient IS NOT NULL;
ALTER TABLE email_queues DROP COLUMN IF EXISTS recipient;
DROP TABLE IF EXISTS push_subscriptions;

-- ========== 000019: refresh_tokens Р Р…Р ВµР Т‘Р С•РЎРѓРЎвЂљР В°РЎР‹РЎвЂ°Р С‘Р Вµ Р С”Р С•Р В»Р С•Р Р…Р С”Р С‘ ==========
DROP INDEX IF EXISTS idx_refresh_tokens_deleted_at;
DROP INDEX IF EXISTS idx_refresh_tokens_updated_at;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS updated_at;

-- ========== 000018: Р СњР ВµР Т‘Р С•РЎРѓРЎвЂљР В°РЎР‹РЎвЂ°Р С‘Р Вµ Р С”Р С•Р В»Р С•Р Р…Р С”Р С‘ ==========
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

-- ========== 000017: Р Р€Р Р†Р ВµР Т‘Р С•Р СР В»Р ВµР Р…Р С‘РЎРЏ ==========
DROP TABLE IF EXISTS notifications;

-- ========== 000016: WebAuthn ==========
DROP TABLE IF EXISTS webauthn_credentials;

-- ========== 000015: Р СџР С•Р В»РЎРЏ РЎРѓР В±РЎР‚Р С•РЎРѓР В° Р С—Р В°РЎР‚Р С•Р В»РЎРЏ ==========
DROP INDEX IF EXISTS idx_password_reset_tokens_used_at;
DROP INDEX IF EXISTS idx_password_reset_tokens_reset_code;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS used_at;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS reset_code;

-- ========== 000014: Р вЂР В»Р С•Р С”Р С‘РЎР‚Р С•Р Р†Р С”Р В° Р С—РЎР‚Р С‘ Р Р…Р ВµРЎС“Р Т‘Р В°РЎвЂЎР Р…РЎвЂ№РЎвЂ¦ Р С—Р С•Р С—РЎвЂ№РЎвЂљР С”Р В°РЎвЂ¦ Р Р†РЎвЂ¦Р С•Р Т‘Р В° ==========
DROP INDEX IF EXISTS idx_users_locked_until;
ALTER TABLE users DROP COLUMN IF EXISTS locked_until;
ALTER TABLE users DROP COLUMN IF EXISTS failed_login_attempts;

-- ========== 000013: pg_trgm РЎР‚Р В°РЎРѓРЎв‚¬Р С‘РЎР‚Р ВµР Р…Р С‘Р Вµ ==========
DROP INDEX IF EXISTS idx_games_name_trgm;
-- Р В Р В°РЎРѓРЎв‚¬Р С‘РЎР‚Р ВµР Р…Р С‘Р Вµ pg_trgm Р Р…Р Вµ РЎС“Р Т‘Р В°Р В»РЎРЏР ВµР С, РЎвЂљР В°Р С” Р С”Р В°Р С” Р С•Р Р…Р С• Р СР С•Р В¶Р ВµРЎвЂљ Р С‘РЎРѓР С—Р С•Р В»РЎРЉР В·Р С•Р Р†Р В°РЎвЂљРЎРЉРЎРѓРЎРЏ Р Р† Р Т‘РЎР‚РЎС“Р С–Р С‘РЎвЂ¦ Р СР ВµРЎРѓРЎвЂљР В°РЎвЂ¦
-- DROP EXTENSION IF EXISTS pg_trgm;

-- ========== 000012: Р ВР Р…Р Т‘Р ВµР С”РЎРѓ user_achievements ==========
DROP INDEX IF EXISTS idx_user_achievements_user_id;

-- ========== 000011: Р СџР С•Р В»Р Р…Р С•РЎвЂљР ВµР С”РЎРѓРЎвЂљР С•Р Р†РЎвЂ№Р в„– Р С—Р С•Р С‘РЎРѓР С” ==========
DROP TRIGGER IF EXISTS trg_games_search_vector ON games;
DROP FUNCTION IF EXISTS games_search_vector_update();
DROP INDEX IF EXISTS idx_games_search_vector;
ALTER TABLE games DROP COLUMN IF EXISTS search_vector;

-- ========== 000010: Р ТђР ВµРЎв‚¬Р С‘РЎР‚Р С•Р Р†Р В°Р Р…Р С‘Р Вµ РЎвЂљР С•Р С”Р ВµР Р…Р С•Р Р† ==========
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

-- ========== 000008: Р ВР Р…Р Т‘Р ВµР С”РЎРѓРЎвЂ№ Р С—РЎР‚Р С•Р С‘Р В·Р Р†Р С•Р Т‘Р С‘РЎвЂљР ВµР В»РЎРЉР Р…Р С•РЎРѓРЎвЂљР С‘ ==========
DROP INDEX IF EXISTS idx_game_passings_game_status;
DROP INDEX IF EXISTS idx_attempts_level_progress_created;
DROP INDEX IF EXISTS idx_games_draft_visibility_created;
DROP INDEX IF EXISTS idx_level_progresses_game_passing_finished;

-- ========== 000007: Р ВР Р…Р Т‘Р ВµР С”РЎРѓРЎвЂ№ РЎвЂћР С‘Р В»РЎРЉРЎвЂљРЎР‚Р В°РЎвЂ Р С‘Р С‘ Р С‘Р С–РЎР‚ ==========
DROP INDEX IF EXISTS idx_game_passings_game_status;
DROP INDEX IF EXISTS idx_reviews_game_id;
DROP INDEX IF EXISTS idx_games_draft_visibility;

-- ========== 000006: Р СћР В°Р В±Р В»Р С‘РЎвЂ Р В° Р Р…Р В°РЎРѓРЎвЂљРЎР‚Р С•Р ВµР С” РЎС“Р Р†Р ВµР Т‘Р С•Р СР В»Р ВµР Р…Р С‘Р в„– ==========
DROP INDEX IF EXISTS idx_notification_settings_user_id;
DROP TABLE IF EXISTS notification_settings;

-- ========== 000005: Р ВР Р…Р Т‘Р ВµР С”РЎРѓ Р В°Р Р†РЎвЂљР С•РЎР‚Р В° Р С‘ РЎРѓРЎвЂљР В°РЎвЂљРЎС“РЎРѓР В° ==========
DROP INDEX IF EXISTS idx_games_author_status;
