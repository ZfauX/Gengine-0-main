-- migrations_squashed/000010_schema_tail.up.sql
-- Дополнение после миграции 000063.

-- ========== 000063_payments_amount_kopecks.up.sql ==========
-- 000063_payments_amount_kopecks.up.sql
-- DEEP-REVIEW PASS-3 M10: денежная арифметика в копейках (int64) вместо float64.
-- Добавляем amount_kopecks и переносим данные из amount (рубли, float64 → копейки).
ALTER TABLE payments ADD COLUMN IF NOT EXISTS amount_kopecks BIGINT NOT NULL DEFAULT 0;

UPDATE payments
SET amount_kopecks = ROUND(amount * 100)::BIGINT
WHERE amount_kopecks = 0 AND amount IS NOT NULL AND amount > 0;
