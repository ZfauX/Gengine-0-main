-- 000022_add_theme_settings.down.sql
ALTER TABLE users DROP COLUMN IF EXISTS theme_settings;
