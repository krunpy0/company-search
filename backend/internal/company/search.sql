WITH sme AS NOT MATERIALIZED (
    SELECT r.reg_no, r.inn, r.kind, s.display_name, s.as_of, s.release_id,
           s.source_doc_id, s.source_file, NULL::date AS document_date
    FROM published_releases p
    JOIN sme_observations s ON s.release_id = p.release_id
    JOIN registrations r ON r.reg_no = s.reg_no
    WHERE p.dataset_code = 'rsmp' AND p.period_key = 'current'
), finance AS (
    SELECT f.*
    FROM published_releases p
    JOIN financials f ON f.release_id = p.release_id
    WHERE p.dataset_code = 'revexp' AND $1 <> 'registration' AND $3 <> 'entrepreneur'
      AND (($1 = 'inn' AND f.inn = $2)
           OR ($1 = 'name' AND company_search_name(f.display_name) % company_search_name($2)))
), ip_regimes AS NOT MATERIALIZED (
    SELECT o.reg_no, o.inn, o.display_name, o.as_of, o.release_id,
           o.source_doc_id, o.source_file, o.document_date
    FROM tax_regime_observations o
    WHERE o.release_id = (
        SELECT release_id FROM published_releases
        WHERE dataset_code = 'snrip' ORDER BY period_key DESC LIMIT 1
    ) AND o.reg_no IS NOT NULL
), legal_observations AS (
    SELECT e.inn, e.display_name, e.as_of, e.release_id, e.source_doc_id, e.source_file, e.document_date
    FROM published_releases p JOIN employee_observations e ON e.release_id = p.release_id
    WHERE p.dataset_code = 'sshr2019' AND $1 = 'inn' AND $3 <> 'entrepreneur' AND e.inn = $2
    UNION ALL
    SELECT o.inn, o.display_name, o.as_of, o.release_id, o.source_doc_id, o.source_file, o.document_date
    FROM tax_regime_observations o
    WHERE o.release_id = (
        SELECT release_id FROM published_releases WHERE dataset_code = 'snr' ORDER BY period_key DESC LIMIT 1
    ) AND $1 = 'inn' AND $3 <> 'entrepreneur' AND o.reg_no IS NULL AND o.inn = $2
    UNION ALL
    SELECT t.inn, NULL::text, t.as_of, t.release_id, t.source_doc_id, t.source_file, t.document_date
    FROM published_releases p
    CROSS JOIN LATERAL (
        SELECT * FROM tax_payments WHERE release_id = p.release_id AND inn = $2
        ORDER BY source_doc_id, line_no LIMIT 1
    ) t
    WHERE p.dataset_code = 'paytax' AND $1 = 'inn' AND $3 <> 'entrepreneur'
    UNION ALL
    SELECT t.inn, NULL::text, t.as_of, t.release_id, t.source_doc_id, t.source_file, t.document_date
    FROM published_releases p
    CROSS JOIN LATERAL (
        SELECT * FROM tax_debts WHERE release_id = p.release_id AND inn = $2
        ORDER BY source_doc_id, line_no LIMIT 1
    ) t
    WHERE p.dataset_code = 'debtam' AND $1 = 'inn' AND $3 <> 'entrepreneur'
), matches AS (
    SELECT 'reg:' || reg_no AS id, reg_no, inn, kind, display_name, as_of, release_id,
           source_doc_id, source_file, document_date, CASE WHEN display_name IS NULL THEN 2 ELSE 0 END AS priority
    FROM sme
    WHERE ($3 = '' OR kind = $3)
      AND (($1 = 'inn' AND inn = $2) OR ($1 = 'registration' AND reg_no = $2)
           OR ($1 = 'name' AND company_search_name(display_name) % company_search_name($2)))
    UNION ALL
    SELECT coalesce('reg:' || s.reg_no, 'inn:' || f.inn), s.reg_no, f.inn, 'legal',
           f.display_name,
           f.as_of,
           f.release_id,
           f.source_doc_id,
           f.source_file,
           f.document_date,
           1
    FROM finance f
    LEFT JOIN LATERAL (SELECT * FROM sme WHERE inn = f.inn LIMIT ALL) s ON true
    UNION ALL
    SELECT 'reg:' || o.reg_no, o.reg_no, o.inn, 'entrepreneur', NULLIF(o.display_name, ''),
           o.as_of, o.release_id, o.source_doc_id, o.source_file, o.document_date, 3
    FROM ip_regimes o
    WHERE $3 <> 'legal'
      AND (($1 = 'inn' AND o.inn = $2) OR ($1 = 'registration' AND o.reg_no = $2))
    UNION ALL
    SELECT coalesce('reg:' || s.reg_no, 'inn:' || o.inn), s.reg_no, o.inn, 'legal',
           o.display_name, o.as_of, o.release_id, o.source_doc_id, o.source_file, o.document_date, 3
    FROM legal_observations o
    LEFT JOIN LATERAL (SELECT * FROM sme WHERE inn = o.inn LIMIT ALL) s ON true
), ranked AS (
    SELECT *, CASE
        WHEN $1 <> 'name' THEN 0
        WHEN lower(display_name) = lower($2) THEN -10001
        ELSE -(10000 * similarity(company_search_name(display_name), company_search_name($2)))::int
    END AS rank
    FROM matches
), best AS (
    SELECT DISTINCT ON (id) *
    FROM ranked
    ORDER BY id, rank, priority, as_of DESC, release_id DESC
), page AS (
    SELECT *
    FROM best
    WHERE (rank, id) > ($4::int, $5::text)
    ORDER BY rank, id
    LIMIT $6::int
)
SELECT page.id, page.reg_no, page.inn, page.kind, page.display_name, page.as_of::text, page.release_id,
       page.source_doc_id, page.source_file, page.document_date::text, page.rank,
       coalesce(s.release_id, ip.release_id), coalesce(s.as_of, ip.as_of)::text,
       coalesce(s.source_doc_id, ip.source_doc_id), coalesce(s.source_file, ip.source_file),
       CASE WHEN s.release_id IS NULL THEN ip.document_date::text END
FROM page
LEFT JOIN LATERAL (SELECT * FROM sme WHERE reg_no = page.reg_no LIMIT ALL) s ON true
LEFT JOIN ip_regimes ip ON s.reg_no IS NULL AND ip.reg_no = page.reg_no AND $1 <> 'name'
ORDER BY page.rank, page.id
