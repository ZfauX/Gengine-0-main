-- 000053_team_member_roles.down.sql
ALTER TABLE team_members
    DROP COLUMN IF EXISTS role,
    DROP COLUMN IF EXISTS group_type,
    DROP COLUMN IF EXISTS field_role;
