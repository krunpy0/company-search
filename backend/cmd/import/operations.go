package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func expectedRelease(dataset string, now time.Time) time.Time {
	// FNS publication dates follow Moscow time.
	now = now.In(time.FixedZone("MSK", 3*60*60))
	year, month, today := now.Date()
	switch dataset {
	case "taxoffence":
		if month < time.December {
			year--
		}
		return time.Date(year, time.December, 1, 0, 0, 0, 0, time.UTC)
	case "rsmppp":
		if today < 15 {
			month--
		}
		return time.Date(year, month, 15, 0, 0, 0, 0, time.UTC)
	case "paytax":
		if month < time.April {
			year--
		}
		return time.Date(year, time.April, 1, 0, 0, 0, 0, time.UTC)
	case "rsmp":
		if today < 10 {
			month--
		}
		return time.Date(year, month, 10, 0, 0, 0, 0, time.UTC)
	default:
		if today < 25 {
			month--
		}
		return time.Date(year, month, 25, 0, 0, 0, 0, time.UTC)
	}
}

func lockDataset(ctx context.Context, dataset string) (*pgx.Conn, error) {
	lockID := additionalDatasets[dataset].lockID
	switch dataset {
	case revexp:
		lockID = 71002
	case "rsmp":
		lockID = 71003
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("could not connect to PostgreSQL")
	}
	var locked bool
	err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&locked)
	if err != nil || !locked {
		conn.Close(context.Background())
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%s operation is already running", dataset)
	}
	return conn, nil
}

func rollback(ctx context.Context, dataset string, current, previous int64) error {
	conn, err := lockDataset(ctx, dataset)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	var result pgconn.CommandTag
	if previous == 0 {
		// Date-keyed queries fall back to the last earlier published snapshot.
		if dataset != "snr" && dataset != "snrip" && dataset != "debtam" {
			return fmt.Errorf("a replacement release is required for %s", dataset)
		}
		result, err = conn.Exec(ctx, `DELETE FROM published_releases p
			WHERE p.dataset_code=$1 AND p.release_id=$2 AND EXISTS (
				SELECT 1 FROM published_releases older
				WHERE older.dataset_code=p.dataset_code AND older.period_key<p.period_key
			)`, dataset, current)
	} else {
		result, err = conn.Exec(ctx, `UPDATE published_releases p SET release_id=previous.id
			FROM releases previous
			WHERE p.dataset_code=$1 AND p.release_id=$2 AND previous.id=$3
				AND previous.dataset_code=p.dataset_code AND previous.period_key=p.period_key
				AND previous.status='published' AND previous.id<>p.release_id`, dataset, current, previous)
	}
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("rollback rejected: active release or eligible replacement not found")
	}
	slog.Info("release rolled back", "dataset", dataset, "removed_release_id", current, "replacement_release_id", previous)
	return nil
}
