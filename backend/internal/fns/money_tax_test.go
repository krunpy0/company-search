package fns

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"
	"time"
)

func TestMoneyTaxDebtNullAndZero(t *testing.T) {
	const name = "debt"
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, err := archive.Create(name + ".xml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write([]byte(`<Файл ИдФайл="debt" ВерсФорм="4.01" ТипИнф="ОТКРДАННЫЕ6" КолДок="1">
 <ИдОтпр/><Документ ИдДок="debt-1" ДатаДок="25.10.2026" ДатаСост="01.10.2026">
 <СведНП ИННЮЛ="7707083893"/>
 <СведНедоим НаимНалог="Налог" СумПени="0.00" ОбщСумНедоим="142.01"/>
 </Документ></Файл>`))
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	published := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	called := false
	err = ReadMoneyTax(context.Background(), z, "debtam", published, func(doc MoneyTaxDocument) error {
		called = true
		line := doc.Lines[0]
		if line.Principal.Valid || line.Fines.Valid || !line.Penalties.Valid || line.Penalties.Int.Sign() != 0 ||
			!line.Total.Valid || line.Total.Int.Int64() != 14201 || line.Total.Exp != -2 {
			t.Fatalf("unexpected amounts: %+v", line)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}
