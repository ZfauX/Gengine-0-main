-- 000020_fix_migration_gaps.down.sql
-- Откат изменений 000020

-- ========== 1. push_subscriptions ==========
DROP TABLE IF EXISTS push_subscriptions CASCADE;

-- ========== 2. email_queues: recipient → to_address ==========
ALTER TABLE email_queues ADD COLUMN IF NOT EXISTS to_address TEXT;
UPDATE email_queues SET to_address = recipient WHERE to_address IS NULL AND recipient IS NOT NULL;
ALTER TABLE email_queues DROP COLUMN IF EXISTS recipient;

-- ========== 3. refresh_tokens: device_id index ==========
DROP INDEX IF EXISTS idx_refresh_tokens_device;
