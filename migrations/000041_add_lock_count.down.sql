-- 000041_add_lock_count.down.sql
ALTER TABLE users DROP COLUMN IF EXISTS lock_count;
