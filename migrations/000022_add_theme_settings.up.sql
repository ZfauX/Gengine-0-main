-- 000022_add_theme_settings.up.sql
ALTER TABLE users ADD COLUMN IF NOT EXISTS theme_settings TEXT NOT NULL DEFAULT '';
