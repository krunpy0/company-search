WITH sme AS NOT MATERIALIZED (
    SELECT r.inn, r.kind, s.*
    FROM published_releases p
    JOIN sme_observations s ON s.release_id = p.release_id
    JOIN registrations r USING (reg_no)
    WHERE p.dataset_code = 'rsmp' AND p.period_key = 'current'
      AND (($1 = 'reg' AND r.reg_no = $2) OR ($1 = 'inn' AND r.inn = $2))
), identity AS (
    SELECT 'reg:' || s.reg_no AS id, s.reg_no, s.inn, s.kind,
           s.display_name, s.region_code, s.okved, s.included_on, s.category, s.subject_type,
           s.employees, s.employee_year, s.as_of, s.release_id, s.source_doc_id, s.source_file
    FROM sme s
    UNION ALL
    SELECT 'inn:' || $2, NULL, $2, 'legal', NULL, NULL, NULL, NULL, NULL, NULL,
           NULL, NULL, NULL, NULL, NULL, NULL
    WHERE $1 = 'inn' AND length($2) = 10 AND NOT EXISTS (SELECT 1 FROM sme)
      AND EXISTS (
          SELECT 1 FROM published_releases p JOIN financials f ON f.release_id = p.release_id
          WHERE p.dataset_code = 'revexp' AND f.inn = $2
      )
)
SELECT id, reg_no, inn, kind, display_name, region_code, okved, included_on::text, category,
       subject_type, employees, employee_year, as_of::text, release_id, source_doc_id, source_file
FROM identity
ORDER BY id
