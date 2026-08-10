-- 000052_coauthor_permissions.down.sql
ALTER TABLE co_authors DROP COLUMN IF EXISTS permissions;
