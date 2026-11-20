package fns

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestEmploymentTaxArchive(t *testing.T) {
	const legal = `<СведНП НаимОрг="ООО &quot;Пример&quot;" ИННЮЛ="0100000001"/>`
	const entrepreneur = `<СведНП ИННФЛ="010000000001" ОГРНИП="326010000000001"><ФИО Фамилия="Иванов" Имя="Иван"/></СведНП>`
	const partialName = `<СведНП ИННФЛ="010000000001" ОГРНИП="326010000000001"><ФИО Имя="Иван"/></СведНП>`
	const annual = `<Документ ИдДок="annual" ДатаДок="25.10.2026" ДатаСост="31.12.2025">`
	const october = `<Документ ИдДок="october" ДатаДок="25.10.2026" ДатаСост="01.10.2026">`
	const previous = `<Документ ИдДок="previous" ДатаДок="25.10.2026" ДатаСост="01.09.2026">`
	const closeDoc = `</Документ>`
	const employee = `<СведССЧР КолРаб="0"/>`
	const regimes = `<СведСНР ПризнЕСХН="0" ПризнУСН="1" ПризнАУСН="0" ПризнСРП="0"/>`
	const noRegime = `<СведСНР ПризнЕСХН="0" ПризнУСН="0" ПризнАУСН="0" ПризнСРП="0"/>`
	const ipUSN = `<СведСНР ПризнСНР="1"/>`
	const ipPSN = `<СведСНР ПризнСНР="4"/>`
	const ipUnknown = `<СведСНР ПризнСНР="9"/>`
	published := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, dataset, version, info, asOf, displayName string
		docs, regimes                                   []string
		err                                             bool
	}{
		{"published zero", "sshr2019", "4.01", "ОТКРДАННЫЕ3", "2025-12-31", `ООО "Пример"`, []string{annual + legal + employee + closeDoc}, nil, false},
		{"missing headcount", "sshr2019", "4.01", "ОТКРДАННЫЕ3", "", "", []string{annual + legal + closeDoc}, nil, true},
		{"legal regime", "snr", "4.02", "ОТКРДАННЫЕ1", "2026-10-01", `ООО "Пример"`, []string{october + legal + regimes + closeDoc}, []string{"usn"}, false},
		{"legal all flags zero", "snr", "4.02", "ОТКРДАННЫЕ1", "2026-10-01", `ООО "Пример"`, []string{october + legal + noRegime + closeDoc}, []string{}, false},
		{"mixed dates", "snr", "4.02", "ОТКРДАННЫЕ1", "", "", []string{october + legal + regimes + closeDoc, previous + legal + regimes + closeDoc}, nil, true},
		{"entrepreneur regimes", "snrip", "4.01", "ОДИПСПЕЦРЕЖИМ", "2026-10-01", "Иванов Иван", []string{october + entrepreneur + ipUSN + ipPSN + closeDoc}, []string{"usn", "psn"}, false},
		{"partial entrepreneur name", "snrip", "4.01", "ОДИПСПЕЦРЕЖИМ", "2026-10-01", "Иван", []string{october + partialName + ipUSN + closeDoc}, []string{"usn"}, false},
		{"unknown entrepreneur regime", "snrip", "4.01", "ОДИПСПЕЦРЕЖИМ", "", "", []string{october + entrepreneur + ipUnknown + closeDoc}, nil, true},
		{"repeated entrepreneur regime", "snrip", "4.01", "ОДИПСПЕЦРЕЖИМ", "2026-10-01", "Иванов Иван", []string{october + entrepreneur + ipUSN + ipUSN + closeDoc}, []string{"usn"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data bytes.Buffer
			archive := zip.NewWriter(&data)
			entry, err := archive.Create("sample.xml")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintf(entry, `<Файл ИдФайл="sample" ВерсФорм="%s" ТипИнф="%s" КолДок="%d"><ИдОтпр/>`, tc.version, tc.info, len(tc.docs)); err != nil {
				t.Fatal(err)
			}
			for _, doc := range tc.docs {
				if _, err := fmt.Fprint(entry, doc); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := fmt.Fprint(entry, `</Файл>`); err != nil {
				t.Fatal(err)
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			z, err := zip.NewReader(bytes.NewReader(data.Bytes()), int64(data.Len()))
			if err != nil {
				t.Fatal(err)
			}
			var rows []EmploymentTaxObservation
			err = ReadEmploymentTax(context.Background(), z, tc.dataset, published, func(row EmploymentTaxObservation) error {
				rows = append(rows, row)
				return nil
			})
			if tc.err {
				if err == nil {
					t.Fatal("invalid archive accepted")
				}
				return
			}
			if err != nil || len(rows) != 1 {
				t.Fatalf("read %d observations: %v", len(rows), err)
			}
			row := rows[0]
			if row.AsOf.Format(time.DateOnly) != tc.asOf || row.DocumentDate != published || row.File != "sample.xml" ||
				row.DocumentID == "" || row.Name != tc.displayName || row.Employees != 0 || (row.Regimes == nil) != (tc.regimes == nil) ||
				!slices.Equal(row.Regimes, tc.regimes) {
				t.Fatalf("unexpected observation: %+v", row)
			}
			if tc.dataset == "snrip" {
				if row.Registration == nil || *row.Registration != "326010000000001" || row.INN != "010000000001" {
					t.Fatalf("unexpected entrepreneur identity: %+v", row)
				}
			} else if row.Registration != nil || row.INN != "0100000001" {
				t.Fatalf("unexpected legal identity: %+v", row)
			}
		})
	}
}
