-- Máquinas com dono, compartilhamento e códigos de cadastro
-- (docs/adr/0010-maquinas-por-tecnico.md).

-- Código gravado no .exe baixado (kind = 'download') ou num link de
-- instalação (kind = 'link'). Como nas sessões, só o SHA-256 fica no banco.
CREATE TABLE enrollment_codes (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code_hash  BYTEA       NOT NULL UNIQUE,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT        NOT NULL CHECK (kind IN ('download', 'link')),
    edition    TEXT        NOT NULL CHECK (edition IN ('portatil', 'servico')),
    max_uses   INT         NOT NULL CHECK (max_uses > 0),
    uses       INT         NOT NULL DEFAULT 0 CHECK (uses <= max_uses),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX enrollment_codes_user_idx ON enrollment_codes (user_id, kind, created_at DESC);

-- credential_hash nulo = agente legado (chave compartilhada, ID = nome do
-- computador). owner_id nulo = sem dono: legado ou dono removido; só admins veem.
CREATE TABLE machines (
    id              TEXT        PRIMARY KEY,
    name            TEXT        NOT NULL,
    owner_id        BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    credential_hash BYTEA,
    enrolled_with   BIGINT      REFERENCES enrollment_codes (id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    last_seen_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);
CREATE INDEX machines_owner_idx ON machines (owner_id) WHERE revoked_at IS NULL;

CREATE TABLE machine_shares (
    machine_id TEXT        NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (machine_id, user_id)
);
CREATE INDEX machine_shares_user_idx ON machine_shares (user_id);
