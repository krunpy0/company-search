package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/krunpy0/company-search/backend/internal/fns"
)

const revexp = "revexp"
const passport = "https://www.nalog.gov.ru/rn27/opendata/7707329152-revexp/"
const baseURL = "https://file.nalog.ru/opendata/7707329152-revexp/"

var additionalDatasets = map[string]struct {
	schemaDate, version string
	lockID              int64
}{
	"sshr2019":   {"20200408", fns.HeadcountVersion, 71004},
	"snr":        {"20230425", fns.LegalRegimeVersion, 71005},
	"snrip":      {"20241025", fns.EntrepreneurRegimeVersion, 71006},
	"paytax":     {"20180110", fns.MoneyTaxVersion, 71007},
	"debtam":     {"20181201", fns.MoneyTaxVersion, 71008},
	"rsmppp":     {"20230615", fns.SupportVersion, 71009},
	"taxoffence": {"20191201", fns.OffenceVersion, 71010},
}

func main() {
	dataset := flag.String("dataset", revexp, "FNS dataset (revexp, rsmp, sshr2019, snr, snrip, paytax, debtam, rsmppp, taxoffence)")
	date := flag.String("published-on", "", "FNS release date (YYYY-MM-DD)")
	archive := flag.String("archive", "", "optional local copy of the official ZIP")
	check := flag.Bool("check", false, "import the latest expected release if not already imported")
	current := flag.Int64("rollback-current", 0, "expected active release ID to roll back")
	previous := flag.Int64("restore-release", 0, "replacement release ID for the same period (omit to unpublish the period)")
	flag.Parse()
	if *check && *date == "" && *archive == "" {
		*date = expectedRelease(*dataset, time.Now()).Format(time.DateOnly)
	}
	published, err := time.Parse(time.DateOnly, *date)
	_, additional := additionalDatasets[*dataset]
	rollbackMode := *current > 0 && *previous >= 0 && !*check && *date == "" && *archive == ""
	importMode := *current == 0 && *previous == 0 && err == nil && published.Year() >= 2019
	if (!rollbackMode && !importMode) || flag.NArg() != 0 || (*dataset != revexp && *dataset != "rsmp" && !additional) {
		flag.Usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if rollbackMode {
		err = rollback(ctx, *dataset, *current, *previous)
	} else {
		err = run(ctx, *dataset, published, *archive, *check)
	}
	if err != nil {
		slog.Error("operation failed", "dataset", *dataset, "published_on", *date, "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dataset string, published time.Time, archive string, check bool) (runErr error) {
	year := published.Year() - 1
	period := strconv.Itoa(year)
	maxZIP := int64(fns.MaxRevexpZIP)
	timeout := 3 * time.Minute
	passportURL, schemaURL, version := passport, baseURL+"structure-20180110.xsd", fns.RevexpVersion
	url := baseURL + "data-" + published.Format("20060102") + "-structure-20180110.zip"
	additionalSpec, additional := additionalDatasets[dataset]
	if dataset == "rsmp" {
		const rsmpBase = "https://file.nalog.ru/opendata/7707329152-rsmp/"
		period, maxZIP, timeout = "current", fns.MaxRSMPZIP, 30*time.Minute
		passportURL = "https://www.nalog.gov.ru/opendata/7707329152-rsmp/"
		schemaURL, version = rsmpBase+"structure-12052026.xsd", fns.RSMPVersion
		url = rsmpBase + "data-" + published.Format("02012006") + "-structure-12052026.zip"
	}
	if additional {
		base := "https://file.nalog.ru/opendata/7707329152-" + dataset + "/"
		period, maxZIP, timeout = "pending", 512<<20, 10*time.Minute
		passportURL = "https://www.nalog.gov.ru/opendata/7707329152-" + dataset + "/"
		schemaURL, version = base+"structure-"+additionalSpec.schemaDate+".xsd", additionalSpec.version
		url = base + "data-" + published.Format("20060102") + "-structure-" + additionalSpec.schemaDate + ".zip"
	}
	switch dataset {
	case "rsmppp":
		period, maxZIP, timeout = "current", 3<<30, 30*time.Minute
	case "taxoffence":
		period = strconv.Itoa(year)
		const base = "https://data.nalog.ru/opendata/7707329152-taxoffence/"
		url = base + "data-" + published.Format("20060102") + "-structure-20191201.zip"
		schemaURL = base + "structure-20181201.xsd"
	}
	conn, err := lockDataset(ctx, dataset)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	cadence := "monthly"
	if dataset == "paytax" || dataset == "taxoffence" {
		cadence = "yearly"
	}
	if _, err := conn.Exec(ctx, `INSERT INTO datasets VALUES ($1, 'ФНС России', $2, $3)
		ON CONFLICT (code) DO UPDATE SET expected_cadence=EXCLUDED.expected_cadence
		WHERE datasets.expected_cadence IS DISTINCT FROM EXCLUDED.expected_cadence`, dataset, passportURL, cadence); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `UPDATE import_runs SET status='failed', finished_at=now(), error_code='interrupted'
		WHERE dataset_code=$1 AND status='running'`, dataset); err != nil {
		return err
	}
	if check {
		var known bool
		err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM releases
			WHERE dataset_code=$1 AND published_on=$2 AND status='published')`, dataset, published).Scan(&known)
		if err != nil {
			return err
		}
		if known {
			slog.Info("expected release already imported", "dataset", dataset, "published_on", published.Format(time.DateOnly))
			return nil
		}
	}
	var runID int64
	if err := conn.QueryRow(ctx, "INSERT INTO import_runs (dataset_code,status) VALUES ($1,'running') RETURNING id", dataset).Scan(&runID); err != nil {
		return err
	}
	phase := "archive"
	var rows int64
	defer func() {
		if runErr == nil {
			return
		}
		switch {
		case errors.Is(runErr, context.Canceled):
			phase = "interrupted"
		case errors.Is(runErr, fns.ErrReleaseMissing):
			phase = "release_missing"
		case errors.Is(runErr, fns.ErrUnknownVersion):
			phase = "unknown_schema"
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := conn.Exec(cleanup, `UPDATE import_runs SET status='failed', finished_at=now(), error_code=$2, rows_seen=$3
			WHERE id=$1 AND status='running'`, runID, phase, rows)
		if err != nil {
			slog.Warn("could not record failure; next import can recover", "run_id", runID, "error", err)
		}
	}()
	var file *os.File
	if archive == "" {
		file, err = fns.DownloadZIP(ctx, url, maxZIP, timeout)
	} else {
		file, err = os.Open(archive)
	}
	if err != nil {
		return err
	}
	defer func() {
		file.Close()
		if archive == "" {
			os.Remove(file.Name())
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxZIP {
		return fmt.Errorf("invalid ZIP file size or type")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(file, 0, info.Size())); err != nil {
		return err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	var releaseID int64
	err = conn.QueryRow(ctx, `SELECT id FROM releases
		WHERE dataset_code=$1 AND published_on=$2 AND sha256=$3 AND status='published'`,
		dataset, published, digest).Scan(&releaseID)
	if err == nil {
		_, err := conn.Exec(ctx, `UPDATE import_runs SET status='succeeded', finished_at=now(), release_id=$2 WHERE id=$1`, runID, releaseID)
		if err == nil {
			slog.Info("release already imported", "dataset", dataset, "published_on", published.Format(time.DateOnly), "release_id", releaseID)
		}
		return err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	z, err := zip.NewReader(file, info.Size())
	if err != nil {
		return err
	}
	phase = "load"
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx.Rollback(cleanup)
	}()
	if err := tx.QueryRow(ctx, `INSERT INTO releases
		(dataset_code,published_on,period_key,download_url,schema_url,schema_version,sha256,size_bytes,status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'downloaded') RETURNING id`,
		dataset, published, period, url, schemaURL, version, digest, info.Size()).Scan(&releaseID); err != nil {
		return err
	}
	if dataset == "rsmp" {
		rows, err = loadRSMP(ctx, tx, z, published, releaseID, runID)
	} else if dataset == "paytax" || dataset == "debtam" {
		rows, period, err = loadMoneyTax(ctx, tx, z, dataset, published, releaseID, runID)
	} else if dataset == "rsmppp" || dataset == "taxoffence" {
		rows, err = loadEvents(ctx, tx, z, dataset, published, releaseID, runID)
	} else if additional {
		rows, period, err = loadEmploymentTax(ctx, tx, z, dataset, published, releaseID, runID)
	} else {
		rows, err = loadRevexp(ctx, tx, z, published, year, releaseID, runID)
	}
	if err != nil {
		return err
	}
	phase = "publish"
	if _, err := tx.Exec(ctx, `UPDATE releases SET status='published', validated_at=now(), period_key=$2 WHERE id=$1`, releaseID, period); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO published_releases VALUES ($1,$2,$3)
		ON CONFLICT (dataset_code, period_key) DO UPDATE SET release_id=EXCLUDED.release_id
		WHERE (SELECT published_on FROM releases WHERE id=published_releases.release_id) <= $4`,
		dataset, period, releaseID, published); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE import_runs SET status='succeeded', finished_at=now(), progress_at=now(), release_id=$2, rows_seen=$3
		WHERE id=$1`, runID, releaseID, rows); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	slog.Info("release imported", "dataset", dataset, "published_on", published.Format(time.DateOnly), "release_id", releaseID, "rows", rows, "sha256", digest)
	return nil
}
