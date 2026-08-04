-- 000005_schema.up.sql
-- Р РЋР Р†Р С•Р Т‘Р Р…Р В°РЎРЏ Р СР С‘Р С–РЎР‚Р В°РЎвЂ Р С‘РЎРЏ: Р С‘Р Р…Р Т‘Р ВµР С”РЎРѓРЎвЂ№, РЎвЂљР В°Р В±Р В»Р С‘РЎвЂ РЎвЂ№, Р С—Р С•Р В»Р Р…Р С•РЎвЂљР ВµР С”РЎРѓРЎвЂљР С•Р Р†РЎвЂ№Р в„– Р С—Р С•Р С‘РЎРѓР С”, WebAuthn, РЎС“Р Р†Р ВµР Т‘Р С•Р СР В»Р ВµР Р…Р С‘РЎРЏ
-- Р С›Р В±РЎР‰Р ВµР Т‘Р С‘Р Р…РЎРЏР ВµРЎвЂљ Р С‘Р Р…Р Т‘Р С‘Р Р†Р С‘Р Т‘РЎС“Р В°Р В»РЎРЉР Р…РЎвЂ№Р Вµ Р СР С‘Р С–РЎР‚Р В°РЎвЂ Р С‘Р С‘ 000005РІР‚вЂњ000018

-- ========== 000005: Р РЋР С•РЎРѓРЎвЂљР В°Р Р†Р Р…Р С•Р в„– Р С‘Р Р…Р Т‘Р ВµР С”РЎРѓ Р Т‘Р В»РЎРЏ РЎвЂћР С‘Р В»РЎРЉРЎвЂљРЎР‚Р В°РЎвЂ Р С‘Р С‘ Р С‘Р С–РЎР‚ Р С—Р С• Р В°Р Р†РЎвЂљР С•РЎР‚РЎС“ Р С‘ РЎРѓРЎвЂљР В°РЎвЂљРЎС“РЎРѓРЎС“ ==========
CREATE INDEX IF NOT EXISTS idx_games_author_status ON games(author_id, is_draft);

