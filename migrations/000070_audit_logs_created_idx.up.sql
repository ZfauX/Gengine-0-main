-- 000070_audit_logs_created_idx.up.sql
-- M9 (PASS-17): индекс по audit_logs(created_at) — админ-журнал сортируется
-- ORDER BY created_at DESC (pkg/audit) с пагинацией; без индекса каждая
-- страница делает heap-sort всей отфильтрованной выборки.
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at DESC);
