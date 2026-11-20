package fns

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"

	"regexp"

	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

const RevexpVersion = "4.01"
const MaxRevexpZIP = 512 << 20

var (
	innPattern     = regexp.MustCompile(`^[0-9]{10}$`)
	decimalPattern = regexp.MustCompile(`^[+-]?[0-9]+(\.[0-9]{1,2})?$`)
)

type taxpayer struct {
	INN  string `xml:"ИННЮЛ,attr"`
	Name string `xml:"НаимОрг,attr"`
}

type amounts struct {
	Income  string `xml:"СумДоход,attr"`
	Expense string `xml:"СумРасход,attr"`
}

type document struct {
	ID        string     `xml:"ИдДок,attr"`
	Date      string     `xml:"ДатаДок,attr"`
	AsOf      string     `xml:"ДатаСост,attr"`
	Taxpayers []taxpayer `xml:"СведНП"`
	Amounts   []amounts  `xml:"СведДохРасх"`
}

type Financial struct {
	INN, Name, DocumentID, File string
	AsOf, DocumentDate          time.Time
	Income, Expense             pgtype.Numeric
}

func ReadRevexp(ctx context.Context, z *zip.Reader, published time.Time, year int, yield func(Financial) error) error {
	return readXMLArchive(ctx, z, 20000, 8<<20, 2<<30, func(r io.Reader, name string) error {
		return readRevexpXML(ctx, r, name, published, year, yield)
	})
}

func readRevexpXML(ctx context.Context, r io.Reader, name string, published time.Time, year int, yield func(Financial) error) error {
	return readFNSXML(ctx, r, name, RevexpVersion, "ОТКРДАННЫЕ5", true, func(d *xml.Decoder, t xml.StartElement) error {
		var doc document
		if err := d.DecodeElement(&doc, &t); err != nil {
			return err
		}
		row, err := doc.financial(name, published, year)
		if err != nil {
			return fmt.Errorf("document %q: %w", doc.ID, err)
		}
		return yield(row)
	})
}

func (d document) financial(name string, published time.Time, year int) (Financial, error) {
	row := Financial{DocumentID: d.ID, File: name}
	if d.ID == "" || utf8.RuneCountInString(d.ID) > 36 || len(d.Taxpayers) != 1 || len(d.Amounts) != 1 {
		return row, fmt.Errorf("missing or repeated document fields")
	}
	row.INN, row.Name = d.Taxpayers[0].INN, d.Taxpayers[0].Name
	if !innPattern.MatchString(row.INN) || row.INN[:2] == "00" || strings.TrimSpace(row.Name) == "" || utf8.RuneCountInString(row.Name) > 1000 {
		return row, fmt.Errorf("invalid taxpayer")
	}
	var err error
	row.DocumentDate, err = time.Parse("02.01.2006", d.Date)
	if err != nil || row.DocumentDate.After(published) {
		return row, fmt.Errorf("invalid document date %q", d.Date)
	}
	row.AsOf, err = time.Parse("02.01.2006", d.AsOf)
	if err != nil || row.AsOf.Year() != year || row.AsOf.After(row.DocumentDate) {
		return row, fmt.Errorf("invalid date of observation %q for year %d", d.AsOf, year)
	}
	row.Income, err = parseAmount(d.Amounts[0].Income)
	if err != nil {
		return row, err
	}
	row.Expense, err = parseAmount(d.Amounts[0].Expense)
	return row, err
}

func parseAmount(s string) (pgtype.Numeric, error) {
	var result pgtype.Numeric
	digits := strings.ReplaceAll(strings.TrimLeft(s, "+-"), ".", "")
	if !decimalPattern.MatchString(s) || len(strings.TrimLeft(digits, "0")) > 19 {
		return result, fmt.Errorf("invalid monetary amount")
	}
	err := result.Scan(strings.TrimPrefix(s, "+"))
	return result, err
}