-- ========== 000006: Р СћР В°Р В±Р В»Р С‘РЎвЂ Р В° Р Р…Р В°РЎРѓРЎвЂљРЎР‚Р С•Р ВµР С” РЎС“Р Р†Р ВµР Т‘Р С•Р СР В»Р ВµР Р…Р С‘Р в„– ==========
CREATE TABLE IF NOT EXISTS notification_settings (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    settings_json TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_notification_settings_user_id ON notification_settings(user_id);

-- ========== 000007: Р ВР Р…Р Т‘Р ВµР С”РЎРѓРЎвЂ№ Р Т‘Р В»РЎРЏ РЎвЂћР С‘Р В»РЎРЉРЎвЂљРЎР‚Р В°РЎвЂ Р С‘Р С‘ Р С‘ РЎРѓР С•РЎР‚РЎвЂљР С‘РЎР‚Р С•Р Р†Р С”Р С‘ Р С‘Р С–РЎР‚ ==========
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility ON games (is_draft, visibility);
CREATE INDEX IF NOT EXISTS idx_reviews_game_id ON reviews (game_id);
CREATE INDEX IF NOT EXISTS idx_game_passings_game_status ON game_passings (game_id, status);

-- ========== 000008: Р ВР Р…Р Т‘Р ВµР С”РЎРѓРЎвЂ№ Р С—РЎР‚Р С•Р С‘Р В·Р Р†Р С•Р Т‘Р С‘РЎвЂљР ВµР В»РЎРЉР Р…Р С•РЎРѓРЎвЂљР С‘ ==========
CREATE INDEX IF NOT EXISTS idx_level_progresses_game_passing_finished
    ON level_progresses(game_passing_id, finished_at)
    WHERE finished_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_created
    ON games(is_draft, visibility, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_attempts_level_progress_created
    ON attempts(level_progress_id, created_at DESC);

-- ========== 000009: 2FA (TOTP + backup codes) ==========
ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_backup_codes TEXT NOT NULL DEFAULT '';

-- ========== 000010: Р ТђР ВµРЎв‚¬Р С‘РЎР‚Р С•Р Р†Р В°Р Р…Р С‘Р Вµ РЎвЂљР С•Р С”Р ВµР Р…Р С•Р Р† (Р Т‘Р С•Р В±Р В°Р Р†Р В»Р ВµР Р…Р С‘Р Вµ Р С”Р С•Р В»Р С•Р Р…Р С•Р С” token_hash) ==========
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS token_hash TEXT;
ALTER TABLE email_verification_tokens ADD COLUMN IF NOT EXISTS token_hash TEXT;

-- ========== 000011: Р СџР С•Р В»Р Р…Р С•РЎвЂљР ВµР С”РЎРѓРЎвЂљР С•Р Р†РЎвЂ№Р в„– Р С—Р С•Р С‘РЎРѓР С” (FTS) ==========
ALTER TABLE games ADD COLUMN IF NOT EXISTS search_vector tsvector;
UPDATE games SET search_vector = to_tsvector('russian', COALESCE(name, '') || ' ' || COALESCE(description, ''));
CREATE INDEX IF NOT EXISTS idx_games_search_vector ON games USING GIN(search_vector);

CREATE OR REPLACE FUNCTION games_search_vector_update() RETURNS trigger AS $$
BEGIN
    NEW.search_vector := to_tsvector('russian', COALESCE(NEW.name, '') || ' ' || COALESCE(NEW.description, ''));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_games_search_vector ON games;
CREATE TRIGGER trg_games_search_vector
    BEFORE INSERT OR UPDATE OF name, description ON games
    FOR EACH ROW EXECUTE FUNCTION games_search_vector_update();

-- ========== 000012: Р ВР Р…Р Т‘Р ВµР С”РЎРѓ user_achievements ==========
CREATE INDEX IF NOT EXISTS idx_user_achievements_user_id ON user_achievements(user_id);

-- ========== 000013: pg_trgm РЎР‚Р В°РЎРѓРЎв‚¬Р С‘РЎР‚Р ВµР Р…Р С‘Р Вµ Р С‘ Р С‘Р Р…Р Т‘Р ВµР С”РЎРѓ ==========
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_games_name_trgm ON games USING gin (name gin_trgm_ops);

-- ========== 000014: Р вЂР В»Р С•Р С”Р С‘РЎР‚Р С•Р Р†Р С”Р В° Р С—РЎР‚Р С‘ Р Р…Р ВµРЎС“Р Т‘Р В°РЎвЂЎР Р…РЎвЂ№РЎвЂ¦ Р С—Р С•Р С—РЎвЂ№РЎвЂљР С”Р В°РЎвЂ¦ Р Р†РЎвЂ¦Р С•Р Т‘Р В° ==========
ALTER TABLE users ADD COLUMN IF NOT EXISTS failed_login_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_until TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_users_locked_until ON users(locked_until);

-- ========== 000015: Р СџР С•Р В»РЎРЏ РЎРѓР В±РЎР‚Р С•РЎРѓР В° Р С—Р В°РЎР‚Р С•Р В»РЎРЏ ==========
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS reset_code VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS used_at TIMESTAMP WITH TIME ZONE;
CREATE UNIQUE INDEX IF NOT EXISTS idx_password_reset_tokens_reset_code ON password_reset_tokens(reset_code);
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_used_at ON password_reset_tokens(used_at);

-- ========== 000016: WebAuthn (passkey) ==========
CREATE TABLE IF NOT EXISTS webauthn_credentials (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    user_id BIGINT NOT NULL REFERENCES users(id),
    credential_id BYTEA NOT NULL,
    public_key BYTEA NOT NULL,
    attestation_type TEXT NOT NULL DEFAULT '',
    transport TEXT[] NOT NULL DEFAULT '{}',
    aaguid BYTEA NOT NULL DEFAULT '\x00000000000000000000000000000000',
    sign_count BIGINT NOT NULL DEFAULT 0,
    backup_eligible BOOLEAN NOT NULL DEFAULT false,
    backup_state BOOLEAN NOT NULL DEFAULT false,
    name TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_webauthn_credentials_user ON webauthn_credentials(user_id);
CREATE UNIQUE INDEX idx_webauthn_credentials_cred_id ON webauthn_credentials(credential_id);

-- ========== 000017: Р Р€Р Р†Р ВµР Т‘Р С•Р СР В»Р ВµР Р…Р С‘РЎРЏ ==========
CREATE TABLE IF NOT EXISTS notifications (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id BIGINT NOT NULL REFERENCES users(id),
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    link TEXT NOT NULL DEFAULT '',
    read BOOLEAN NOT NULL DEFAULT false,
    read_at TIMESTAMPTZ,
    game_id BIGINT,
    team_id BIGINT
);
CREATE INDEX IF NOT EXISTS idx_notifications_user_read ON notifications(user_id, read);

-- ========== 000018: Р СњР ВµР Т‘Р С•РЎРѓРЎвЂљР В°РЎР‹РЎвЂ°Р С‘Р Вµ Р С”Р С•Р В»Р С•Р Р…Р С”Р С‘ ==========
-- email_verification_tokens
ALTER TABLE email_verification_tokens ADD COLUMN IF NOT EXISTS verification_code VARCHAR(8) NOT NULL DEFAULT '';
ALTER TABLE email_verification_tokens ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE email_verification_tokens ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE UNIQUE INDEX IF NOT EXISTS idx_email_verification_tokens_verification_code
    ON email_verification_tokens(verification_code);
CREATE INDEX IF NOT EXISTS idx_email_verification_tokens_deleted_at
    ON email_verification_tokens(deleted_at);

-- password_reset_tokens
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_deleted_at
    ON password_reset_tokens(deleted_at);

-- notifications
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_notifications_deleted_at
    ON notifications(deleted_at);

-- ========== 000019: refresh_tokens Р Р…Р ВµР Т‘Р С•РЎРѓРЎвЂљР В°РЎР‹РЎвЂ°Р С‘Р Вµ Р С”Р С•Р В»Р С•Р Р…Р С”Р С‘ ==========
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_updated_at ON refresh_tokens(updated_at);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_deleted_at ON refresh_tokens(deleted_at);

-- ========== 000020: push_subscriptions, email_queues recipient, device_id index ==========
CREATE TABLE IF NOT EXISTS push_subscriptions (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL,
    auth TEXT NOT NULL,
    p256dh TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_user ON push_subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_endpoint ON push_subscriptions(endpoint);

ALTER TABLE email_queues ADD COLUMN IF NOT EXISTS recipient TEXT;
UPDATE email_queues SET recipient = to_address WHERE recipient IS NULL AND to_address IS NOT NULL;
ALTER TABLE email_queues DROP COLUMN IF EXISTS to_address;

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_device ON refresh_tokens(device_id);

-- ========== 000021: client_fingerprint token binding ==========
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS client_fingerprint TEXT NOT NULL DEFAULT '';


-- ========== 000022: theme settings (auto theme switching) ==========
ALTER TABLE users ADD COLUMN IF NOT EXISTS theme_settings TEXT NOT NULL DEFAULT '';

-- ========== 000023: missing performance indexes ==========
CREATE INDEX IF NOT EXISTS idx_attempts_created_at ON attempts(created_at);
CREATE INDEX IF NOT EXISTS idx_logs_game_passing_created ON logs(game_passing_id, created_at);
CREATE INDEX IF NOT EXISTS idx_invitations_user_status ON invitations(user_id, status);
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_users_name_trgm ON users USING gin(name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_users_email_trgm ON users USING gin(email gin_trgm_ops);
