-- The append-only journal behind agent-runtime. Rows are never updated or deleted.
CREATE TABLE IF NOT EXISTS journal (
    job_id   text        NOT NULL,
    seq      bigint      NOT NULL,
    kind     text        NOT NULL,
    step_id  text        NOT NULL DEFAULT '',
    attempt  integer     NOT NULL DEFAULT 0,
    idem_key text        NOT NULL DEFAULT '',
    payload  jsonb       NOT NULL DEFAULT 'null',
    at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, seq)
);

-- Tool results are replayed by idempotency key, so the key must find its result quickly.
CREATE INDEX IF NOT EXISTS journal_idem_key ON journal (idem_key) WHERE idem_key <> '';

-- Enforce append-only at the database too.
CREATE OR REPLACE FUNCTION journal_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'journal rows are append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS journal_no_update ON journal;
CREATE TRIGGER journal_no_update BEFORE UPDATE OR DELETE ON journal
    FOR EACH ROW EXECUTE FUNCTION journal_append_only();
