-- Bootstrap schema for the postgres store backend.
-- Executed at startup; intentionally idempotent. If this schema ever needs
-- real evolution, switch to a migration tool (e.g. golang-migrate).

CREATE TABLE IF NOT EXISTS guardrails_rules (
    rule_id    TEXT PRIMARY KEY,
    rule       JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Rule IDs (builtin or custom) disabled via the configuration API.
CREATE TABLE IF NOT EXISTS guardrails_disabled_rules (
    rule_id    TEXT PRIMARY KEY,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS guardrails_settings (
    id         SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1), -- singleton row
    settings   JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- state is the statecodec payload: plain MaskingState JSON, or an AES-256-GCM
-- envelope {"_enc":"aes256gcm",...} when store encryption is enabled. Both
-- forms are valid JSON, so the column type stays JSONB either way.
CREATE TABLE IF NOT EXISTS guardrails_masking_state (
    request_id TEXT PRIMARY KEY,
    state      JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS guardrails_masking_state_expires_idx
    ON guardrails_masking_state (expires_at);

-- Per-request masking audit trail. The record JSONB document is the source
-- of truth; ts/model/path/rule_ids/data_types are denormalized copies that
-- feed list filtering and keyset pagination.
CREATE TABLE IF NOT EXISTS guardrails_audit (
    request_id TEXT PRIMARY KEY,
    ts         TIMESTAMPTZ NOT NULL,
    model      TEXT NOT NULL DEFAULT '',
    path       TEXT NOT NULL DEFAULT '',
    rule_ids   TEXT[] NOT NULL DEFAULT '{}',
    data_types INT[]  NOT NULL DEFAULT '{}',
    record     JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS guardrails_audit_ts_idx
    ON guardrails_audit (ts DESC, request_id DESC);
CREATE INDEX IF NOT EXISTS guardrails_audit_expires_idx
    ON guardrails_audit (expires_at);
CREATE INDEX IF NOT EXISTS guardrails_audit_rules_idx
    ON guardrails_audit USING GIN (rule_ids);
