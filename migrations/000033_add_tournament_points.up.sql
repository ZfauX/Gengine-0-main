-- 000033_add_tournament_points.up.sql
-- Точное значение начисленных турнирных очков на прохождении (C-M2):
-- RemoveGame списывает ровно то, что было начислено при подсчёте, а не
-- пересчитывает по текущему месту/настройкам (которые могли измениться).
ALTER TABLE game_passings ADD COLUMN IF NOT EXISTS tournament_points integer NOT NULL DEFAULT 0;
