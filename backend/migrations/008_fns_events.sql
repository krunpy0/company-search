CREATE TABLE fns_events (
    release_id bigint NOT NULL REFERENCES releases(id),
    inn text NOT NULL CHECK (inn ~ '^[0-9]{10}([0-9]{2})?$'),
    reg_no text CHECK (reg_no ~ '^[0-9]{13}([0-9]{2})?$'),
    as_of date NOT NULL,
    event_on date NOT NULL,
    document_id text NOT NULL,
    source_file text NOT NULL,
    event_id text NOT NULL,
    details jsonb NOT NULL,
    PRIMARY KEY (release_id, document_id, event_id)
);

CREATE INDEX fns_events_by_inn ON fns_events (release_id, inn, event_on DESC);
CREATE INDEX fns_events_by_registration ON fns_events (release_id, reg_no, event_on DESC);
