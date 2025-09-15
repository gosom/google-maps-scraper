BEGIN;

-- 1) Foundation: Event types and pricing rules
CREATE TABLE IF NOT EXISTS billing_event_types (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT
);

-- Insert the 7 required event types
INSERT INTO billing_event_types (code, name, description) VALUES
    ('actor_start', 'Actor Start', 'Flat fee per scraping run initiation'),
    ('place_scraped', 'Place Scraped', 'Per successfully scraped place'),
    ('filters_applied', 'Filters Applied', 'Per filter per place'),
    ('additional_place_details', 'Additional Place Details', 'Extra data per place'),
    ('contact_details', 'Contact Details', 'Emails/social from websites per place'),
    ('review', 'Review', 'Per individual review scraped'),
    ('image', 'Image', 'Per image with metadata scraped')
ON CONFLICT (code) DO NOTHING;

-- Pricing rules with temporal versioning and A/B testing support
CREATE TABLE IF NOT EXISTS pricing_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type_code TEXT NOT NULL REFERENCES billing_event_types(code) ON DELETE RESTRICT,
    ab_test_group VARCHAR(50), -- NULL = default cohort
    valid_from TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    valid_to TIMESTAMP WITH TIME ZONE,
    price_credits NUMERIC(12,6) NOT NULL CHECK (price_credits > 0),
    price_usd NUMERIC(12,6) GENERATED ALWAYS AS (
        price_credits
    ) STORED
);

-- Coalesced AB group for correct NULL handling in constraints and indexes
ALTER TABLE pricing_rules
  ADD COLUMN IF NOT EXISTS ab_group_coalesced TEXT
    GENERATED ALWAYS AS (COALESCE(ab_test_group, '<DEFAULT_AB_GROUP>')) STORED;

-- Prevent overlapping active prices per event type and AB group (NULL-safe)
ALTER TABLE pricing_rules DROP CONSTRAINT IF EXISTS pricing_rules_no_overlap;
CREATE INDEX IF NOT EXISTS idx_pricing_rules_range ON pricing_rules
  USING gist (event_type_code, ab_group_coalesced, tstzrange(valid_from, COALESCE(valid_to, 'infinity'::timestamptz)));
ALTER TABLE pricing_rules
  ADD CONSTRAINT pricing_rules_no_overlap EXCLUDE USING gist (
    event_type_code WITH =,
    ab_group_coalesced WITH =,
    tstzrange(valid_from, COALESCE(valid_to, 'infinity'::timestamptz)) WITH &&
  );

-- Ensure at most one active price per (type, group)
CREATE UNIQUE INDEX IF NOT EXISTS uq_pricing_active_one ON pricing_rules(event_type_code, ab_group_coalesced)
  WHERE valid_to IS NULL;

-- Ensure version uniqueness per (type, group, valid_from)
CREATE UNIQUE INDEX IF NOT EXISTS uq_pricing_rule_version
  ON pricing_rules(event_type_code, ab_group_coalesced, valid_from);

-- Helpful partial index for active pricing lookups
CREATE INDEX IF NOT EXISTS idx_pricing_active_lookup ON pricing_rules(
    event_type_code, ab_group_coalesced, valid_from DESC
) WHERE valid_to IS NULL;

-- Seed initial pricing (idempotent - removed ON CONFLICT as per Solution 1)
INSERT INTO pricing_rules (event_type_code, ab_test_group, valid_from, price_credits)
SELECT 'actor_start', NULL, NOW(), 0.007000
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules
  WHERE event_type_code = 'actor_start' AND ab_group_coalesced = '<DEFAULT_AB_GROUP>' AND valid_to IS NULL
);

INSERT INTO pricing_rules (event_type_code, ab_test_group, valid_from, price_credits)
SELECT 'place_scraped', NULL, NOW(), 0.004000
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules
  WHERE event_type_code = 'place_scraped' AND ab_group_coalesced = '<DEFAULT_AB_GROUP>' AND valid_to IS NULL
);

