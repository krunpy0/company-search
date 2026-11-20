package main

import (
	"archive/zip"
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/krunpy0/company-search/backend/internal/fns"
)

func loadMoneyTax(ctx context.Context, tx pgx.Tx, z *zip.Reader, dataset string, published time.Time, releaseID, runID int64) (int64, string, error) {
	table := "tax_payments"
	columns := []string{"release_id", "inn", "tax_name", "as_of", "document_date", "source_doc_id", "source_file", "line_no"}
	if dataset == "paytax" {
		columns = append(columns, "amount")
	} else {
		table = "tax_debts"
		columns = append(columns, "principal", "penalties", "fines", "total")
	}
	var period string
	rows, err := copyBatches(ctx, tx, table, columns, runID, func(yield func([]any) error) error {
		return fns.ReadMoneyTax(ctx, z, dataset, published, func(doc fns.MoneyTaxDocument) error {
			if period == "" {
				period = doc.AsOf.Format(time.DateOnly)
				if dataset == "paytax" {
					period = strconv.Itoa(doc.AsOf.Year())
				}
			}
			for i, line := range doc.Lines {
				row := []any{releaseID, doc.INN, line.Name, doc.AsOf, doc.DocumentDate, doc.ID, doc.File, int32(i + 1)}
				if dataset == "paytax" {
					row = append(row, line.Amount)
				} else {
					row = append(row, line.Principal, line.Penalties, line.Fines, line.Total)
				}
				if err := yield(row); err != nil {
					return err
				}
			}
			return nil
		})
	})
	return rows, period, err
}
