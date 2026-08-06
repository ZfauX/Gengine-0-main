-- 000034_unique_reviews.up.sql
-- Защита от дублирования отзывов (pass 24 / C-3): уникальный индекс
-- reviews(game_id, user_id) — два параллельных POST от одного пользователя
-- не создадут два отзыва и не завысят рейтинг.
CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_game_user ON reviews(game_id, user_id);
