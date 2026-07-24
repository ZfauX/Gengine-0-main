-- 000001_foundation.up.sql
-- Все таблицы и базовые индексы

-- ========== users ==========
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    email TEXT NOT NULL UNIQUE,
    password TEXT NOT NULL,
    name TEXT NOT NULL,
    role TEXT DEFAULT 'user',
    email_verified BOOLEAN DEFAULT FALSE,
    avatar_path TEXT DEFAULT '',
    profile_visibility TEXT DEFAULT 'public',
    plan TEXT DEFAULT 'free',
    stripe_customer_id TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON users(deleted_at);

-- ========== achievements ==========
CREATE TABLE IF NOT EXISTS achievements (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT,
    icon TEXT
);

-- ========== user_achievements ==========
CREATE TABLE IF NOT EXISTS user_achievements (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    achievement_id INTEGER NOT NULL REFERENCES achievements(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, achievement_id)
);

-- ========== external_logins ==========
CREATE TABLE IF NOT EXISTS external_logins (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    external_id TEXT NOT NULL,
    access_token TEXT,
    refresh_token TEXT,
    expires_at TIMESTAMP WITH TIME ZONE
);

-- ========== password_reset_tokens ==========
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL
);

-- ========== email_verification_tokens ==========
CREATE TABLE IF NOT EXISTS email_verification_tokens (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL
);

-- ========== games ==========
CREATE TABLE IF NOT EXISTS games (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    name TEXT NOT NULL,
    description TEXT,
    author_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_draft BOOLEAN DEFAULT TRUE,
    visibility TEXT DEFAULT 'public',
    starts_at TIMESTAMP WITH TIME ZONE,
    registration_deadline TIMESTAMP WITH TIME ZONE,
    max_team_number INTEGER DEFAULT 10,
    cover_path TEXT
);
CREATE INDEX IF NOT EXISTS idx_games_author_id ON games(author_id);
CREATE INDEX IF NOT EXISTS idx_games_is_draft ON games(is_draft);
CREATE INDEX IF NOT EXISTS idx_games_visibility ON games(visibility);
CREATE INDEX IF NOT EXISTS idx_games_starts_at ON games(starts_at);
CREATE INDEX IF NOT EXISTS idx_games_registration_deadline ON games(registration_deadline);
CREATE INDEX IF NOT EXISTS idx_games_deleted_at ON games(deleted_at);

-- ========== game_settings ==========
CREATE TABLE IF NOT EXISTS game_settings (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_id INTEGER NOT NULL UNIQUE REFERENCES games(id) ON DELETE CASCADE,
    allow_hints BOOLEAN DEFAULT TRUE,
    hint_penalty_seconds INTEGER DEFAULT 300,
    max_hints INTEGER DEFAULT 3,
    per_level_time_limit INTEGER DEFAULT 0,
    hide_answers_until_finished BOOLEAN DEFAULT FALSE,
    auto_start BOOLEAN DEFAULT FALSE
);

-- ========== levels ==========
CREATE TABLE IF NOT EXISTS levels (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    position INTEGER DEFAULT 0,
    type TEXT DEFAULT 'single',
    parent_id INTEGER REFERENCES levels(id) ON DELETE SET NULL,
    group_id INTEGER REFERENCES levels(id) ON DELETE SET NULL,
    min_children INTEGER DEFAULT 0,
    requires_confirmation BOOLEAN DEFAULT FALSE,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    UNIQUE (game_id, position)
);
CREATE INDEX IF NOT EXISTS idx_levels_game_id ON levels(game_id);
CREATE INDEX IF NOT EXISTS idx_levels_parent_id ON levels(parent_id);
CREATE INDEX IF NOT EXISTS idx_levels_group_id ON levels(group_id);
CREATE INDEX IF NOT EXISTS idx_levels_deleted_at ON levels(deleted_at);

-- ========== questions ==========
CREATE TABLE IF NOT EXISTS questions (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    level_id INTEGER NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    text TEXT NOT NULL,
    hint TEXT
);
CREATE INDEX IF NOT EXISTS idx_questions_level_id ON questions(level_id);

