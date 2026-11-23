CREATE TABLE IF NOT EXISTS alert_events (
    event_key text PRIMARY KEY,
    incident_key text NOT NULL,
    fingerprint text NOT NULL,
    status text NOT NULL CHECK (status IN ('firing', 'resolved')),
    team text NOT NULL,
    service text NOT NULL,
    severity text NOT NULL CHECK (severity IN ('info', 'warning', 'page')),
    alert jsonb NOT NULL,
    service_metadata jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notification_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_key text NOT NULL REFERENCES alert_events(event_key),
    strategy text NOT NULL,
    destination text NOT NULL,
    stage text NOT NULL CHECK (stage IN ('primary', 'escalation', 'acknowledgement')),
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'delivered', 'failed')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    last_error text,
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_key, strategy, destination, stage)
);
CREATE INDEX IF NOT EXISTS notification_outbox_claim_idx ON notification_outbox(status, next_attempt_at, locked_until);

CREATE TABLE IF NOT EXISTS escalations (
    event_key text PRIMARY KEY REFERENCES alert_events(event_key),
    state text NOT NULL CHECK (state IN ('pending', 'acknowledged', 'escalated', 'resolved')),
    due_at timestamptz NOT NULL,
    acknowledged_at timestamptz,
    acknowledged_by text,
    escalated_at timestamptz,
    resolved_at timestamptz,
    alert jsonb NOT NULL,
    service_metadata jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS escalations_due_idx ON escalations(state, due_at);

CREATE TABLE IF NOT EXISTS delivery_rate_limits (
    destination text NOT NULL,
    minute_bucket timestamptz NOT NULL,
    count integer NOT NULL,
    PRIMARY KEY (destination, minute_bucket)
);

CREATE TABLE IF NOT EXISTS silences (
    id text PRIMARY KEY,
    service text NOT NULL,
    team text NOT NULL,
    created_by text NOT NULL,
    reason text NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS silences_active_idx ON silences(service, starts_at, ends_at);

CREATE TABLE IF NOT EXISTS audit_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    event_type text NOT NULL,
    actor text NOT NULL,
    team text,
    service text,
    alert_fingerprint text,
    strategy text,
    destination text,
    outcome text NOT NULL,
    detail jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS audit_log_lookup_idx ON audit_log(service, occurred_at DESC);
