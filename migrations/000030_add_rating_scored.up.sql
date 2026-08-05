-- 000030_add_rating_scored.up.sql
-- Идемпотентность начисления очков рейтинга (B3): флаг на games предотвращает
-- двойное начисление при повторном вызове UpdateRatingsForGame (авторские +
-- командные очки начисляются один раз за игру).
ALTER TABLE games ADD COLUMN IF NOT EXISTS rating_scored boolean NOT NULL DEFAULT false;
