package main

import (
	"archive/zip"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/krunpy0/company-search/backend/internal/fns"
)

func loadRevexp(ctx context.Context, tx pgx.Tx, z *zip.Reader, published time.Time, year int, releaseID, runID int64) (int64, error) {
	columns := []string{"release_id", "inn", "display_name", "report_year", "as_of", "document_date", "source_doc_id", "source_file", "income", "expense"}
	return copyBatches(ctx, tx, "financials", columns, runID, func(yield func([]any) error) error {
		return fns.ReadRevexp(ctx, z, published, year, func(row fns.Financial) error {
			return yield([]any{releaseID, row.INN, row.Name, int16(year), row.AsOf, row.DocumentDate, row.DocumentID, row.File, row.Income, row.Expense})
		})
	})
}

func loadRSMP(ctx context.Context, tx pgx.Tx, z *zip.Reader, published time.Time, releaseID, runID int64) (int64, error) {
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE sme_stage (LIKE sme_observations, inn text NOT NULL, kind text NOT NULL)
		ON COMMIT DROP`); err != nil {
		return 0, err
	}
	columns := []string{"release_id", "reg_no", "display_name", "region_code", "okved", "as_of", "included_on", "category", "subject_type", "employees", "employee_year", "source_doc_id", "source_file", "inn", "kind"}
	rows, err := copyBatches(ctx, tx, "sme_stage", columns, runID, func(yield func([]any) error) error {
		return fns.ReadRSMP(ctx, z, published, func(row fns.SME) error {
			return yield([]any{releaseID, row.Number, row.Name, row.Region, row.OKVED, row.AsOf, row.Included, row.Category, row.SubjectType,
				row.Employees, row.EmployeeYear, row.DocumentID, row.File, row.INN, row.Kind})
		})
	})
	if err != nil {
		return rows, err
	}
	slog.Info("merging SME registrations", "run_id", runID, "rows", rows)
	if _, err := tx.Exec(ctx, `INSERT INTO registrations (reg_no, kind, inn)
		SELECT reg_no, kind, inn FROM sme_stage ORDER BY reg_no
		ON CONFLICT (reg_no) DO NOTHING`); err != nil {
		return rows, err
	}
	var conflict bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM sme_stage s JOIN registrations r USING (reg_no)
		WHERE (s.inn, s.kind) IS DISTINCT FROM (r.inn, r.kind)
	)`).Scan(&conflict); err != nil {
		return rows, err
	}
	if conflict {
		return rows, fmt.Errorf("SME registration has conflicting taxpayer identifiers")
	}
	slog.Info("merging SME observations", "run_id", runID)
	_, err = tx.Exec(ctx, `INSERT INTO sme_observations (release_id, reg_no, display_name, region_code, okved, as_of, included_on, category, subject_type, employees, employee_year, source_doc_id, source_file)
		SELECT release_id, reg_no, display_name, region_code, okved, as_of, included_on, category, subject_type,
		       employees, employee_year, source_doc_id, source_file
		FROM sme_stage ORDER BY reg_no`)
	return rows, err
}

func copyBatches(ctx context.Context, tx pgx.Tx, table string, columns []string, runID int64, read func(func([]any) error) error) (int64, error) {
	batch := make([][]any, 0, 5000)
	var rows int64
	flush := func() error {
		n, err := tx.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromRows(batch))
		if err != nil {
			return err
		}
		rows += n
		batch = batch[:0]
		if rows%100000 == 0 {
			slog.Info("import progress", "run_id", runID, "rows", rows)
		}
		return nil
	}
	err := read(func(row []any) error {
		batch = append(batch, row)
		if len(batch) == cap(batch) {
			return flush()
		}
		return nil
	})
	if err == nil && len(batch) > 0 {
		err = flush()
	}
	return rows, err
}
