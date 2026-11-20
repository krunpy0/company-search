package fns

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestReadRSMPXML(t *testing.T) {
	const legal = `<Документ ИдДок="legal" ДатаСост="10.11.2026" ДатаВклМСП="01.08.2016" ВидСубМСП="1" КатСубМСП="2" ССЧР="0">
		<ОргВклМСП НаимОрг="Тест" ИННЮЛ="0012345678" ОГРН="1234567890123"/>
		<СведМН КодРегион="01"/><СвОКВЭД><СвОКВЭДОсн КодОКВЭД="62.01"/></СвОКВЭД>
	</Документ>`
	const ip = `<Документ ИдДок="ip" ДатаСост="10.11.2026" ДатаВклМСП="10.11.2026" ВидСубМСП="3" КатСубМСП="1">
		<ИПВклМСП ИННФЛ="001234567890" ОГРНИП="123456789012345"/><СведМН КодРегион="01"/>
	</Документ>`
	const sample = `<Файл ИдФайл="sample" ВерсФорм="4.06" ТипИнф="РЕЕСТРМСП" КолДок="2">` + legal + ip + `</Файл>`
	date := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	var rows []SME
	read := func(s string) error {
		return readRSMPXML(context.Background(), strings.NewReader(s), "sample.xml", date, func(row SME) error {
			rows = append(rows, row)
			return nil
		})
	}
	if err := read(sample); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	org := rows[0]
	if org.Kind != "legal" || org.INN != "0012345678" || org.Number != "1234567890123" ||
		org.OKVED == nil || *org.OKVED != "62.01" || org.Employees == nil || *org.Employees != 0 ||
		org.EmployeeYear == nil || *org.EmployeeYear != 2025 || !org.AsOf.Equal(date) {
		t.Fatalf("unexpected legal observation: %+v", org)
	}
	entrepreneur := rows[1]
	if entrepreneur.Kind != "entrepreneur" || entrepreneur.SubjectType != "3" || entrepreneur.INN != "001234567890" ||
		entrepreneur.Number != "123456789012345" || entrepreneur.Name != nil || entrepreneur.Employees != nil {
		t.Fatalf("unexpected IP observation: %+v", entrepreneur)
	}

	for _, tc := range []struct{ name, old, replacement string }{
		{"schema", "4.06", "4.07"},
		{"count", `КолДок="2"`, `КолДок="3"`},
		{"date", "10.11.2026", "10.10.2026"},
		{"type", `ВидСубМСП="1"`, `ВидСубМСП="2"`},
		{"empty-count", `ССЧР="0"`, `ССЧР=""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := read(strings.Replace(sample, tc.old, tc.replacement, 1)); err == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
}