INSERT INTO pricing_rules (event_type_code, ab_test_group, valid_from, price_credits)
SELECT 'filters_applied', NULL, NOW(), 0.001000
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules
  WHERE event_type_code = 'filters_applied' AND ab_group_coalesced = '<DEFAULT_AB_GROUP>' AND valid_to IS NULL
);

INSERT INTO pricing_rules (event_type_code, ab_test_group, valid_from, price_credits)
SELECT 'additional_place_details', NULL, NOW(), 0.002000
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules
  WHERE event_type_code = 'additional_place_details' AND ab_group_coalesced = '<DEFAULT_AB_GROUP>' AND valid_to IS NULL
);

INSERT INTO pricing_rules (event_type_code, ab_test_group, valid_from, price_credits)
SELECT 'contact_details', NULL, NOW(), 0.002000
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules
  WHERE event_type_code = 'contact_details' AND ab_group_coalesced = '<DEFAULT_AB_GROUP>' AND valid_to IS NULL
);

INSERT INTO pricing_rules (event_type_code, ab_test_group, valid_from, price_credits)
SELECT 'review', NULL, NOW(), 0.000500
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules
  WHERE event_type_code = 'review' AND ab_group_coalesced = '<DEFAULT_AB_GROUP>' AND valid_to IS NULL
);

INSERT INTO pricing_rules (event_type_code, ab_test_group, valid_from, price_credits)
SELECT 'image', NULL, NOW(), 0.000500
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules
  WHERE event_type_code = 'image' AND ab_group_coalesced = '<DEFAULT_AB_GROUP>' AND valid_to IS NULL
);

-- 2) Event System: immutable audit log of billable actions
CREATE TABLE IF NOT EXISTS billing_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    event_type_code TEXT NOT NULL REFERENCES billing_event_types(code) ON DELETE RESTRICT,
    occurred_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    unit_price_credits NUMERIC(12,6) NOT NULL CHECK (unit_price_credits > 0),
    total_price_credits NUMERIC(14,6) NOT NULL,
    pricing_rule_id UUID NOT NULL REFERENCES pricing_rules(id) ON DELETE RESTRICT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_events_idem ON billing_events(
    job_id, event_type_code, (metadata->>'idempotency_key')
) WHERE metadata ? 'idempotency_key';

CREATE OR REPLACE FUNCTION fn_billing_events_block_mod() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'billing_events are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_billing_events_block_update ON billing_events;
CREATE TRIGGER trg_billing_events_block_update
    BEFORE UPDATE ON billing_events
    FOR EACH ROW EXECUTE FUNCTION fn_billing_events_block_mod();

DROP TRIGGER IF EXISTS trg_billing_events_block_delete ON billing_events;
CREATE TRIGGER trg_billing_events_block_delete
    BEFORE DELETE ON billing_events
    FOR EACH ROW EXECUTE FUNCTION fn_billing_events_block_mod();

CREATE OR REPLACE FUNCTION fn_billing_events_before_insert() RETURNS trigger AS $$
DECLARE
    v_group VARCHAR(50);
    v_group_coalesced TEXT;
    v_rule pricing_rules%ROWTYPE;
BEGIN
    IF NEW.occurred_at IS NULL THEN
        NEW.occurred_at := NOW();
    END IF;

    IF NEW.quantity IS NULL OR NEW.quantity <= 0 THEN
        RAISE EXCEPTION 'quantity must be positive';
    END IF;

    v_group := NULLIF(NEW.metadata->>'ab_test_group', '');
    v_group_coalesced := COALESCE(v_group, '<DEFAULT_AB_GROUP>');

    SELECT r.* INTO v_rule
    FROM pricing_rules r
    WHERE r.event_type_code = NEW.event_type_code
      AND r.ab_group_coalesced = v_group_coalesced
      AND r.valid_from <= NEW.occurred_at
      AND (r.valid_to IS NULL OR r.valid_to > NEW.occurred_at)
    ORDER BY r.valid_from DESC
    LIMIT 1;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'No active pricing rule for event_type=% at time=% (group=%)', NEW.event_type_code, NEW.occurred_at, v_group;
    END IF;

    NEW.pricing_rule_id := COALESCE(NEW.pricing_rule_id, v_rule.id);
    NEW.unit_price_credits := COALESCE(NEW.unit_price_credits, v_rule.price_credits);
    NEW.total_price_credits := ROUND(NEW.unit_price_credits * NEW.quantity, 6);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_billing_events_before_insert ON billing_events;
