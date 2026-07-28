-- 000020_fix_migration_gaps.up.sql
-- Исправление проблем, найденных при аудите миграций

-- ========== 1. push_subscriptions (модель существует, таблицы нет) ==========
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

-- ========== 2. email_queues: to_address → recipient ==========
-- Модель QueuedEmail использует поле Recipient → колонка recipient
ALTER TABLE email_queues ADD COLUMN IF NOT EXISTS recipient TEXT;
UPDATE email_queues SET recipient = to_address WHERE recipient IS NULL AND to_address IS NOT NULL;
ALTER TABLE email_queues DROP COLUMN IF EXISTS to_address;

-- ========== 3. refresh_tokens: недостающий индекс device_id ==========
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_device ON refresh_tokens(device_id);
