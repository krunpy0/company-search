CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE FUNCTION company_search_name(name text) RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
RETURN btrim(regexp_replace(lower(btrim(name)),
    '^(общество с ограниченной ответственностью|публичное акционерное общество|акционерное общество|ооо|пао|ао)[[:space:]]+', ''),
    ' "«»');

CREATE INDEX sme_name_trgm_idx ON sme_observations USING gin (company_search_name(display_name) gin_trgm_ops);
CREATE INDEX financials_name_trgm_idx ON financials USING gin (company_search_name(display_name) gin_trgm_ops);
