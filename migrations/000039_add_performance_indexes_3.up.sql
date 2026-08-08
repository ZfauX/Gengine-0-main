-- 000039_add_performance_indexes_3.up.sql
-- Индексы из pass 30 (секция B):

-- Поиск приглашений в команду: WHERE team_id = ? (SearchUsersForInvitation,
-- RemoveMember, IsMember) + JOIN dashboard/rating — секвес-скан без индекса.
CREATE INDEX IF NOT EXISTS idx_team_members_team_id
    ON team_members(team_id);

-- Dashboard команды: JOIN team_members → teams → game_passings по
-- (team_id, status), фильтр по активным прохождениям.
CREATE INDEX IF NOT EXISTS idx_game_passings_team_status
    ON game_passings(team_id, status);

-- GameSnapshot (мониторинг): LATERAL ORDER BY created_at на незавершённых
-- прогрессах уровня.
CREATE INDEX IF NOT EXISTS idx_level_progresses_passing_unfinished_created
    ON level_progresses(game_passing_id, created_at DESC) WHERE finished_at IS NULL;
