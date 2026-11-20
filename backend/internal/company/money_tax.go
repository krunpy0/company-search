package company

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type MoneyTax struct {
	State      string         `json:"state"`
	Items      []MoneyTaxItem `json:"items"`
	NextOffset *int           `json:"next_offset"`
}

type MoneyTaxItem struct {
	TaxName  string       `json:"tax_name"`
	Currency string       `json:"currency"`
	Scope    string       `json:"scope"`
	Year     *int         `json:"year,omitempty"`
	Amount   *string      `json:"amount,omitempty"`
	Debt     *DebtAmounts `json:"debt,omitempty"`
	Evidence
}

type DebtAmounts struct {
	Principal *string `json:"principal"`
	Penalties *string `json:"penalties"`
	Fines     *string `json:"fines"`
	Total     string  `json:"total"`
}

func MoneyTaxes(ctx context.Context, tx pgx.Tx, subject Detail, dataset, period string, limit, offset int) (MoneyTax, error) {
	result := MoneyTax{State: "not_loaded", Items: []MoneyTaxItem{}}
	if subject.Kind != "legal" {
		result.State = "not_applicable"
		return result, nil
	}
	if dataset == "debtam" && period == "" {
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(period_key), '') FROM published_releases WHERE dataset_code = 'debtam'`).Scan(&period); err != nil {
			return result, err
		}
	}
	table := "tax_payments"
	amounts := "t.amount::text, NULL::text, NULL::text, NULL::text, NULL::text"
	if dataset == "debtam" {
		table = "tax_debts"
		amounts = "NULL::text, t.principal::text, t.penalties::text, t.fines::text, t.total::text"
	}
	from := ` FROM published_releases p JOIN ` + table + ` t ON t.release_id=p.release_id AND t.inn=$3
  WHERE p.dataset_code=$1 AND ($2='' OR p.period_key=$2)`
	rows, err := tx.Query(ctx, `SELECT extract(year FROM t.as_of)::integer, t.release_id, t.tax_name, t.as_of::text, t.document_date::text,
  t.source_doc_id, t.source_file, `+amounts+from+
		` ORDER BY p.period_key DESC, t.source_doc_id, t.line_no LIMIT $4 OFFSET $5`, dataset, period, subject.INN, limit+1, offset)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var release int64
		var year int
		var principal, penalties, fines, total *string
		row := MoneyTaxItem{Scope: "taxpayer", Currency: "RUB"}
		if err := rows.Scan(&year, &release, &row.TaxName, &row.AsOf, &row.DocumentDate, &row.DocumentID, &row.File,
			&row.Amount, &principal, &penalties, &fines, &total); err != nil {
			return result, err
		}
		row.SourceRef = sourceRef(release)
		if total != nil {
			row.Debt = &DebtAmounts{Principal: principal, Penalties: penalties, Fines: fines, Total: *total}
		} else {

			row.Year = &year
		}
		result.Items = append(result.Items, row)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if len(result.Items) > 0 {
		result.State = "found"
		if len(result.Items) > limit {
			next := offset + limit
			result.NextOffset = &next
			result.Items = result.Items[:limit]
		}
		return result, nil
	}
	if err := tx.QueryRow(ctx, `SELECT CASE
  WHEN EXISTS (SELECT 1`+from+`) THEN 'found'
  WHEN EXISTS (SELECT 1 FROM published_releases WHERE dataset_code=$1 AND ($2='' OR period_key=$2)) THEN 'not_published'
  ELSE 'not_loaded' END`, dataset, period, subject.INN).Scan(&result.State); err != nil {
		return result, err
	}
	return result, nil
}
