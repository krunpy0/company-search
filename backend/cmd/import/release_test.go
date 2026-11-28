package main

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestCorrectedRelease(t *testing.T) {
	dsn := os.Getenv("COMPANY_IMPORT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("run test-postgres.ps1 for an isolated database")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var database string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil || database != "stage14_test" {
		t.Fatalf("use the isolated stage14_test database: %q, %v", database, err)
	}
	t.Setenv("DATABASE_URL", dsn)
	published := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	archive := func(income int) string {
		file, err := os.Create(filepath.Join(t.TempDir(), "sample.zip"))
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		writer := zip.NewWriter(file)
		entry, err := writer.Create("sample.xml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(entry, `<Файл ИдФайл="sample" ВерсФорм="4.01" ТипИнф="ОТКРДАННЫЕ5" КолДок="1">
<ИдОтпр><ФИООтв Фамилия="_" Имя="_"/></ИдОтпр>
<Документ ИдДок="doc1" ДатаДок="25.10.2026" ДатаСост="31.12.2025">
<СведНП НаимОрг="Пример" ИННЮЛ="0100000001"/>
<СведДохРасх СумДоход="%d" СумРасход="0.00"/>
</Документ></Файл>`, income); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return file.Name()
	}
	original, corrected := archive(100), archive(200)
	for _, step := range []struct {
		path     string
		income   int
		releases int
	}{
		{original, 100, 1},
		{corrected, 200, 2},
		{corrected, 200, 2},
		{original, 200, 2},
	} {
		if err := run(ctx, revexp, published, step.path, false); err != nil {
			t.Fatal(err)
		}
		var income, releases int
		err := conn.QueryRow(ctx, `
			SELECT f.income::integer, (
				SELECT count(*) FROM releases WHERE dataset_code='revexp' AND published_on=$1 AND period_key='2025'
			)
			FROM published_releases p JOIN financials f ON f.release_id=p.release_id
			WHERE p.dataset_code='revexp' AND p.period_key='2025' AND f.inn='0100000001'
		`, published).Scan(&income, &releases)
		if err != nil || income != step.income || releases != step.releases {
			t.Fatalf("active income=%d and releases=%d, want %d and %d: %v", income, releases, step.income, step.releases, err)
		}
	}
}
