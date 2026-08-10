-- migrations_squashed/000007_schema_tail.up.sql
-- Полный слепок изменений 000037-000059 (новые колонки, индексы, таблицы).
-- Created automatically from individual migrations to keep squashed schema current.

-- ========== 000037_add_performance_indexes.up.sql ==========
-- 000037_add_performance_indexes.up.sql
-- Индексы из pass 28 (секция B, PF5/PF6 и рекомендованные):

-- Лидерборд турнира: ORDER BY score DESC по турниру (filesort на данных без индекса).
CREATE INDEX IF NOT EXISTS idx_tournament_results_tournament_score
    ON tournament_results(tournament_id, score DESC);

-- Чат: выборка истории комнаты ORDER BY created_at DESC (filesort на каждом открытии).
CREATE INDEX IF NOT EXISTS idx_chat_messages_room_created
    ON chat_messages(room_id, created_at DESC);

-- Timeout-воркер: незавершённые прогрессы (finished_at IS NULL) по времени старта.
CREATE INDEX IF NOT EXISTS idx_level_progresses_unfinished_started
    ON level_progresses(started_at) WHERE finished_at IS NULL;

-- Листинг игр: фильтр по статусу/видимости + сортировка по названию.
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_name
    ON games(is_draft, visibility, name);

-- Календарь/листинг: фильтр по статусу/видимости + время старта.
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_starts
    ON games(is_draft, visibility, starts_at);

-- Отзывы на странице игры: ORDER BY created_at DESC.
CREATE INDEX IF NOT EXISTS idx_reviews_game_created
    ON reviews(game_id, created_at DESC);

-- ========== 000038_add_performance_indexes_2.up.sql ==========
-- 000038_add_performance_indexes_2.up.sql
-- Индексы из pass 29 (секция B):

-- OAuth: поиск/создание external_login по провайдеру (на каждый вход).
CREATE INDEX IF NOT EXISTS idx_external_logins_provider_external
    ON external_logins(provider, external_id);

-- Удаление пользователя: каскад по user_id.
CREATE INDEX IF NOT EXISTS idx_external_logins_user_id
    ON external_logins(user_id);

-- Лидерборд: ORDER BY score DESC.
CREATE INDEX IF NOT EXISTS idx_player_ratings_score
    ON player_ratings(score DESC);

-- Админ-поиск команд: ILIKE по teams.name (trgm есть для users/games).
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_teams_name_trgm
    ON teams USING gin (name gin_trgm_ops);

-- ========== 000039_add_performance_indexes_3.up.sql ==========
-- 000039_add_performance_indexes_3.up.sql
-- Индексы из pass 30 (секция B):

-- Поиск приглашений в команду: WHERE team_id = ? (SearchUsersForInvitation,
-- RemoveMember, IsMember) + JOIN dashboard/rating — секвес-скан без индекса.
CREATE INDEX IF NOT EXISTS idx_team_members_team_id
    ON team_members(team_id);

-- Dashboard команды: JOIN team_members → teams → game_passings по
-- (team_id, status), фильтр по активным прохождениям.
CREATE INDEX IF NOT EXISTS idx_game_passings_team_status
    ON game_passings(team_id, status);

-- GameSnapshot (мониторинг): LATERAL ORDER BY created_at на незавершённых
-- прогрессах уровня.
CREATE INDEX IF NOT EXISTS idx_level_progresses_passing_unfinished_created
    ON level_progresses(game_passing_id, created_at DESC) WHERE finished_at IS NULL;

-- ========== 000040_add_performance_indexes_4.up.sql ==========
-- 000040_add_performance_indexes_4.up.sql
-- Индексы из pass 31 (секция B):

