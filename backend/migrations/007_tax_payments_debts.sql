CREATE TABLE tax_payments (
    release_id bigint NOT NULL REFERENCES releases (id),
    inn text NOT NULL CHECK (inn ~ '^[0-9]{10}$'),
    tax_name text NOT NULL,
    amount numeric NOT NULL,
    as_of date NOT NULL,
    document_date date NOT NULL CHECK (document_date >= as_of),
    source_doc_id text NOT NULL,
    source_file text NOT NULL,
    line_no integer NOT NULL,
    PRIMARY KEY (release_id, inn, source_doc_id, line_no)
);

CREATE TABLE tax_debts (
    release_id bigint NOT NULL REFERENCES releases (id),
    inn text NOT NULL CHECK (inn ~ '^[0-9]{10}$'),
    tax_name text NOT NULL,
    principal numeric,
    penalties numeric,
    fines numeric,
    total numeric NOT NULL,
    as_of date NOT NULL,
    document_date date NOT NULL CHECK (document_date >= as_of),
    source_doc_id text NOT NULL,
    source_file text NOT NULL,
    line_no integer NOT NULL,
    PRIMARY KEY (release_id, inn, source_doc_id, line_no)
);
