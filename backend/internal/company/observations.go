package company

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type Headcount struct {
	Employees int    `json:"employees"`
	Year      int    `json:"year"`
	Scope     string `json:"scope"`
	Evidence
}

func Headcounts(ctx context.Context, tx pgx.Tx, item Detail) ([]Headcount, error) {
	items := make([]Headcount, 0)
	if item.SME != nil && item.SME.Employees != nil && item.SME.EmployeeYear != nil {
		items = append(items, Headcount{Employees: *item.SME.Employees, Year: *item.SME.EmployeeYear,
			Scope: "registration", Evidence: item.SME.Evidence})
	}
	if item.Kind != "legal" {
		return items, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT e.employees, e.report_year, e.as_of::text, e.document_date::text,
		       e.release_id, e.source_doc_id, e.source_file
		FROM published_releases p JOIN employee_observations e ON e.release_id = p.release_id
		WHERE p.dataset_code = 'sshr2019' AND e.inn = $1
		ORDER BY e.report_year DESC`, item.INN)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item := Headcount{Scope: "taxpayer"}
		var source int64
		if err := rows.Scan(&item.Employees, &item.Year, &item.AsOf, &item.DocumentDate,
			&source, &item.DocumentID, &item.File); err != nil {
			return nil, err
		}
		item.SourceRef = sourceRef(source)
		items = append(items, item)
	}
	return items, rows.Err()
}

type TaxRegimes struct {
	State string   `json:"state"`
	AsOf  *string  `json:"as_of"`
	Items []Regime `json:"items"`
}

type Regime struct {
	Codes []string `json:"codes"`
	Scope string   `json:"scope"`
	Evidence
}

func Regimes(ctx context.Context, tx pgx.Tx, item Detail, asOf string) (TaxRegimes, error) {
	result := TaxRegimes{State: "not_loaded", Items: []Regime{}}
	dataset, scope := "snr", "taxpayer"
	var registration *string
	if item.Kind == "entrepreneur" {
		dataset, scope, registration = "snrip", "registration", item.Registration
	}
	rows, err := tx.Query(ctx, `
		SELECT p.period_key, o.regimes, o.as_of::text, o.document_date::text,
		       o.release_id, o.source_doc_id, o.source_file, EXISTS (
		           SELECT 1 FROM tax_regime_quarantine q WHERE q.release_id = p.release_id AND q.reg_no = $4
		       )
		FROM (
			SELECT period_key, release_id FROM published_releases
			WHERE dataset_code = $1 AND ($2 = '' OR period_key = $2)
			ORDER BY period_key DESC LIMIT 1
		) p
		LEFT JOIN tax_regime_observations o ON o.release_id = p.release_id AND o.inn = $3
		    AND (($4::text IS NULL AND o.reg_no IS NULL) OR o.reg_no = $4)
	`, dataset, asOf, item.INN, registration)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var period string
		var codes []string
		var source *int64
		var quarantined bool
		var observed, documentDate, documentID, file *string
		if err := rows.Scan(&period, &codes, &observed, &documentDate, &source, &documentID, &file, &quarantined); err != nil {
			return result, err
		}
		result.AsOf, result.State = &period, "not_published"
		if quarantined {
			result.State = "identity_conflict"
		} else if source != nil {
			result.State = "found"
			result.Items = append(result.Items, Regime{Codes: codes, Scope: scope, Evidence: Evidence{
				SourceRef: sourceRef(*source), AsOf: *observed, DocumentDate: documentDate, DocumentID: *documentID, File: *file,
			}})
		}
	}
	return result, rows.Err()
}
