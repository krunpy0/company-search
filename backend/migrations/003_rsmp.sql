CREATE TABLE registrations (
    reg_no text PRIMARY KEY,
    kind text NOT NULL,
    inn text NOT NULL,
    CHECK ((kind = 'legal' AND reg_no ~ '^[0-9]{13}$' AND inn ~ '^[0-9]{10}$')
        OR (kind = 'entrepreneur' AND reg_no ~ '^[0-9]{15}$' AND inn ~ '^[0-9]{12}$'))
);

CREATE INDEX registrations_inn_idx ON registrations (inn, reg_no);

-- The registry uses the same source document for its profile and SME facts.
CREATE TABLE sme_observations (
    release_id bigint NOT NULL REFERENCES releases (id),
    reg_no text NOT NULL REFERENCES registrations (reg_no),
    display_name text,
    region_code text NOT NULL,
    okved text,
    as_of date NOT NULL,
    included_on date NOT NULL CHECK (included_on <= as_of),
    category text NOT NULL CHECK (category IN ('1', '2', '3')),
    subject_type text NOT NULL CHECK (subject_type IN ('1', '2', '3')),
    employees integer CHECK (employees >= 0),
    employee_year smallint,
    source_doc_id text NOT NULL,
    source_file text NOT NULL,
    PRIMARY KEY (release_id, reg_no),
    CHECK ((employees IS NULL) = (employee_year IS NULL))
);
