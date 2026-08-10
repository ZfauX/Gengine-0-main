-- 000052_coauthor_permissions.up.sql
-- A-1 (pass 45): выборочные права соавторов (jsonb).
-- Пресет для существующих записей заполняется из role:
--   observer -> ['read']
--   content_editor -> ['read','edit_content','upload_media']
--   moderator -> ['read','edit_content','upload_media','moderate']
ALTER TABLE co_authors
    ADD COLUMN IF NOT EXISTS permissions JSONB NOT NULL DEFAULT '["read"]'::jsonb;

UPDATE co_authors
SET permissions = CASE role
    WHEN 'moderator' THEN '["read","edit_content","upload_media","moderate"]'::jsonb
    WHEN 'content_editor' THEN '["read","edit_content","upload_media"]'::jsonb
    WHEN 'observer' THEN '["read"]'::jsonb
    ELSE '["read"]'::jsonb
END
WHERE permissions = '["read"]'::jsonb AND role IS NOT NULL AND role != '';
