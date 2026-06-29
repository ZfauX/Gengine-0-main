-- Добавление индексов для часто запрашиваемых полей

-- Индексы для users
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON users(deleted_at);

-- Индексы для games
CREATE INDEX IF NOT EXISTS idx_games_author_id ON games(author_id);
CREATE INDEX IF NOT EXISTS idx_games_is_draft ON games(is_draft);
CREATE INDEX IF NOT EXISTS idx_games_visibility ON games(visibility);
CREATE INDEX IF NOT EXISTS idx_games_starts_at ON games(starts_at);
CREATE INDEX IF NOT EXISTS idx_games_registration_deadline ON games(registration_deadline);
CREATE INDEX IF NOT EXISTS idx_games_deleted_at ON games(deleted_at);

-- Индексы для levels
CREATE INDEX IF NOT EXISTS idx_levels_game_id ON levels(game_id);
CREATE INDEX IF NOT EXISTS idx_levels_parent_id ON levels(parent_id);
CREATE INDEX IF NOT EXISTS idx_levels_group_id ON levels(group_id);
CREATE INDEX IF NOT EXISTS idx_levels_deleted_at ON levels(deleted_at);

-- Индексы для questions
CREATE INDEX IF NOT EXISTS idx_questions_level_id ON questions(level_id);

-- Индексы для answers
CREATE INDEX IF NOT EXISTS idx_answers_question_id ON answers(question_id);

-- Индексы для teams
CREATE INDEX IF NOT EXISTS idx_teams_captain_id ON teams(captain_id);

-- Индексы для team_members
CREATE INDEX IF NOT EXISTS idx_team_members_user_id ON team_members(user_id);

-- Индексы для invitations
CREATE INDEX IF NOT EXISTS idx_invitations_team_id ON invitations(team_id);
CREATE INDEX IF NOT EXISTS idx_invitations_user_id ON invitations(user_id);
CREATE INDEX IF NOT EXISTS idx_invitations_status ON invitations(status);

-- Индексы для game_passings
CREATE INDEX IF NOT EXISTS idx_game_passings_game_id ON game_passings(game_id);
CREATE INDEX IF NOT EXISTS idx_game_passings_team_id ON game_passings(team_id);
CREATE INDEX IF NOT EXISTS idx_game_passings_status ON game_passings(status);
CREATE INDEX IF NOT EXISTS idx_game_passings_deleted_at ON game_passings(deleted_at);

-- Индексы для level_progresses
CREATE INDEX IF NOT EXISTS idx_level_progresses_game_passing_id ON level_progresses(game_passing_id);
CREATE INDEX IF NOT EXISTS idx_level_progresses_level_id ON level_progresses(level_id);
CREATE INDEX IF NOT EXISTS idx_level_progresses_deleted_at ON level_progresses(deleted_at);

-- Индексы для attempts
CREATE INDEX IF NOT EXISTS idx_attempts_level_progress_id ON attempts(level_progress_id);

-- Индексы для co_authors
CREATE INDEX IF NOT EXISTS idx_co_authors_game_id ON co_authors(game_id);
CREATE INDEX IF NOT EXISTS idx_co_authors_user_id ON co_authors(user_id);

-- Индексы для notes
CREATE INDEX IF NOT EXISTS idx_notes_game_id ON notes(game_id);
CREATE INDEX IF NOT EXISTS idx_notes_user_id ON notes(user_id);

-- Индексы для reviews
CREATE INDEX IF NOT EXISTS idx_reviews_game_id ON reviews(game_id);
CREATE INDEX IF NOT EXISTS idx_reviews_user_id ON reviews(user_id);

-- Индексы для photos
CREATE INDEX IF NOT EXISTS idx_photos_game_id ON photos(game_id);
CREATE INDEX IF NOT EXISTS idx_photos_user_id ON photos(user_id);

-- Индексы для follows
CREATE INDEX IF NOT EXISTS idx_follows_follower_id ON follows(follower_id);
CREATE INDEX IF NOT EXISTS idx_follows_author_id ON follows(author_id);

-- Индексы для chat_rooms
CREATE INDEX IF NOT EXISTS idx_chat_rooms_game_id ON chat_rooms(game_id);
CREATE INDEX IF NOT EXISTS idx_chat_rooms_team_id ON chat_rooms(team_id);
CREATE INDEX IF NOT EXISTS idx_chat_rooms_passing_id ON chat_rooms(passing_id);

-- Индексы для chat_messages
CREATE INDEX IF NOT EXISTS idx_chat_messages_room_id ON chat_messages(room_id);
CREATE INDEX IF NOT EXISTS idx_chat_messages_user_id ON chat_messages(user_id);
CREATE INDEX IF NOT EXISTS idx_chat_messages_created_at ON chat_messages(created_at);

-- Индексы для blackbox_voting_sessions
CREATE INDEX IF NOT EXISTS idx_blackbox_voting_sessions_game_passing_id ON blackbox_voting_sessions(game_passing_id);
CREATE INDEX IF NOT EXISTS idx_blackbox_voting_sessions_level_id ON blackbox_voting_sessions(level_id);

-- Индексы для blackbox_votes
CREATE INDEX IF NOT EXISTS idx_blackbox_votes_session_id ON blackbox_votes(session_id);
CREATE INDEX IF NOT EXISTS idx_blackbox_votes_voter_id ON blackbox_votes(voter_id);

-- Индексы для logs
CREATE INDEX IF NOT EXISTS idx_logs_game_passing_id ON logs(game_passing_id);
CREATE INDEX IF NOT EXISTS idx_logs_level_id ON logs(level_id);

-- Индексы для tournaments
CREATE INDEX IF NOT EXISTS idx_tournaments_author_id ON tournaments(author_id);
CREATE INDEX IF NOT EXISTS idx_tournaments_deleted_at ON tournaments(deleted_at);

-- Индексы для tournament_games
CREATE INDEX IF NOT EXISTS idx_tournament_games_tournament_id ON tournament_games(tournament_id);
CREATE INDEX IF NOT EXISTS idx_tournament_games_game_id ON tournament_games(game_id);

-- Индексы для tournament_teams
CREATE INDEX IF NOT EXISTS idx_tournament_teams_tournament_id ON tournament_teams(tournament_id);
CREATE INDEX IF NOT EXISTS idx_tournament_teams_team_id ON tournament_teams(team_id);

-- Индексы для tournament_results
CREATE INDEX IF NOT EXISTS idx_tournament_results_tournament_id ON tournament_results(tournament_id);
CREATE INDEX IF NOT EXISTS idx_tournament_results_team_id ON tournament_results(team_id);

-- Индексы для audit_logs
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_object_type ON audit_logs(object_type);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at);