-- ========== answers ==========
CREATE TABLE IF NOT EXISTS answers (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    question_id INTEGER NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    code TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_answers_question_id ON answers(question_id);

-- ========== teams ==========
CREATE TABLE IF NOT EXISTS teams (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    name TEXT NOT NULL,
    captain_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_teams_captain_id ON teams(captain_id);

-- ========== team_members ==========
CREATE TABLE IF NOT EXISTS team_members (
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_team_members_user_id ON team_members(user_id);

-- ========== invitations ==========
CREATE TABLE IF NOT EXISTS invitations (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT DEFAULT 'pending',
    expires_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_invitations_team_id ON invitations(team_id);
CREATE INDEX IF NOT EXISTS idx_invitations_user_id ON invitations(user_id);
CREATE INDEX IF NOT EXISTS idx_invitations_status ON invitations(status);

-- ========== game_passings ==========
CREATE TABLE IF NOT EXISTS game_passings (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    status TEXT DEFAULT 'pending',
    result_duration BIGINT,
    place INTEGER,
    UNIQUE(game_id, team_id)
);
CREATE INDEX IF NOT EXISTS idx_game_passings_game_id ON game_passings(game_id);
CREATE INDEX IF NOT EXISTS idx_game_passings_team_id ON game_passings(team_id);
CREATE INDEX IF NOT EXISTS idx_game_passings_status ON game_passings(status);
CREATE INDEX IF NOT EXISTS idx_game_passings_deleted_at ON game_passings(deleted_at);

-- ========== level_progresses ==========
CREATE TABLE IF NOT EXISTS level_progresses (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_passing_id INTEGER NOT NULL REFERENCES game_passings(id) ON DELETE CASCADE,
    level_id INTEGER NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    started_at TIMESTAMP WITH TIME ZONE NOT NULL,
    finished_at TIMESTAMP WITH TIME ZONE,
    hints_used INTEGER DEFAULT 0,
    penalty_seconds INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_level_progresses_game_passing_id ON level_progresses(game_passing_id);
CREATE INDEX IF NOT EXISTS idx_level_progresses_level_id ON level_progresses(level_id);
CREATE INDEX IF NOT EXISTS idx_level_progresses_deleted_at ON level_progresses(deleted_at);

-- ========== attempts ==========
CREATE TABLE IF NOT EXISTS attempts (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    level_progress_id INTEGER NOT NULL REFERENCES level_progresses(id) ON DELETE CASCADE,
    code TEXT,
    file_path TEXT,
    is_file BOOLEAN DEFAULT FALSE,
    success BOOLEAN DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_attempts_level_progress_id ON attempts(level_progress_id);

-- ========== co_authors ==========
CREATE TABLE IF NOT EXISTS co_authors (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT DEFAULT 'content',
    UNIQUE(game_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_co_authors_game_id ON co_authors(game_id);
CREATE INDEX IF NOT EXISTS idx_co_authors_user_id ON co_authors(user_id);

-- ========== notes ==========
CREATE TABLE IF NOT EXISTS notes (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level_id INTEGER REFERENCES levels(id) ON DELETE SET NULL,
    text TEXT
);
CREATE INDEX IF NOT EXISTS idx_notes_game_id ON notes(game_id);
CREATE INDEX IF NOT EXISTS idx_notes_user_id ON notes(user_id);

-- ========== reviews ==========
CREATE TABLE IF NOT EXISTS reviews (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rating INTEGER NOT NULL,
    comment TEXT
);
CREATE INDEX IF NOT EXISTS idx_reviews_game_id ON reviews(game_id);
CREATE INDEX IF NOT EXISTS idx_reviews_user_id ON reviews(user_id);

-- ========== photos ==========
CREATE TABLE IF NOT EXISTS photos (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    level_id INTEGER REFERENCES levels(id) ON DELETE SET NULL,
    path TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_photos_game_id ON photos(game_id);
CREATE INDEX IF NOT EXISTS idx_photos_user_id ON photos(user_id);

-- ========== player_ratings ==========
CREATE TABLE IF NOT EXISTS player_ratings (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    score INTEGER DEFAULT 0,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (user_id)
);

-- ========== follows ==========
CREATE TABLE IF NOT EXISTS follows (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    follower_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE(follower_id, author_id)
);
CREATE INDEX IF NOT EXISTS idx_follows_follower_id ON follows(follower_id);
CREATE INDEX IF NOT EXISTS idx_follows_author_id ON follows(author_id);

-- ========== chat_rooms ==========
CREATE TABLE IF NOT EXISTS chat_rooms (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_id INTEGER REFERENCES games(id) ON DELETE CASCADE,
    team_id INTEGER REFERENCES teams(id) ON DELETE CASCADE,
    passing_id INTEGER REFERENCES game_passings(id) ON DELETE CASCADE,
    name TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_rooms_game_id ON chat_rooms(game_id);
CREATE INDEX IF NOT EXISTS idx_chat_rooms_team_id ON chat_rooms(team_id);
CREATE INDEX IF NOT EXISTS idx_chat_rooms_passing_id ON chat_rooms(passing_id);

-- ========== chat_messages ==========
CREATE TABLE IF NOT EXISTS chat_messages (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    room_id INTEGER NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_messages_room_id ON chat_messages(room_id);
CREATE INDEX IF NOT EXISTS idx_chat_messages_user_id ON chat_messages(user_id);
CREATE INDEX IF NOT EXISTS idx_chat_messages_created_at ON chat_messages(created_at);

-- ========== blackbox_voting_sessions ==========
CREATE TABLE IF NOT EXISTS blackbox_voting_sessions (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_passing_id INTEGER NOT NULL REFERENCES game_passings(id) ON DELETE CASCADE,
    level_id INTEGER NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    is_open BOOLEAN DEFAULT TRUE,
    winner_option TEXT,
    UNIQUE(game_passing_id, level_id)
);
CREATE INDEX IF NOT EXISTS idx_blackbox_voting_sessions_game_passing_id ON blackbox_voting_sessions(game_passing_id);
CREATE INDEX IF NOT EXISTS idx_blackbox_voting_sessions_level_id ON blackbox_voting_sessions(level_id);

-- ========== blackbox_votes ==========
CREATE TABLE IF NOT EXISTS blackbox_votes (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    session_id INTEGER NOT NULL REFERENCES blackbox_voting_sessions(id) ON DELETE CASCADE,
    voter_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    option TEXT NOT NULL,
    UNIQUE(session_id, voter_id)
);
CREATE INDEX IF NOT EXISTS idx_blackbox_votes_session_id ON blackbox_votes(session_id);
CREATE INDEX IF NOT EXISTS idx_blackbox_votes_voter_id ON blackbox_votes(voter_id);

-- ========== logs ==========
CREATE TABLE IF NOT EXISTS logs (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    game_passing_id INTEGER NOT NULL REFERENCES game_passings(id) ON DELETE CASCADE,
    level_id INTEGER REFERENCES levels(id) ON DELETE SET NULL,
    message TEXT
);
CREATE INDEX IF NOT EXISTS idx_logs_game_passing_id ON logs(game_passing_id);
CREATE INDEX IF NOT EXISTS idx_logs_level_id ON logs(level_id);

-- ========== tournaments ==========
CREATE TABLE IF NOT EXISTS tournaments (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    name TEXT NOT NULL,
    description TEXT,
    author_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    points_for_first INTEGER DEFAULT 10,
    points_for_second INTEGER DEFAULT 7,
    points_for_third INTEGER DEFAULT 5,
    points_for_participation INTEGER DEFAULT 2
);
CREATE INDEX IF NOT EXISTS idx_tournaments_author_id ON tournaments(author_id);
CREATE INDEX IF NOT EXISTS idx_tournaments_deleted_at ON tournaments(deleted_at);

-- ========== tournament_games ==========
CREATE TABLE IF NOT EXISTS tournament_games (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    tournament_id INTEGER NOT NULL REFERENCES tournaments(id) ON DELETE CASCADE,
    game_id INTEGER NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    order_index INTEGER DEFAULT 0,
    UNIQUE(tournament_id, game_id)
);
CREATE INDEX IF NOT EXISTS idx_tournament_games_tournament_id ON tournament_games(tournament_id);
CREATE INDEX IF NOT EXISTS idx_tournament_games_game_id ON tournament_games(game_id);

-- ========== tournament_teams ==========
CREATE TABLE IF NOT EXISTS tournament_teams (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    tournament_id INTEGER NOT NULL REFERENCES tournaments(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    UNIQUE(tournament_id, team_id)
);
CREATE INDEX IF NOT EXISTS idx_tournament_teams_tournament_id ON tournament_teams(tournament_id);
CREATE INDEX IF NOT EXISTS idx_tournament_teams_team_id ON tournament_teams(team_id);

-- ========== tournament_results ==========
CREATE TABLE IF NOT EXISTS tournament_results (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    tournament_id INTEGER NOT NULL REFERENCES tournaments(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    score INTEGER DEFAULT 0,
    games_played INTEGER DEFAULT 0,
    UNIQUE(tournament_id, team_id)
);
CREATE INDEX IF NOT EXISTS idx_tournament_results_tournament_id ON tournament_results(tournament_id);
CREATE INDEX IF NOT EXISTS idx_tournament_results_team_id ON tournament_results(team_id);

-- ========== audit_logs ==========
CREATE TABLE IF NOT EXISTS audit_logs (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id INTEGER NOT NULL,
    details TEXT
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_object_type ON audit_logs(object_type);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at);

-- ========== backups ==========
CREATE TABLE IF NOT EXISTS backups (
    id SERIAL PRIMARY KEY,
    filename TEXT NOT NULL,
    file_path TEXT NOT NULL,
    size BIGINT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- ========== email_queues ==========
CREATE TABLE IF NOT EXISTS email_queues (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE,
    to_address TEXT NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    attempts INTEGER DEFAULT 0,
    last_error TEXT,
    scheduled_at TIMESTAMP WITH TIME ZONE,
    sent_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_email_queues_status_created ON email_queues(status, created_at);
CREATE INDEX IF NOT EXISTS idx_email_queues_scheduled_at ON email_queues(scheduled_at);
CREATE INDEX IF NOT EXISTS idx_email_queues_deleted_at ON email_queues(deleted_at);

-- ========== refresh_tokens ==========
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    device_id TEXT,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    revoked_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_revoked_at ON refresh_tokens(revoked_at);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at ON refresh_tokens(expires_at);

-- ========== notification_settings ==========
CREATE TABLE IF NOT EXISTS notification_settings (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    settings_json TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_notification_settings_user_id ON notification_settings(user_id);
