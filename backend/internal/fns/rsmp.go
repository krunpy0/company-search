package fns

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const RSMPVersion = "4.06"
const MaxRSMPZIP = 3 << 30

var (
	entrepreneurINN = regexp.MustCompile(`^[0-9]{12}$`)
	ogrn            = regexp.MustCompile(`^[0-9]{13}$`)
	ogrnip          = regexp.MustCompile(`^[0-9]{15}$`)
	regionCode      = regexp.MustCompile(`^[0-9]{2}$`)
)

type smeDocument struct {
	ID        string  `xml:"ИдДок,attr"`
	AsOf      string  `xml:"ДатаСост,attr"`
	Included  string  `xml:"ДатаВклМСП,attr"`
	Kind      string  `xml:"ВидСубМСП,attr"`
	Category  string  `xml:"КатСубМСП,attr"`
	Employees *string `xml:"ССЧР,attr"`
	Legal     []struct {
		INN    string `xml:"ИННЮЛ,attr"`
		Number string `xml:"ОГРН,attr"`
		Name   string `xml:"НаимОрг,attr"`
	} `xml:"ОргВклМСП"`
	Entrepreneurs []struct {
		INN    string `xml:"ИННФЛ,attr"`
		Number string `xml:"ОГРНИП,attr"`
		Name   struct {
			Last   string `xml:"Фамилия,attr"`
			First  string `xml:"Имя,attr"`
			Middle string `xml:"Отчество,attr"`
		} `xml:"ФИОИП"`
	} `xml:"ИПВклМСП"`

	Location []struct {
		Code string `xml:"КодРегион,attr"`
	} `xml:"СведМН"`
	OKVED []struct {
		Primary struct {
			Code string `xml:"КодОКВЭД,attr"`
		} `xml:"СвОКВЭДОсн"`
	} `xml:"СвОКВЭД"`
}

type SME struct {
	INN, Number, Kind, DocumentID, File, Region, Category, SubjectType string
	Name, OKVED                                                        *string
	AsOf, Included                                                     time.Time
	Employees                                                          *int32
	EmployeeYear                                                       *int16
}

func ReadRSMP(ctx context.Context, z *zip.Reader, published time.Time, yield func(SME) error) error {
	return readXMLArchive(ctx, z, 30000, 16<<20, 32<<30, func(r io.Reader, name string) error {
		return readRSMPXML(ctx, r, name, published, yield)
	})
}

func readRSMPXML(ctx context.Context, r io.Reader, name string, published time.Time, yield func(SME) error) error {
	return readFNSXML(ctx, r, name, RSMPVersion, "РЕЕСТРМСП", false, func(d *xml.Decoder, t xml.StartElement) error {
		var doc smeDocument
		if err := d.DecodeElement(&doc, &t); err != nil {
			return err
		}
		row, err := doc.observation(name, published)
		if err != nil {
			return err
		}
		return yield(row)
	})
}

func (d smeDocument) observation(file string, published time.Time) (SME, error) {
	row := SME{DocumentID: d.ID, File: file, Category: d.Category, SubjectType: d.Kind}
	var name string
	switch {
	case len(d.Legal) == 1 && len(d.Entrepreneurs) == 0 && d.Kind == "1":
		legal := d.Legal[0]
		row.Kind, row.INN, row.Number, name = "legal", legal.INN, legal.Number, strings.TrimSpace(legal.Name)
		if !innPattern.MatchString(row.INN) || !ogrn.MatchString(row.Number) || name == "" {
			return row, fmt.Errorf("invalid legal entity")
		}
	case len(d.Entrepreneurs) == 1 && len(d.Legal) == 0 && (d.Kind == "2" || d.Kind == "3"):
		ip := d.Entrepreneurs[0]
		row.Kind, row.INN, row.Number = "entrepreneur", ip.INN, ip.Number
		name = strings.TrimSpace(strings.Join([]string{ip.Name.Last, ip.Name.First, ip.Name.Middle}, " "))
		if !entrepreneurINN.MatchString(row.INN) || !ogrnip.MatchString(row.Number) {
			return row, fmt.Errorf("invalid entrepreneur")
		}
	default:
		return row, fmt.Errorf("invalid SME entity branch or kind")
	}
	if name != "" {
		row.Name = &name
	}
	if d.ID == "" || len(d.Location) != 1 || !regionCode.MatchString(d.Location[0].Code) ||
		(d.Category != "1" && d.Category != "2" && d.Category != "3") || len(d.OKVED) > 1 {
		return row, fmt.Errorf("invalid SME document fields")
	}
	row.Region = d.Location[0].Code
	if len(d.OKVED) == 1 && d.OKVED[0].Primary.Code != "" {
		row.OKVED = &d.OKVED[0].Primary.Code
	}
	var err error
	row.AsOf, err = time.Parse("02.01.2006", d.AsOf)
	if err != nil || !row.AsOf.Equal(published) {
		return row, fmt.Errorf("invalid SME observation date %q", d.AsOf)
	}
	row.Included, err = time.Parse("02.01.2006", d.Included)
	if err != nil || row.Included.After(row.AsOf) {
		return row, fmt.Errorf("invalid SME inclusion date %q", d.Included)
	}
	if d.Employees != nil {
		n, err := strconv.ParseInt(*d.Employees, 10, 32)
		if err != nil || n < 0 {
			return row, fmt.Errorf("invalid SME employee count")
		}
		year := int16(published.Year() - 1)
		if published.Month() <= time.June {
			year--
		}
		count := int32(n)
		row.Employees, row.EmployeeYear = &count, &year
	}
	return row, nil
}
