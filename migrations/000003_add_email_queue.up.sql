-- Создание таблицы для очереди email
CREATE TABLE IF NOT EXISTS email_queues (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    to_address TEXT NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    attempts INTEGER DEFAULT 0,
    last_error TEXT,
    scheduled_at TIMESTAMP WITH TIME ZONE,
    sent_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_email_queues_status_created ON email_queues(status, created_at);
CREATE INDEX idx_email_queues_scheduled_at ON email_queues(scheduled_at);
CREATE INDEX idx_email_queues_deleted_at ON email_queues(deleted_at);