-- Аудит: фильтр user_id/action + пагинация ORDER BY created_at DESC.
-- Одна большая append-only таблица — составные индексы вместо пересечения
-- одноколоночных (F2).
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_created
    ON audit_logs(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_logs_action_created
    ON audit_logs(action, created_at DESC);

-- Dashboard: pending-приглашения пользователя с сортировкой по времени.
-- idx_invitations_user_status покрывает фильтр, но не сортировку (F12).
CREATE INDEX IF NOT EXISTS idx_invitations_user_status_created
    ON invitations(user_id, status, created_at DESC);

-- ========== 000041_add_lock_count.up.sql ==========
-- 000041_add_lock_count.up.sql
-- S-4 (pass 31): экспоненциальный backoff при блокировке аккаунта.
-- lock_count — число последовательных блокировок; длительность следующей
-- блокировки = min(5 мин * 2^(lock_count-1), 24 ч).
ALTER TABLE users ADD COLUMN IF NOT EXISTS lock_count integer NOT NULL DEFAULT 0;

-- ========== 000042_add_performance_indexes_5.up.sql ==========
-- 000042_add_performance_indexes_5.up.sql
-- Индексы из pass 32 (секция B):

-- Список заметок: ORDER BY created_at DESC по игре (note_repository.ListByGame).
CREATE INDEX IF NOT EXISTS idx_notes_game_created
    ON notes(game_id, created_at DESC);

-- Галерея фото: ORDER BY created_at DESC по игре (photo_repository.List).
CREATE INDEX IF NOT EXISTS idx_photos_game_created
    ON photos(game_id, created_at DESC);

-- StartTesting ищет тестовую команду по точному имени (svc_play).
-- Существующий idx_teams_name_trgm (000038) — для ILIKE, не для =.
CREATE INDEX IF NOT EXISTS idx_teams_name
    ON teams(name);

-- ========== 000043_add_performance_indexes_6.up.sql ==========
-- 000043_add_performance_indexes_6.up.sql
-- Индексы из pass 33 (секция B):

-- Список уведомлений пользователя: WHERE user_id ORDER BY created_at DESC.
-- idx_notifications_user_read (000017) покрывает фильтр по read, но не сортировку.
CREATE INDEX IF NOT EXISTS idx_notifications_user_created
    ON notifications(user_id, created_at DESC);

-- Листинг авторских игр: WHERE author_id AND is_draft ORDER BY created_at DESC
-- (дополняет idx_games_author_status 000005).
CREATE INDEX IF NOT EXISTS idx_games_author_draft_created
    ON games(author_id, is_draft, created_at DESC);

-- ========== 000044_add_performance_indexes_7.up.sql ==========
-- 000044_add_performance_indexes_7.up.sql
-- Индексы из pass 34 (секция B):

-- Retention прочитанных уведомлений: ежедневный DELETE по read_at.
-- Без partial-индекса — полный seq-скан таблицы (F-2).
CREATE INDEX IF NOT EXISTS idx_notifications_read_at_read
    ON notifications(read_at) WHERE read = true;

-- Аутентифицированный листинг игр: OR-ветка (visibility/public/author).
CREATE INDEX IF NOT EXISTS idx_games_visibility_draft_author
    ON games(visibility, is_draft, author_id);

-- ========== 000045_add_performance_indexes_8.up.sql ==========
-- 000045_add_performance_indexes_8.up.sql
-- Индексы из pass 37-38 (секция B, P-7 / P-2):

-- Голосование «чёрного ящика»: подзапрос level_progresses по (game_passing_id, level_id)
-- в monitor/service.go Vote — без композитного индекса секвес-скан на партиции прохождения.
-- P-2 (pass 38) + S-1 (pass 39): UNIQUE с WHERE deleted_at IS NULL — автостарт создаёт
-- прогресс первого уровня с ON CONFLICT (game_passing_id, level_id). Частичный индекс
-- не блокирует повторное создание после soft-delete прогресса и не падает, если в БД
-- уже есть дубликаты среди удалённых строк.
CREATE UNIQUE INDEX IF NOT EXISTS idx_level_progresses_passing_level
    ON level_progresses(game_passing_id, level_id) WHERE deleted_at IS NULL;

-- ========== 000046_logs_game_id_denormalization.up.sql ==========
-- 000046_logs_game_id_denormalization.up.sql
-- P-5 (pass 39): денормализация logs.game_id.
-- GetLogsByGameID/GetLogsByGameIDPaginated фильтруют по game_id через JOIN
-- game_passings, а ORDER BY logs.created_at поперёк всех passings игры не
-- покрыт индексом (heap-sort на больших играх). Добавляем колонку game_id,
-- backfill и составной индекс (game_id, created_at DESC).

ALTER TABLE logs ADD COLUMN IF NOT EXISTS game_id bigint NOT NULL DEFAULT 0;

-- Backfill из game_passings.
UPDATE logs SET game_id = gp.game_id
FROM game_passings gp
WHERE gp.id = logs.game_passing_id AND logs.game_id = 0;

CREATE INDEX IF NOT EXISTS idx_logs_game_created
    ON logs(game_id, created_at DESC);

-- ========== 000047_add_performance_indexes_9.up.sql ==========
-- 000047_add_performance_indexes_9.up.sql
-- Индексы из pass 40 (L-3):

-- GetByTournamentAndTeam / GetByTournamentAndTeamIDs фильтруют по
-- (tournament_id, team_id); существующий idx_tournament_results_tournament_score
-- (tournament_id, score DESC) не покрывает выборку по team_id (leading-колонка
-- score). Seq scan при lookup команды в турнире.
CREATE INDEX IF NOT EXISTS idx_tournament_results_tournament_team
    ON tournament_results(tournament_id, team_id);

-- ========== 000048_add_performance_indexes_10.up.sql ==========
-- 000048_add_performance_indexes_10.up.sql
-- Индексы из pass 42 (P-04):

-- Vote (monitor/service.go) ищет попытки по (level_progress_id, is_file, code)
-- / (level_progress_id, is_file, file_path) для проверки варианта ответа.
-- Существующий idx_attempts_level_progress_id даёт residual-фильтр по code/
-- file_path на каждой строке прогресса уровня; составной индекс покрывает
-- выборку. (Композит blackbox_votes(session_id, voter_id) уже есть в 000023.)
CREATE INDEX IF NOT EXISTS idx_attempts_progress_is_file_code
    ON attempts(level_progress_id, is_file, code);

CREATE INDEX IF NOT EXISTS idx_attempts_progress_is_file_path
    ON attempts(level_progress_id, is_file, file_path);

-- P-06 (pass 42): GetPassingByUser JOIN team_members ON team_id WHERE
-- team_members.user_id = ? (горячий gameplay-экран). Существующий
-- idx_team_members_team_id заставляет сканировать всех членов совпавших команд.
CREATE INDEX IF NOT EXISTS idx_team_members_user_id
    ON team_members(user_id);

-- P-05 (pass 42): ListFilteredPaginated по умолчанию сортирует
-- ORDER BY games.created_at DESC; существующие idx_games_draft_visibility_name/
-- starts_at не покрывают дефолтный порядок → полная сортировка множества.
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_created
    ON games(is_draft, visibility, created_at DESC);

-- ========== 000049_add_performance_indexes_11.up.sql ==========
-- 000049_add_performance_indexes_11.up.sql
-- Индексы из pass 43 (P-43-5):

-- TeamRepository.SearchPaginated/CountSearch фильтруют teams.name ILIKE
-- (admin-поиск команд); users.name уже имеет pg_trgm (000023), teams.name — нет,
-- поэтому поиск по имени команды делал seq-scan. pg_trgm уже создан в 000023.
CREATE INDEX IF NOT EXISTS idx_teams_name_trgm
    ON teams USING gin(name gin_trgm_ops);

-- ========== 000050_add_performance_indexes_12.up.sql ==========
-- 000050_add_performance_indexes_12.up.sql
-- Индексы из pass 43 (P-43-3):

-- CheckTimeouts (svc_progress.go) каждые 30с: WHERE finished_at IS NULL
-- ORDER BY started_at ASC LIMIT batch. Существующие partial-индексы
-- (game_passing_id, finished_at) / (game_passing_id, created_at) не покрывают
-- сортировку по started_at незавершённых прогрессов → полная сортировка.
CREATE INDEX IF NOT EXISTS idx_level_progresses_unfinished_started
    ON level_progresses(started_at) WHERE finished_at IS NULL;

-- ========== 000051_add_performance_indexes_13.up.sql ==========
-- 000051_add_performance_indexes_13.up.sql
-- Индексы из pass 44:

-- S-44-1: ExternalLogin(provider, external_id) должен быть UNIQUE — FindOrCreate
-- (FirstOrCreate) — проверка-затем-вставка; два параллельных OAuth-флоу с одним
-- VK user_id могли создать дубликаты, указывающие на разных пользователей.
-- ВАЖНО: перед применением убедиться, что дубликатов нет (SELECT provider,
-- external_id, count(*) FROM external_logins GROUP BY 1,2 HAVING count(*)>1;).
CREATE UNIQUE INDEX IF NOT EXISTS idx_external_logins_provider_external_unique
    ON external_logins(provider, external_id);

-- P-44-6: GetOpenVotingSession/StartVoting фильтруют
-- (game_passing_id, level_id, is_open) — раздельные idx_blackbox_voting_sessions_*
-- не покрывают выборку.
CREATE INDEX IF NOT EXISTS idx_blackbox_voting_sessions_passing_level
    ON blackbox_voting_sessions(game_passing_id, level_id);
-- NB (P-45-4, pass 45): co_authors composite НЕ добавляем — GORM AutoMigrate уже
-- создаёт uniqueIndex idx_game_user(GameID, UserID) (model.go:132-133), покрывающий
-- FindByGameAndUser. Миграция 000051 из pass 44 содержала дубль — удалён.

-- ========== 000052_coauthor_permissions.up.sql ==========
-- 000052_coauthor_permissions.up.sql
-- A-1 (pass 45): выборочные права соавторов (jsonb).
-- Пресет для существующих записей заполняется из role:
--   observer -> ['read']
--   content_editor -> ['read','edit_content','upload_media']
--   moderator -> ['read','edit_content','upload_media','moderate']
ALTER TABLE co_authors
    ADD COLUMN IF NOT EXISTS permissions JSONB NOT NULL DEFAULT '["read"]'::jsonb;

UPDATE co_authors
SET permissions = CASE role
    WHEN 'moderator' THEN '["read","edit_content","upload_media","moderate"]'::jsonb
    WHEN 'content_editor' THEN '["read","edit_content","upload_media"]'::jsonb
    WHEN 'observer' THEN '["read"]'::jsonb
    ELSE '["read"]'::jsonb
END
WHERE permissions = '["read"]'::jsonb AND role IS NOT NULL AND role != '';

-- ========== 000053_team_member_roles.up.sql ==========
-- 000053_team_member_roles.up.sql
-- A-2/A-3 (pass 45): роли участников команды и группы.
-- team_members получает:
--   role       — 'member' | 'deputy' (зам. капитана); капитан хранится в teams.captain_id
--   group_type — 'main' | 'reserve' (основной состав / резерв)
--   field_role — 'field' | 'driver' | 'navigator' (роль на поле)
ALTER TABLE team_members
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'member',
    ADD COLUMN IF NOT EXISTS group_type TEXT NOT NULL DEFAULT 'main',
    ADD COLUMN IF NOT EXISTS field_role TEXT NOT NULL DEFAULT 'field';

-- ========== 000054_chat_room_permissions.up.sql ==========
-- 000054_chat_room_permissions.up.sql
-- B-1/B-5 (pass 45): типы комнат чата, владелец, членство с правами.
ALTER TABLE chat_rooms
    ADD COLUMN IF NOT EXISTS room_type TEXT NOT NULL DEFAULT 'game_general',
    ADD COLUMN IF NOT EXISTS owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_chat_rooms_type ON chat_rooms(room_type);
CREATE INDEX IF NOT EXISTS idx_chat_rooms_owner ON chat_rooms(owner_id);

CREATE TABLE IF NOT EXISTS chat_room_members (
    room_id INTEGER NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    can_read BOOLEAN NOT NULL DEFAULT TRUE,
    can_write BOOLEAN NOT NULL DEFAULT TRUE,
    can_attach BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (room_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_room_member_user ON chat_room_members(user_id);

-- ========== 000055_team_routes_and_answers.up.sql ==========
-- 000055_team_routes_and_answers.up.sql
-- Фаза 3 (Эпик C): маршруты команд, индивидуальный старт, разные ответы.

-- C-3: индивидуальное время старта команды (NULL = общее время игры).
ALTER TABLE game_passings
    ADD COLUMN IF NOT EXISTS start_time TIMESTAMP WITH TIME ZONE;

-- C-1/C-2: маршрут команды — порядок прохождения уровней.
-- game_passing_levels(game_passing_id, level_id, order_index).
CREATE TABLE IF NOT EXISTS game_passing_levels (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    game_passing_id INTEGER NOT NULL REFERENCES game_passings(id) ON DELETE CASCADE,
    level_id INTEGER NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    order_index INTEGER NOT NULL DEFAULT 0,
    UNIQUE(game_passing_id, level_id)
);
CREATE INDEX IF NOT EXISTS idx_gpl_passing_order ON game_passing_levels(game_passing_id, order_index);

-- C-4: разные ответы на уровень для конкретной команды.
-- level_team_answers(level_id, team_id, code, hint) — если запись есть,
-- для команды ожидается именно этот код.
CREATE TABLE IF NOT EXISTS level_team_answers (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    level_id INTEGER NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    code TEXT NOT NULL,
    hint TEXT NOT NULL DEFAULT '',
    UNIQUE(level_id, team_id)
);
CREATE INDEX IF NOT EXISTS idx_lta_team ON level_team_answers(team_id);

-- C-5: кто именно в команде отправил код (для итогов «на человека»).
ALTER TABLE attempts
    ADD COLUMN IF NOT EXISTS user_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_attempts_user ON attempts(user_id);

-- ========== 000056_notify_game_days.up.sql ==========
-- 000056_notify_game_days.up.sql
-- D-1 (pass 45): за сколько дней уведомлять пользователя о предстоящих играх.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS notify_game_days INTEGER NOT NULL DEFAULT 0;

-- ========== 000057_player_locations.up.sql ==========
-- 000057_player_locations.up.sql
-- G-1..G-4 (pass 45): позиции игроков (водителей) во время игры.
CREATE TABLE IF NOT EXISTS player_locations (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    game_passing_id BIGINT NOT NULL REFERENCES game_passings(id) ON DELETE CASCADE,
    team_id BIGINT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    accuracy DOUBLE PRECISION NOT NULL DEFAULT 0,
    UNIQUE(game_passing_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_player_loc_team ON player_locations(team_id);

-- ========== 000058_payments.up.sql ==========
-- 000058_payments.up.sql
-- G-1..G-3 (pass 45): платёжные записи ЮKassa.
CREATE TABLE IF NOT EXISTS payments (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    payment_id TEXT NOT NULL UNIQUE,
    idempotency_key TEXT NOT NULL,
    amount DOUBLE PRECISION NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'RUB',
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    confirmation_url TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_payments_user ON payments(user_id);
CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status);
CREATE INDEX IF NOT EXISTS idx_payments_idempotency ON payments(idempotency_key);

-- ========== 000059_chat_personal_rooms.up.sql ==========
-- 000059_chat_personal_rooms.up.sql
-- B-7 (pass 45): личный чат 1-на-1 — поля участников в chat_rooms.
ALTER TABLE chat_rooms
    ADD COLUMN IF NOT EXISTS user1_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS user2_id BIGINT REFERENCES users(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_chat_rooms_users ON chat_rooms(user1_id, user2_id);

