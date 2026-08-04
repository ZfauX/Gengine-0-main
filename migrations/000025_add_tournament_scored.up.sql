-- 000025_add_tournament_scored.up.sql
-- Идемпотентность начисления турнирных очков: очки начисляются только
-- один раз за прохождение (обычный финиш, таймаут или force-finish).
ALTER TABLE game_passings ADD COLUMN IF NOT EXISTS tournament_scored boolean NOT NULL DEFAULT false;
