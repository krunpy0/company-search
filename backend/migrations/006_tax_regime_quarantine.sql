CREATE TABLE tax_regime_quarantine (
    release_id bigint NOT NULL REFERENCES releases (id),
    reg_no text NOT NULL,
    inn text NOT NULL CHECK (inn ~ '^[0-9]{12}$'),
    sme_inn text NOT NULL CHECK (sme_inn ~ '^[0-9]{12}$' AND sme_inn <> inn),
    sme_release_id bigint NOT NULL,
    display_name text NOT NULL,
    regimes text[] NOT NULL,
    as_of date NOT NULL,
    document_date date NOT NULL,
    source_doc_id text NOT NULL,
    source_file text NOT NULL,
    PRIMARY KEY (release_id, reg_no),
    FOREIGN KEY (sme_release_id, reg_no) REFERENCES sme_observations (release_id, reg_no)
);
