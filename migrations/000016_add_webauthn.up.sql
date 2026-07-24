CREATE TABLE IF NOT EXISTS webauthn_credentials (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    user_id BIGINT NOT NULL REFERENCES users(id),
    credential_id BYTEA NOT NULL,
    public_key BYTEA NOT NULL,
    attestation_type TEXT NOT NULL DEFAULT '',
    transport TEXT[] NOT NULL DEFAULT '{}',
    aaguid BYTEA NOT NULL DEFAULT '\x00000000000000000000000000000000',
    sign_count BIGINT NOT NULL DEFAULT 0,
    backup_eligible BOOLEAN NOT NULL DEFAULT false,
    backup_state BOOLEAN NOT NULL DEFAULT false,
    name TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_webauthn_credentials_user ON webauthn_credentials(user_id);
CREATE UNIQUE INDEX idx_webauthn_credentials_cred_id ON webauthn_credentials(credential_id);
