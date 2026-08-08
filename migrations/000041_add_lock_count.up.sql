-- 000041_add_lock_count.up.sql
-- S-4 (pass 31): экспоненциальный backoff при блокировке аккаунта.
-- lock_count — число последовательных блокировок; длительность следующей
-- блокировки = min(5 мин * 2^(lock_count-1), 24 ч).
ALTER TABLE users ADD COLUMN IF NOT EXISTS lock_count integer NOT NULL DEFAULT 0;
