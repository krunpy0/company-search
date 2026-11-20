package main

import (
	"archive/zip"
	"context"
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
