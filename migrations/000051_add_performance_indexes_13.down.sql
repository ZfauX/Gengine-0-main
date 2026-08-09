-- 000051_add_performance_indexes_13.down.sql
DROP INDEX IF EXISTS idx_external_logins_provider_external_unique;
DROP INDEX IF EXISTS idx_blackbox_voting_sessions_passing_level;
