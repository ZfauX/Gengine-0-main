-- 000048_add_performance_indexes_10.up.sql
-- Индексы из pass 41 (N-2):

-- Vote (monitor/service.go) ищет голос по (session_id, voter_id); раздельные
-- idx_blackbox_votes_session_id / idx_blackbox_votes_voter_id дают bitmap-OR
-- вместо одного композитного. Также GetVoteBySessionAndVoter.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_blackbox_votes_session_voter
    ON blackbox_votes(session_id, voter_id);
