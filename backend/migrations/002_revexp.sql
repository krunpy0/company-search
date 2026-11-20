ALTER TABLE releases DROP CONSTRAINT releases_dataset_code_published_on_period_key_key;
ALTER TABLE releases ADD UNIQUE (dataset_code, published_on, period_key, sha256);
ALTER TABLE releases ADD UNIQUE (dataset_code, period_key, id);

CREATE TABLE published_releases (
    dataset_code text NOT NULL,
    period_key text NOT NULL,
    release_id bigint NOT NULL,
    PRIMARY KEY (dataset_code, period_key),
    FOREIGN KEY (dataset_code, period_key, release_id)
        REFERENCES releases (dataset_code, period_key, id)
);

CREATE TABLE financials (
    release_id bigint NOT NULL REFERENCES releases (id),
    inn text NOT NULL CHECK (inn ~ '^[0-9]{10}$'),
    display_name text NOT NULL,
    report_year smallint NOT NULL,
    as_of date NOT NULL,
    document_date date NOT NULL,
    source_doc_id text NOT NULL,
    source_file text NOT NULL,
    income numeric NOT NULL,
    expense numeric NOT NULL,
    PRIMARY KEY (release_id, inn)
);
