package main

import (
	"archive/zip"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/krunpy0/company-search/backend/internal/fns"
)

func loadEmploymentTax(ctx context.Context, tx pgx.Tx, z *zip.Reader, dataset string, published time.Time, releaseID, runID int64) (int64, string, error) {
	if dataset == "sshr2019" {
		var period string
		columns := []string{"release_id", "inn", "display_name", "report_year", "employees", "as_of", "document_date", "source_doc_id", "source_file"}
		rows, err := copyBatches(ctx, tx, "employee_observations", columns, runID, func(yield func([]any) error) error {
			return fns.ReadEmploymentTax(ctx, z, dataset, published, func(row fns.EmploymentTaxObservation) error {
				year := row.AsOf.Year()
				if period == "" {
					period = strconv.Itoa(year)
				}
				return yield([]any{releaseID, row.INN, row.Name, int16(year), row.Employees, row.AsOf, row.DocumentDate, row.DocumentID, row.File})
			})
		})
		return rows, period, err
	}

	table := "tax_regime_observations"
	if dataset == "snrip" {
		table = "entrepreneur_regime_stage"
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE entrepreneur_regime_stage (LIKE tax_regime_observations)
			ON COMMIT DROP`); err != nil {
			return 0, "", err
		}
	}
	var period string
	columns := []string{"release_id", "inn", "reg_no", "display_name", "regimes", "as_of", "document_date", "source_doc_id", "source_file"}
	rows, err := copyBatches(ctx, tx, table, columns, runID, func(yield func([]any) error) error {
		return fns.ReadEmploymentTax(ctx, z, dataset, published, func(row fns.EmploymentTaxObservation) error {
			if period == "" {
				period = row.AsOf.Format(time.DateOnly)
			}
			return yield([]any{releaseID, row.INN, row.Registration, row.Name, row.Regimes, row.AsOf, row.DocumentDate, row.DocumentID, row.File})
		})
	})
	if err != nil || dataset != "snrip" {
		return rows, period, err
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE entrepreneur_regime_stage ADD PRIMARY KEY (reg_no)`); err != nil {
		return rows, period, err
	}
	slog.Info("merging entrepreneur registrations", "run_id", runID)
	if _, err := tx.Exec(ctx, `INSERT INTO registrations (reg_no, kind, inn)
		SELECT reg_no, 'entrepreneur', inn FROM entrepreneur_regime_stage ORDER BY reg_no
		ON CONFLICT (reg_no) DO NOTHING`); err != nil {
		return rows, period, err
	}
	quarantined, err := tx.Exec(ctx, `
		INSERT INTO tax_regime_quarantine
			(release_id, reg_no, inn, sme_inn, sme_release_id, display_name, regimes, as_of, document_date, source_doc_id, source_file)
		SELECT s.release_id, s.reg_no, s.inn, r.inn, o.release_id, s.display_name, s.regimes, s.as_of, s.document_date, s.source_doc_id, s.source_file
		FROM published_releases p JOIN sme_observations o ON o.release_id = p.release_id
		JOIN registrations r USING (reg_no)
		JOIN entrepreneur_regime_stage s USING (reg_no)
		WHERE p.dataset_code = 'rsmp' AND p.period_key = 'current'
		  AND r.kind = 'entrepreneur' AND s.inn <> r.inn`)
	if err != nil {
		return rows, period, err
	}
	slog.Info("merging entrepreneur regime observations", "run_id", runID, "quarantined", quarantined.RowsAffected())
	inserted, err := tx.Exec(ctx, `INSERT INTO tax_regime_observations
		(release_id, inn, reg_no, display_name, regimes, as_of, document_date, source_doc_id, source_file)
		SELECT s.release_id, s.inn, s.reg_no, s.display_name, s.regimes, s.as_of, s.document_date, s.source_doc_id, s.source_file
		FROM entrepreneur_regime_stage s JOIN registrations r USING (reg_no, inn)
		WHERE r.kind = 'entrepreneur' ORDER BY s.reg_no`)
	if err != nil {
		return rows, period, err
	}
	if inserted.RowsAffected()+quarantined.RowsAffected() != rows {
		return rows, period, fmt.Errorf("entrepreneur tax registration conflicts outside the current SME release")
	}
	return rows, period, nil
}
