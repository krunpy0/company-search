package company

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// EventSubject validates a stable ID before querying the event sources.
func EventSubject(id string) (string, string, *ReadError) {
	if !validID(id) {
		return "", "", Rejected(400, "invalid_id", "Use inn:<INN> or reg:<OGRN/OGRNIP>").Error
	}
	mode, number, _ := strings.Cut(id, ":")
	if mode == "inn" && len(number) == 12 {
		return "", "", Rejected(400, "registration_required", "Use the exact OGRNIP for entrepreneur events").Error
	}
	return mode, number, nil
}

func (r Reader) FNSEvents(ctx context.Context, id, dataset string, page, limit int) (Result, error) {
	mode, number, invalid := EventSubject(id)
	if invalid != nil {
		return Result{Error: invalid}, nil
	}
	if dataset == "taxoffence" {
		if len(number) == 15 {
			return Result{Body: Body{"id": id, "state": "not_applicable"}}, nil
		}
		if mode != "inn" {
			return Rejected(400, "inn_required", "Use the company INN for tax offences"), nil
		}
	}
	return r.snapshot(ctx, func(ctx context.Context, s snapshot) (Result, error) {
		body := Body{"id": id, "state": "not_loaded", "dataset": dataset}
		var source Source
		var release int64
		err := s.QueryRow(ctx, `
			SELECT r.id, d.publisher, d.code, r.published_on::text, r.period_key, d.passport_url, r.download_url
			FROM published_releases p JOIN releases r ON r.id = p.release_id
			JOIN datasets d ON d.code = p.dataset_code
			WHERE p.dataset_code = $1
			ORDER BY r.published_on DESC LIMIT 1`, dataset).Scan(
			&release, &source.Publisher, &source.Dataset, &source.PublishedOn, &source.Period, &source.URL, &source.DownloadURL)
		if err == pgx.ErrNoRows {
			return Result{Body: body}, nil
		}
		if err != nil {
			return Result{}, err
		}
		ref := sourceRef(release)
		body["state"], body["period"] = "not_published", source.Period
		body["sources"] = map[string]Source{ref: source}
		rows, err := s.Query(ctx, `
			SELECT as_of::text, event_on::text, document_id, source_file, event_id, details
			FROM fns_events WHERE release_id = $1
			  AND (($2 = 'inn' AND inn = $3) OR ($2 = 'reg' AND reg_no = $3))
			ORDER BY event_on DESC, document_id, event_id OFFSET $4 LIMIT $5`,
			release, mode, number, (page-1)*limit, limit+1)
		if err != nil {
			return Result{}, err
		}
		defer rows.Close()
		items := make([]Body, 0, limit)
		for rows.Next() {
			if len(items) == limit {
				body["next_page"] = page + 1
				break
			}
			var asOf, date, document, file, event string
			var facts map[string]string
			if err := rows.Scan(&asOf, &date, &document, &file, &event, &facts); err != nil {
				return Result{}, err
			}
			item := Body{"dataset": dataset, "as_of": asOf, "source_ref": ref, "document_id": document, "file": file}
			if dataset == "rsmppp" {
				item["decision_on"], item["support_id"] = date, event
			}
			for key, value := range facts {
				item[key] = value
			}
			items = append(items, item)
		}
		if len(items) > 0 || page > 1 {
			body["state"] = "found"
		}
		body["items"] = items
		return Result{Body: body}, rows.Err()
	})
}
