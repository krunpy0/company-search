WITH sme AS NOT MATERIALIZED (
    SELECT r.inn, r.kind, s.*
    FROM published_releases p
    JOIN sme_observations s ON s.release_id = p.release_id
    JOIN registrations r USING (reg_no)
    WHERE p.dataset_code = 'rsmp' AND p.period_key = 'current'
      AND (($1 = 'reg' AND r.reg_no = $2) OR ($1 = 'inn' AND r.inn = $2))
), ip AS (
    SELECT DISTINCT ON (o.reg_no) o.*
    FROM published_releases p
    JOIN tax_regime_observations o ON o.release_id = p.release_id
    WHERE p.dataset_code = 'snrip'
      AND (($1 = 'reg' AND o.reg_no = $2)
           OR ($1 = 'inn' AND o.inn = $2 AND p.period_key = (
               SELECT max(period_key) FROM published_releases WHERE dataset_code = 'snrip'
           )))
      AND o.reg_no IS NOT NULL
    ORDER BY o.reg_no, o.as_of DESC
), legal_observations AS (
    SELECT e.display_name, e.as_of, e.release_id, e.source_doc_id, e.source_file, e.document_date
    FROM published_releases p JOIN employee_observations e ON e.release_id = p.release_id
    WHERE p.dataset_code = 'sshr2019' AND e.inn = $2
    UNION ALL
    SELECT o.display_name, o.as_of, o.release_id, o.source_doc_id, o.source_file, o.document_date
    FROM published_releases p JOIN tax_regime_observations o ON o.release_id = p.release_id
    WHERE p.dataset_code = 'snr' AND o.inn = $2 AND o.reg_no IS NULL
), identity AS (
    SELECT 'reg:' || s.reg_no AS id, s.reg_no, s.inn, s.kind,
           s.display_name, s.region_code, s.okved, s.included_on, s.category, s.subject_type,
           s.employees, s.employee_year, s.as_of, s.release_id, s.source_doc_id, s.source_file, NULL::date AS document_date
    FROM sme s
    UNION ALL
    SELECT 'reg:' || o.reg_no, o.reg_no, o.inn, 'entrepreneur', NULLIF(o.display_name, ''), NULL, NULL, NULL, NULL, NULL,
           NULL, NULL, o.as_of, o.release_id, o.source_doc_id, o.source_file, o.document_date
    FROM ip o
    WHERE NOT EXISTS (SELECT 1 FROM sme WHERE sme.reg_no = o.reg_no)
    UNION ALL
    SELECT 'inn:' || $2, NULL, $2, 'legal', o.display_name, NULL, NULL, NULL, NULL, NULL,
           NULL, NULL, o.as_of, o.release_id, o.source_doc_id, o.source_file, o.document_date
    FROM (SELECT 1) anchor
    LEFT JOIN LATERAL (
        SELECT * FROM legal_observations
        ORDER BY as_of DESC, release_id DESC LIMIT 1
    ) o ON true
    WHERE $1 = 'inn' AND length($2) = 10 AND NOT EXISTS (SELECT 1 FROM sme)
      AND (o.release_id IS NOT NULL OR EXISTS (
          SELECT 1 FROM published_releases p JOIN financials f ON f.release_id = p.release_id
          WHERE p.dataset_code = 'revexp' AND f.inn = $2
      ))
)
SELECT id, reg_no, inn, kind, display_name, region_code, okved, included_on::text, category,
       subject_type, employees, employee_year, as_of::text, release_id, source_doc_id, source_file, document_date::text
FROM identity
ORDER BY id
