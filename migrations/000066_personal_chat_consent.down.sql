-- 000066_personal_chat_consent.down.sql
-- DEEP-REVIEW PASS-6 M7: откат.
ALTER TABLE chat_rooms DROP COLUMN IF EXISTS accepted;
