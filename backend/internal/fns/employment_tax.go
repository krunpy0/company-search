package fns

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	HeadcountVersion          = "4.01"
	LegalRegimeVersion        = "4.02"
	EntrepreneurRegimeVersion = "4.01"
)

type EmploymentTaxObservation struct {
	INN, Name, DocumentID, File string
	AsOf, DocumentDate          time.Time
	Employees                   int32
	Registration                *string
	Regimes                     []string
}

type employmentTaxDocument struct {
	ID        string `xml:"ИдДок,attr"`
	Date      string `xml:"ДатаДок,attr"`
	AsOf      string `xml:"ДатаСост,attr"`
	Taxpayers []struct {
		LegalINN     string `xml:"ИННЮЛ,attr"`
		LegalName    string `xml:"НаимОрг,attr"`
		PersonINN    string `xml:"ИННФЛ,attr"`
		Registration string `xml:"ОГРНИП,attr"`
		Names        []struct {
			Last   string `xml:"Фамилия,attr"`
			First  string `xml:"Имя,attr"`
			Middle string `xml:"Отчество,attr"`
		} `xml:"ФИО"`
	} `xml:"СведНП"`
	Headcounts []struct {
		Count string `xml:"КолРаб,attr"`
	} `xml:"СведССЧР"`
	Regimes []struct {
		ESHN string `xml:"ПризнЕСХН,attr"`
		USN  string `xml:"ПризнУСН,attr"`
		AUSN string `xml:"ПризнАУСН,attr"`
		SRP  string `xml:"ПризнСРП,attr"`
		Code string `xml:"ПризнСНР,attr"`
	} `xml:"СведСНР"`
}

func ReadEmploymentTax(ctx context.Context, z *zip.Reader, dataset string, published time.Time, yield func(EmploymentTaxObservation) error) error {
	var version, info string
	switch dataset {
	case "sshr2019":
		version, info = HeadcountVersion, "ОТКРДАННЫЕ3"
	case "snr":
		version, info = LegalRegimeVersion, "ОТКРДАННЫЕ1"
	case "snrip":
		version, info = EntrepreneurRegimeVersion, "ОДИПСПЕЦРЕЖИМ"
	default:
		return fmt.Errorf("unsupported employment or tax dataset %q", dataset)
	}
	var asOf time.Time
	return readXMLArchive(ctx, z, 20000, 8<<20, 4<<30, func(r io.Reader, name string) error {
		return readFNSXML(ctx, r, name, version, info, true, func(d *xml.Decoder, t xml.StartElement) error {
			var doc employmentTaxDocument
			if err := d.DecodeElement(&doc, &t); err != nil {
				return err
			}
			row, err := doc.observation(dataset, name, published)
			if err != nil {
				return fmt.Errorf("document %q: %w", doc.ID, err)
			}
			if asOf.IsZero() {
				asOf = row.AsOf
			}
			if row.AsOf != asOf {
				return fmt.Errorf("inconsistent observation dates: %s and %s", asOf.Format(time.DateOnly), row.AsOf.Format(time.DateOnly))
			}
			return yield(row)
		})
	})
}

func (d employmentTaxDocument) observation(dataset, file string, published time.Time) (EmploymentTaxObservation, error) {
	row := EmploymentTaxObservation{DocumentID: d.ID, File: file}
	if d.ID == "" || utf8.RuneCountInString(d.ID) > 36 || len(d.Taxpayers) != 1 {
		return row, fmt.Errorf("invalid document fields")
	}
	var err error
	row.DocumentDate, err = time.Parse("02.01.2006", d.Date)
	if err != nil || row.DocumentDate.After(published) {
		return row, fmt.Errorf("invalid document date %q", d.Date)
	}
	row.AsOf, err = time.Parse("02.01.2006", d.AsOf)
	if err != nil || row.AsOf.Year() < 2018 || row.AsOf.After(row.DocumentDate) {
		return row, fmt.Errorf("invalid date of observation %q", d.AsOf)
	}

	taxpayer := d.Taxpayers[0]
	if dataset == "snrip" {
		if !entrepreneurINN.MatchString(taxpayer.PersonINN) || !ogrnip.MatchString(taxpayer.Registration) ||
			taxpayer.LegalINN != "" || taxpayer.LegalName != "" || len(taxpayer.Names) != 1 {
			return row, fmt.Errorf("invalid entrepreneur")
		}
		person := taxpayer.Names[0]
		row.INN, row.Registration = taxpayer.PersonINN, &taxpayer.Registration
		// Some published documents omit a name component required by the XSD.
		parts := []string{strings.TrimSpace(person.Last), strings.TrimSpace(person.First), strings.TrimSpace(person.Middle)}
		row.Name = strings.Join(slices.DeleteFunc(parts, func(part string) bool { return part == "" }), " ")
	} else {
		if !innPattern.MatchString(taxpayer.LegalINN) || taxpayer.LegalINN[:2] == "00" ||
			strings.TrimSpace(taxpayer.LegalName) == "" || utf8.RuneCountInString(taxpayer.LegalName) > 1000 ||
			taxpayer.PersonINN != "" || taxpayer.Registration != "" || len(taxpayer.Names) != 0 {
			return row, fmt.Errorf("invalid legal entity")
		}
		row.INN, row.Name = taxpayer.LegalINN, taxpayer.LegalName
	}

	if dataset == "sshr2019" {
		if len(d.Headcounts) != 1 || len(d.Regimes) != 0 || row.AsOf.Month() != time.December || row.AsOf.Day() != 31 {
			return row, fmt.Errorf("invalid annual headcount observation")
		}
		count, err := strconv.ParseInt(d.Headcounts[0].Count, 10, 32)
		if err != nil || count < 0 || count > 999999999 {
			return row, fmt.Errorf("invalid headcount")
		}
		row.Employees = int32(count)
		return row, nil
	}

	if len(d.Headcounts) != 0 || len(d.Regimes) == 0 {
		return row, fmt.Errorf("invalid tax observation")
	}
	row.Regimes = make([]string, 0, len(d.Regimes))
	if dataset == "snr" {
		if len(d.Regimes) != 1 || d.Regimes[0].Code != "" {
			return row, fmt.Errorf("invalid legal entity tax regime")
		}
		r := d.Regimes[0]
		for _, flag := range []struct{ value, code string }{{r.ESHN, "eshn"}, {r.USN, "usn"}, {r.AUSN, "ausn"}, {r.SRP, "srp"}} {
			switch flag.value {
			case "1":
				row.Regimes = append(row.Regimes, flag.code)
			case "0":
			default:
				return row, fmt.Errorf("invalid %s tax regime flag", flag.code)
			}
		}
	} else {
		// Code meanings come from the format description linked by the FNS snrip passport.
		codes := [...]string{"usn", "ausn", "eshn", "psn", "npd"}
		for _, r := range d.Regimes {
			if len(r.Code) != 1 || r.Code[0] < '1' || r.Code[0] > '5' || r.ESHN != "" || r.USN != "" || r.AUSN != "" || r.SRP != "" {
				return row, fmt.Errorf("invalid entrepreneur tax regime code")
			}
			code := codes[r.Code[0]-'1']
			if !slices.Contains(row.Regimes, code) {
				row.Regimes = append(row.Regimes, code)
			}
		}
	}
	return row, nil
}
