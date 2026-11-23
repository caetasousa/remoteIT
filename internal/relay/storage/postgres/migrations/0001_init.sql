CREATE TABLE users (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL CHECK (role IN ('admin', 'technician')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Só o SHA-256 do token fica no banco; o token em claro vive apenas no cookie.
CREATE TABLE sessions (
    token_hash BYTEA       PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- username é copiado para o registro continuar legível depois que o usuário
-- for removido (user_id vira NULL).
CREATE TABLE access_log (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    username    TEXT        NOT NULL,
    agent_id    TEXT        NOT NULL,
    host        TEXT        NOT NULL,
    remote_addr TEXT        NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL,
    ended_at    TIMESTAMPTZ
);
CREATE INDEX access_log_started_at_idx ON access_log (started_at DESC);