CREATE TRIGGER trg_billing_events_before_insert
    BEFORE INSERT ON billing_events
    FOR EACH ROW EXECUTE FUNCTION fn_billing_events_before_insert();

CREATE TABLE IF NOT EXISTS job_cost_breakdown (
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    event_type_code TEXT NOT NULL REFERENCES billing_event_types(code) ON DELETE RESTRICT,
    quantity_total BIGINT NOT NULL DEFAULT 0 CHECK (quantity_total >= 0),
    cost_total_credits NUMERIC(18,6) NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_updated TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (job_id, event_type_code)
);

CREATE INDEX IF NOT EXISTS idx_job_cost_breakdown_job ON job_cost_breakdown(job_id);

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS estimated_cost_precise NUMERIC(18,6) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS actual_cost_precise NUMERIC(18,6) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS actual_cost INTEGER GENERATED ALWAYS AS (CAST(ROUND(actual_cost_precise) AS INTEGER)) STORED,
    ADD COLUMN IF NOT EXISTS billing_status TEXT NOT NULL DEFAULT 'pending' CHECK (billing_status IN ('pending','in_progress','billed','partial','refunded','failed'));

CREATE OR REPLACE FUNCTION fn_billing_events_after_insert() RETURNS trigger AS $$
BEGIN
    INSERT INTO job_cost_breakdown(job_id, event_type_code, quantity_total, cost_total_credits, last_updated)
    VALUES (NEW.job_id, NEW.event_type_code, NEW.quantity, NEW.total_price_credits, NOW())
    ON CONFLICT (job_id, event_type_code) DO UPDATE SET
        quantity_total = job_cost_breakdown.quantity_total + EXCLUDED.quantity_total,
        cost_total_credits = job_cost_breakdown.cost_total_credits + EXCLUDED.cost_total_credits,
        last_updated = NOW();

    UPDATE jobs j SET
        actual_cost_precise = COALESCE(j.actual_cost_precise, 0) + NEW.total_price_credits,
        billing_status = CASE WHEN j.billing_status IN ('pending','in_progress') THEN 'in_progress' ELSE j.billing_status END
    WHERE j.id = NEW.job_id;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_billing_events_after_insert ON billing_events;
CREATE TRIGGER trg_billing_events_after_insert
    AFTER INSERT ON billing_events
    FOR EACH ROW EXECUTE FUNCTION fn_billing_events_after_insert();

CREATE TABLE IF NOT EXISTS job_filters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    filter_type TEXT NOT NULL CHECK (filter_type IN ('category','rating','website','title_match','custom')),
    parameters JSONB NOT NULL DEFAULT '{}'::jsonb,
    places_affected INTEGER NOT NULL DEFAULT 0 CHECK (places_affected >= 0),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_job_filters_job ON job_filters(job_id);
CREATE INDEX IF NOT EXISTS idx_job_filters_user ON job_filters(user_id);

CREATE INDEX IF NOT EXISTS idx_billing_events_job_time ON billing_events(job_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_billing_events_user_time ON billing_events(user_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_billing_events_type_time ON billing_events(event_type_code, occurred_at DESC);

COMMENT ON TABLE pricing_rules IS 'Temporal pricing rules; valid_to is exclusive. 1 credit = $1.00.';
COMMENT ON COLUMN pricing_rules.price_credits IS 'Credits with 6-decimal precision; rounding policy: totals rounded to 6 decimals.';
COMMENT ON TABLE billing_events IS 'Immutable billing event log (event sourcing). Metadata keys: ab_test_group, idempotency_key.';
COMMENT ON FUNCTION fn_billing_events_before_insert() IS 'Resolves pricing at event time using ab_group_coalesced; computes total with 6-decimal rounding.';

COMMIT;