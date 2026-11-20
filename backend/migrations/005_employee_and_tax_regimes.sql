CREATE TABLE employee_observations (
    release_id bigint NOT NULL REFERENCES releases (id),
    inn text NOT NULL CHECK (inn ~ '^[0-9]{10}$'),
    display_name text NOT NULL,
    report_year smallint NOT NULL CHECK (report_year = extract(year FROM as_of)),
    employees integer NOT NULL CHECK (employees BETWEEN 0 AND 999999999),
    as_of date NOT NULL,
    document_date date NOT NULL CHECK (document_date >= as_of),
    source_doc_id text NOT NULL,
    source_file text NOT NULL,
    PRIMARY KEY (release_id, inn)
);

CREATE TABLE tax_regime_observations (
    release_id bigint NOT NULL REFERENCES releases (id),
    inn text NOT NULL,
    reg_no text REFERENCES registrations (reg_no),
    display_name text NOT NULL,
    regimes text[] NOT NULL,
    as_of date NOT NULL,
    document_date date NOT NULL CHECK (document_date >= as_of),
    source_doc_id text NOT NULL,
    source_file text NOT NULL,
    CHECK (
        (reg_no IS NULL AND inn ~ '^[0-9]{10}$'
            AND regimes <@ ARRAY['eshn', 'usn', 'ausn', 'srp'])
        OR (reg_no IS NOT NULL AND reg_no ~ '^[0-9]{15}$' AND inn ~ '^[0-9]{12}$'
            AND cardinality(regimes) > 0
            AND regimes <@ ARRAY['usn', 'ausn', 'eshn', 'psn', 'npd'])
    )
);

CREATE UNIQUE INDEX tax_regime_legal_observation_idx
    ON tax_regime_observations (release_id, inn) WHERE reg_no IS NULL;
CREATE UNIQUE INDEX tax_regime_entrepreneur_observation_idx
    ON tax_regime_observations (release_id, reg_no) WHERE reg_no IS NOT NULL;
CREATE INDEX tax_regime_entrepreneur_inn_idx
    ON tax_regime_observations (release_id, inn, reg_no) WHERE reg_no IS NOT NULL;
