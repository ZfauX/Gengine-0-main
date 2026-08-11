-- 000061_team_members_user_unique.up.sql
-- DEEP-REVIEW (pass 46): инвариант «игрок в одной команде» (A-5) теперь
-- гарантируется на уровне БД. Раньше проверка в сервисе была TOCTOU:
-- AcceptInvitation проверял GetTeamsByUserID ВНЕ транзакции, а INSERT
-- защищался только ON CONFLICT (team_id, user_id) — два конкурентных
-- accept могли поставить игрока в две команды.
-- team_members не использует soft-delete (primaryKey = team_id+user_id),
-- поэтому уникальный индекс на user_id безопасен.
CREATE UNIQUE INDEX IF NOT EXISTS idx_team_members_user_unique
    ON team_members(user_id);
