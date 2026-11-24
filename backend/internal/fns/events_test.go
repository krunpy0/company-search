package fns

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"
)

func TestSupportReleaseCountsActualDocuments(t *testing.T) {
	const recipient = `<СвФЛ ИННФЛ="262410199404" ОГРНИП="318265100124511"/><СвПредПод НомерПод="1" ДатаПрин="05.12.2023"><ФормПод НаимФорм="Консультационная поддержка"/><ВидПод НаимВид="Консультация"/><РазмПод РазмПод="1.00" ЕдПод="3"/></СвПредПод>`
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	entry, err := writer.Create("sample.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = entry.Write([]byte(`<Файл ИдФайл="sample" ВерсФорм="4.04" ТипИнф="РЕЕСТРМСП-ПП" КолДок="1">
		<Документ ИдДок="a" ДатаСост="15.11.2026">` + recipient + `</Документ>
		<Документ ИдДок="b" ДатаСост="15.11.2026">` + recipient + `</Документ>
	</Файл>`))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data.Bytes()), int64(data.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var items []Event
	err = ReadEvents(context.Background(), archive, "rsmppp", func(event Event) error {
		items = append(items, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].Registration != "318265100124511" ||
		items[1].Details["support_size"] != "1.00" || items[1].Details["support_unit_code"] != "3" {
		t.Fatalf("unexpected support records: %#v", items)
	}
}
