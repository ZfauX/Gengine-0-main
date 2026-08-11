-- 000063_payments_amount_kopecks.down.sql
-- DEEP-REVIEW PASS-3 M10: откат — восстанавливаем amount (рубли) из копеек,
-- затем удаляем amount_kopecks.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS amount DOUBLE PRECISION NOT NULL DEFAULT 0;

UPDATE payments
SET amount = amount_kopecks::DOUBLE PRECISION / 100.0
WHERE amount = 0 AND amount_kopecks > 0;

ALTER TABLE payments DROP COLUMN IF EXISTS amount_kopecks;
