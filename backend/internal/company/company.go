package company

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5"
)

//go:embed search.sql
var searchSQL string

//go:embed detail.sql
var detailSQL string

type Source struct {
	Publisher        string `json:"publisher"`
	Dataset          string `json:"dataset"`
	PublishedOn      string `json:"published_on"`
	Period           string `json:"period"`
	URL              string `json:"url"`
	DownloadURL      string `json:"download_url"`
	QuarantinedCount int64  `json:"quarantined_count,omitempty"`
}

type Evidence struct {
	SourceRef    string  `json:"source_ref"`
	AsOf         string  `json:"as_of"`
	DocumentID   string  `json:"document_id"`
	File         string  `json:"file"`
	DocumentDate *string `json:"document_date,omitempty"`
}

type Name struct {
	Value string `json:"value"`
	Evidence
}

type Identity struct {
	ID                   string    `json:"id"`
	Registration         *string   `json:"registration_number"`
	INN                  string    `json:"inn"`
	Kind                 string    `json:"kind"`
	Name                 *Name     `json:"name"`
	RegistrationEvidence *Evidence `json:"registration_evidence"`
}

type SME struct {
	Region       string  `json:"region_code"`
	OKVED        *string `json:"okved"`
	IncludedOn   string  `json:"included_on"`
	Category     string  `json:"category"`
	SubjectType  string  `json:"subject_type"`
	Employees    *int    `json:"employees"`
	EmployeeYear *int    `json:"employee_year"`
	Evidence
}

type Financial struct {
	Year        int    `json:"year"`
	DisplayName string `json:"display_name"`
	Income      string `json:"income"`
	Expense     string `json:"expense"`
	Currency    string `json:"currency"`
	Scope       string `json:"scope"`
	Evidence
}

type Detail struct {
	Identity
	SME *SME `json:"sme"`
}

type Match struct {
	Identity
	Rank int `json:"-"`
}

func Sources(ctx context.Context, tx pgx.Tx) (map[string]Source, string, error) {
	rows, err := tx.Query(ctx, `
        SELECT r.id, d.publisher, d.code, r.published_on::text, p.period_key, d.passport_url, r.download_url,
               CASE WHEN d.code = 'snrip' THEN (
                   SELECT count(*) FROM tax_regime_quarantine q WHERE q.release_id = r.id
               ) ELSE 0 END
        FROM published_releases p JOIN releases r ON r.id = p.release_id
        JOIN datasets d ON d.code = p.dataset_code
        WHERE (p.dataset_code = 'rsmp' AND p.period_key = 'current')
           OR p.dataset_code IN ('revexp', 'sshr2019', 'snr', 'snrip')
        ORDER BY r.id`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	sources := make(map[string]Source)
	var version string
	for rows.Next() {
		var id int64
		var s Source
		if err := rows.Scan(&id, &s.Publisher, &s.Dataset, &s.PublishedOn, &s.Period, &s.URL, &s.DownloadURL, &s.QuarantinedCount); err != nil {
			return nil, "", err
		}
		sources[sourceRef(id)] = s
		version += fmt.Sprintf("/%d", id)
	}
	return sources, version, rows.Err()
}

func sourceRef(id int64) string { return fmt.Sprintf("fns-%d", id) }

func Search(ctx context.Context, tx pgx.Tx, mode, query, kind string, rank int, after string, limit int) ([]Match, error) {
	if mode == "name" {
		// Weak trigram matches can fan out to thousands of unrelated companies.
		if _, err := tx.Exec(ctx, "SET LOCAL pg_trgm.similarity_threshold = 0.6"); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, searchSQL, pgx.QueryExecModeExec, mode, query, kind, rank, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Match, 0, limit)
	for rows.Next() {
		var m Match
		var name *string
		var source int64
		var evidence Evidence
		var regSource *int64
		var regAsOf, regDocument, regFile, regDate *string
		if err := rows.Scan(&m.ID, &m.Registration, &m.INN, &m.Kind, &name, &evidence.AsOf,
			&source, &evidence.DocumentID, &evidence.File, &evidence.DocumentDate, &m.Rank, &regSource, &regAsOf, &regDocument, &regFile, &regDate); err != nil {
			return nil, err
		}
		if name != nil {
			evidence.SourceRef = sourceRef(source)
			m.Name = &Name{Value: *name, Evidence: evidence}
		}
		if regSource != nil {
			m.RegistrationEvidence = &Evidence{SourceRef: sourceRef(*regSource), AsOf: *regAsOf, DocumentID: *regDocument, File: *regFile, DocumentDate: regDate}
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

func Resolve(ctx context.Context, tx pgx.Tx, mode, number string) ([]Detail, error) {
	rows, err := tx.Query(ctx, detailSQL, pgx.QueryExecModeExec, mode, number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var details []Detail
	for rows.Next() {
		var item Detail
		var name, region, included, category, subject, asOf, documentID, file, docDate *string
		var source *int64
		var sme SME
		if err := rows.Scan(&item.ID, &item.Registration, &item.INN, &item.Kind, &name, &region,
			&sme.OKVED, &included, &category, &subject, &sme.Employees, &sme.EmployeeYear,
			&asOf, &source, &documentID, &file, &docDate); err != nil {
			return nil, err
		}
		var evidence Evidence
		if source != nil {
			evidence = Evidence{SourceRef: sourceRef(*source), AsOf: *asOf, DocumentID: *documentID, File: *file, DocumentDate: docDate}
			if item.Registration != nil {
				item.RegistrationEvidence = &evidence
			}
		}
		if region != nil {
			sme.Evidence = evidence
			sme.Region, sme.IncludedOn, sme.Category, sme.SubjectType = *region, *included, *category, *subject
			item.SME = &sme
		}
		if name != nil {
			item.Name = &Name{Value: *name, Evidence: evidence}
		}
		details = append(details, item)
	}
	return details, rows.Err()
}

func Financials(ctx context.Context, tx pgx.Tx, inn string) ([]Financial, error) {
	rows, err := tx.Query(ctx, `
        SELECT f.report_year, f.display_name, f.income::text, f.expense::text, f.as_of::text,
               f.document_date::text, f.release_id, f.source_doc_id, f.source_file
        FROM published_releases p JOIN financials f ON f.release_id = p.release_id
        WHERE p.dataset_code = 'revexp' AND f.inn = $1
        ORDER BY f.report_year DESC`, inn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Financial, 0)
	for rows.Next() {
		item := Financial{Currency: "RUB", Scope: "taxpayer"}
		var source int64
		if err := rows.Scan(&item.Year, &item.DisplayName, &item.Income, &item.Expense, &item.AsOf,
			&item.DocumentDate, &source, &item.DocumentID, &item.File); err != nil {
			return nil, err
		}
		item.SourceRef = sourceRef(source)
		items = append(items, item)
	}
	return items, rows.Err()
}
