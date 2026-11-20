package fns

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const sample = `<?xml version="1.0" encoding="UTF-8"?>
<Файл ИдФайл="sample" ВерсФорм="4.01" ТипИнф="ОТКРДАННЫЕ5" КолДок="1">
  <ИдОтпр><ФИООтв Фамилия="_" Имя="_"/></ИдОтпр>
  <Документ ИдДок="doc1" ДатаДок="25.10.2026" ДатаСост="31.12.2025">
    <СведНП НаимОрг="ООО &quot;Пример&quot;" ИННЮЛ="0100000001"/>
    <СведДохРасх СумДоход="12345678901234567.89" СумРасход="0.00"/>
  </Документ>
</Файл>`

func TestReadRevexp(t *testing.T) {
	published := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, xml           string
		ok, corrupt, cancel bool
	}{
		{name: "valid", xml: sample, ok: true},
		{name: "unknown version", xml: strings.Replace(sample, "4.01", "4.02", 1)},
		{name: "missing amount", xml: strings.Replace(sample, ` СумРасход="0.00"`, "", 1)},
		{name: "incorrect count", xml: strings.Replace(sample, `КолДок="1"`, `КолДок="2"`, 1)},
		{name: "bad CRC", xml: sample, corrupt: true},
		{name: "cancelled", xml: sample, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := zip.NewWriter(&buf)
			entry, err := w.CreateHeader(&zip.FileHeader{Name: "sample.xml", Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write([]byte(tc.xml)); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if tc.corrupt {
				index := bytes.Index(buf.Bytes(), []byte("doc1"))
				buf.Bytes()[index] = 'x'
			}
			z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var count int
			err = ReadRevexp(ctx, z, published, 2025, func(row Financial) error {
				count++
				if tc.cancel {
					cancel()
				}
				if tc.ok {
					income, _ := row.Income.Value()
					expense, _ := row.Expense.Value()
					if row.INN != "0100000001" || row.Name != `ООО "Пример"` ||
						row.DocumentID != "doc1" || row.File != "sample.xml" ||
						row.AsOf.Format(time.DateOnly) != "2025-12-31" ||
						income != "12345678901234567.89" || expense != "0.00" {
						t.Fatalf("unexpected row: %+v", row)
					}
				}
				return nil
			})
			if tc.ok && (err != nil || count != 1) || !tc.ok && err == nil {
				t.Fatalf("read %d rows: %v", count, err)
			}
			if tc.corrupt && !errors.Is(err, zip.ErrChecksum) || tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
