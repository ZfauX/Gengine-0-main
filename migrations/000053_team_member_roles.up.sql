-- 000053_team_member_roles.up.sql
-- A-2/A-3 (pass 45): роли участников команды и группы.
-- team_members получает:
--   role       — 'member' | 'deputy' (зам. капитана); капитан хранится в teams.captain_id
--   group_type — 'main' | 'reserve' (основной состав / резерв)
--   field_role — 'field' | 'driver' | 'navigator' (роль на поле)
ALTER TABLE team_members
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'member',
    ADD COLUMN IF NOT EXISTS group_type TEXT NOT NULL DEFAULT 'main',
    ADD COLUMN IF NOT EXISTS field_role TEXT NOT NULL DEFAULT 'field';
