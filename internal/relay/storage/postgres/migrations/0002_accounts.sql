-- O padrão false mantém ativas as contas que já existiam.
ALTER TABLE users
    ADD COLUMN email   TEXT UNIQUE CHECK (email = lower(email)),
    ADD COLUMN pending BOOLEAN NOT NULL DEFAULT false;

-- Links de uso único enviados por e-mail. Como nas sessões, só o SHA-256 do
-- token é gravado. As linhas usadas ficam por dois dias para contar os limites.
CREATE TABLE account_tokens (
    token_hash BYTEA       PRIMARY KEY,
    kind       TEXT        NOT NULL CHECK (kind IN ('signup', 'reset', 'link')),
    email      TEXT        NOT NULL,
    user_id    BIGINT      REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    CHECK ((kind = 'signup') = (user_id IS NULL))
);
CREATE INDEX account_tokens_limits_idx ON account_tokens (kind, created_at);
