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

var employmentTaxDatasets = map[string]struct {
	schemaDate, version string
	lockID              int64
}{
	"sshr2019": {"20200408", fns.HeadcountVersion, 71004},
	"snr":      {"20230425", fns.LegalRegimeVersion, 71005},
	"snrip":    {"20241025", fns.EntrepreneurRegimeVersion, 71006},
}

func main() {
	dataset := flag.String("dataset", revexp, "FNS dataset (revexp, rsmp, sshr2019, snr, snrip)")
	date := flag.String("published-on", "", "FNS release date (YYYY-MM-DD)")
	archive := flag.String("archive", "", "optional local copy of the official ZIP")
	flag.Parse()
	published, err := time.Parse(time.DateOnly, *date)
	_, employmentTax := employmentTaxDatasets[*dataset]
	if err != nil || published.Year() < 2019 || flag.NArg() != 0 || (*dataset != revexp && *dataset != "rsmp" && !employmentTax) {
		flag.Usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *dataset, published, *archive); err != nil {
		slog.Error("import failed", "dataset", *dataset, "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dataset string, published time.Time, archive string) (runErr error) {
	year := published.Year() - 1
	period := strconv.Itoa(year)
	lockID, maxZIP := int64(71002), int64(fns.MaxRevexpZIP)
	timeout := 3 * time.Minute
	passportURL, schemaURL, version := passport, baseURL+"structure-20180110.xsd", fns.RevexpVersion
	url := baseURL + "data-" + published.Format("20060102") + "-structure-20180110.zip"
	employmentTaxSpec, employmentTax := employmentTaxDatasets[dataset]
	if dataset == "rsmp" {
		const rsmpBase = "https://file.nalog.ru/opendata/7707329152-rsmp/"
		period, lockID, maxZIP, timeout = "current", 71003, fns.MaxRSMPZIP, 30*time.Minute
		passportURL = "https://www.nalog.gov.ru/opendata/7707329152-rsmp/"
		schemaURL, version = rsmpBase+"structure-12052026.xsd", fns.RSMPVersion
		url = rsmpBase + "data-" + published.Format("02012006") + "-structure-12052026.zip"
	}
	if employmentTax {
		base := "https://file.nalog.ru/opendata/7707329152-" + dataset + "/"
		period, lockID, maxZIP, timeout = "pending", employmentTaxSpec.lockID, 512<<20, 10*time.Minute
		passportURL = "https://www.nalog.gov.ru/opendata/7707329152-" + dataset + "/"
		schemaURL, version = base+"structure-"+employmentTaxSpec.schemaDate+".xsd", employmentTaxSpec.version
		url = base + "data-" + published.Format("20060102") + "-structure-" + employmentTaxSpec.schemaDate + ".zip"
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("invalid DATABASE_URL")
	}
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, err := pgx.ConnectConfig(connectCtx, config)
	cancel()
	if err != nil {
		return fmt.Errorf("could not connect to PostgreSQL")
	}
	defer conn.Close(context.Background())
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("%s import is already running", dataset)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO datasets VALUES ($1, 'ФНС России', $2, 'monthly')
		ON CONFLICT (code) DO NOTHING`, dataset, passportURL); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `UPDATE import_runs SET status='failed', finished_at=now(), error_code='interrupted'
		WHERE dataset_code=$1 AND status='running'`, dataset); err != nil {
		return err
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
		if errors.Is(runErr, context.Canceled) {
			phase = "interrupted"
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
			slog.Info("release already imported", "release_id", releaseID)
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
	} else if employmentTax {
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
	slog.Info("release imported", "release_id", releaseID, "rows", rows, "sha256", digest)
	return nil
}
