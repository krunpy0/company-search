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
       s.release_id, s.as_of::text, s.source_doc_id, s.source_file
FROM page
LEFT JOIN LATERAL (SELECT * FROM sme WHERE reg_no = page.reg_no LIMIT ALL) s ON true
ORDER BY page.rank, page.id
