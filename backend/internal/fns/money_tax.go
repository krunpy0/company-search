package fns

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

const MoneyTaxVersion = "4.01"

type MoneyTaxLine struct {
	Name                                       string
	Amount, Principal, Penalties, Fines, Total pgtype.Numeric
}

type MoneyTaxDocument struct {
	INN, ID, File      string
	AsOf, DocumentDate time.Time
	Lines              []MoneyTaxLine
}

type moneyTaxXMLLine struct {
	Name      string  `xml:"НаимНалог,attr"`
	Amount    *string `xml:"СумУплНал,attr"`
	Principal *string `xml:"СумНедНалог,attr"`
	Penalties *string `xml:"СумПени,attr"`
	Fines     *string `xml:"СумШтраф,attr"`
	Total     *string `xml:"ОбщСумНедоим,attr"`
}

func ReadMoneyTax(ctx context.Context, z *zip.Reader, dataset string, published time.Time, yield func(MoneyTaxDocument) error) error {
	info := "ОТКРДАННЫЕ4"
	switch dataset {
	case "paytax":
	case "debtam":
		info = "ОТКРДАННЫЕ6"
	default:
		return fmt.Errorf("unsupported tax dataset %q", dataset)
	}
	var releaseDate time.Time
	return readXMLArchive(ctx, z, 20000, 8<<20, 8<<30, func(r io.Reader, name string) error {
		return readFNSXML(ctx, r, name, MoneyTaxVersion, info, true, func(d *xml.Decoder, t xml.StartElement) error {
			var doc struct {
				ID        string `xml:"ИдДок,attr"`
				Date      string `xml:"ДатаДок,attr"`
				AsOf      string `xml:"ДатаСост,attr"`
				Taxpayers []struct {
					INN string `xml:"ИННЮЛ,attr"`
				} `xml:"СведНП"`
				Payments []moneyTaxXMLLine `xml:"СвУплСумНал"`
				Debts    []moneyTaxXMLLine `xml:"СведНедоим"`
			}
			if err := d.DecodeElement(&doc, &t); err != nil {
				return err
			}
			observed, err := time.Parse("02.01.2006", doc.AsOf)
			if err != nil || observed.Year() < 2018 || (!releaseDate.IsZero() && observed != releaseDate) {
				return fmt.Errorf("document %q: invalid observation date %q", doc.ID, doc.AsOf)
			}
			releaseDate = observed
			dated, err := time.Parse("02.01.2006", doc.Date)
			if err != nil || dated.After(published) || dated.Before(observed) ||
				doc.ID == "" || len(doc.ID) > 36 || len(doc.Taxpayers) != 1 ||
				!innPattern.MatchString(doc.Taxpayers[0].INN) || strings.HasPrefix(doc.Taxpayers[0].INN, "00") {
				return fmt.Errorf("document %q: invalid taxpayer or document metadata", doc.ID)
			}
			lines := doc.Debts
			if dataset == "paytax" {
				if len(doc.Debts) != 0 || observed != time.Date(published.Year()-1, 12, 31, 0, 0, 0, 0, time.UTC) {
					return fmt.Errorf("document %q: invalid payment period or unexpected debt", doc.ID)
				}
				lines = doc.Payments
			} else if len(doc.Payments) != 0 {
				return fmt.Errorf("document %q: unexpected payments", doc.ID)
			}
			if len(lines) == 0 {
				return fmt.Errorf("document %q: no tax lines", doc.ID)
			}
			result := MoneyTaxDocument{INN: doc.Taxpayers[0].INN, ID: doc.ID, File: name, AsOf: observed, DocumentDate: dated}
			for _, raw := range lines {
				if strings.TrimSpace(raw.Name) == "" || (dataset == "paytax" && raw.Amount == nil) ||
					(dataset == "debtam" && raw.Total == nil) {
					return fmt.Errorf("document %q: missing tax name or amount", doc.ID)
				}
				line := MoneyTaxLine{Name: raw.Name}
				for _, field := range []struct {
					raw *string
					dst *pgtype.Numeric
				}{
					{raw.Amount, &line.Amount}, {raw.Principal, &line.Principal}, {raw.Penalties, &line.Penalties},
					{raw.Fines, &line.Fines}, {raw.Total, &line.Total},
				} {
					if field.raw == nil {
						continue
					}
					value, err := parseAmount(*field.raw)
					if err != nil {
						return fmt.Errorf("document %q: %w", doc.ID, err)
					}
					*field.dst = value
				}
				result.Lines = append(result.Lines, line)
			}
			return yield(result)
		})
	})
}
