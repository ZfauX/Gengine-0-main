-- 000058_payments.up.sql
-- G-1..G-3 (pass 45): платёжные записи ЮKassa.
CREATE TABLE IF NOT EXISTS payments (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    payment_id TEXT NOT NULL UNIQUE,
    idempotency_key TEXT NOT NULL,
    amount DOUBLE PRECISION NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'RUB',
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    confirmation_url TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_payments_user ON payments(user_id);
CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status);
CREATE INDEX IF NOT EXISTS idx_payments_idempotency ON payments(idempotency_key);
