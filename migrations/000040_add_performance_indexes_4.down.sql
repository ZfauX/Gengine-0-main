-- 000040_add_performance_indexes_4.down.sql
DROP INDEX IF EXISTS idx_audit_logs_user_created;
DROP INDEX IF EXISTS idx_audit_logs_action_created;
DROP INDEX IF EXISTS idx_invitations_user_status_created;
