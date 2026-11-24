package fns

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	SupportVersion = "4.04"
	OffenceVersion = "4.01"
)

type Event struct {
	INN, Registration, DocumentID, File, EventID string
	AsOf, EventOn                                time.Time
	Details                                      map[string]string
}

type supportEvent struct {
	ID        string `xml:"НомерПод,attr"`
	Decision  string `xml:"ДатаПрин,attr"`
	Ends      string `xml:"ДатаПрекр,attr"`
	Provider  string `xml:"НаимОрг,attr"`
	Violation string `xml:"ИнфНаруш,attr"`
	Form      struct {
		Name string `xml:"НаимФорм,attr"`
	} `xml:"ФормПод"`
	Kind struct {
		Name string `xml:"НаимВид,attr"`
	} `xml:"ВидПод"`
	Size struct {
		Value string `xml:"РазмПод,attr"`
		Unit  string `xml:"ЕдПод,attr"`
	} `xml:"РазмПод"`
}

type eventDocument struct {
	ID    string `xml:"ИдДок,attr"`
	AsOf  string `xml:"ДатаСост,attr"`
	Legal []struct {
		INN          string `xml:"ИННЮЛ,attr"`
		Registration string `xml:"ОГРН,attr"`
	} `xml:"СвЮЛ"`
	Person []struct {
		INN          string `xml:"ИННФЛ,attr"`
		Registration string `xml:"ОГРНИП,attr"`
	} `xml:"СвФЛ"`
	Support   []supportEvent `xml:"СвПредПод"`
	Taxpayers []taxpayer     `xml:"СведНП"`
	Offences  []struct {
		Fine string `xml:"СумШтраф,attr"`
	} `xml:"СведНаруш"`
}

func ReadEvents(ctx context.Context, z *zip.Reader, dataset string, yield func(Event) error) error {
	version, info := OffenceVersion, "ОТКРДАННЫЕ7"
	support := dataset == "rsmppp"
	if support {
		version, info = SupportVersion, "РЕЕСТРМСП-ПП"
	}
	return readXMLArchive(ctx, z, 30000, 64<<20, 16<<30, func(r io.Reader, file string) error {
		return readFNSXML(ctx, r, file, version, info, !support, func(d *xml.Decoder, start xml.StartElement) error {
			var doc eventDocument
			if err := d.DecodeElement(&doc, &start); err != nil {
				return err
			}
			asOf, err := time.Parse("02.01.2006", doc.AsOf)
			if err != nil || strings.TrimSpace(doc.ID) == "" {
				return fmt.Errorf("invalid event document metadata")
			}
			event := Event{DocumentID: doc.ID, File: file, AsOf: asOf}
			if support {
				return doc.supportEvents(event, yield)
			}
			if len(doc.Taxpayers) != 1 || len(doc.Offences) != 1 || !innPattern.MatchString(doc.Taxpayers[0].INN) {
				return fmt.Errorf("invalid tax offence document %q", doc.ID)
			}
			fine := doc.Offences[0].Fine
			if _, err := parseAmount(fine); err != nil || strings.HasPrefix(fine, "-") {
				return fmt.Errorf("invalid fine in document %q", doc.ID)
			}
			event.INN, event.EventOn = doc.Taxpayers[0].INN, asOf
			event.Details = map[string]string{"total_fine_rub": fine}
			return yield(event)
		})
	})
}

func (d eventDocument) supportEvents(event Event, yield func(Event) error) error {
	switch {
	case len(d.Legal) == 1 && len(d.Person) == 0 && innPattern.MatchString(d.Legal[0].INN) && ogrn.MatchString(d.Legal[0].Registration):
		event.INN, event.Registration = d.Legal[0].INN, d.Legal[0].Registration
	case len(d.Person) == 1 && len(d.Legal) == 0 && entrepreneurINN.MatchString(d.Person[0].INN):
		event.INN, event.Registration = d.Person[0].INN, d.Person[0].Registration
		if event.Registration == "" {
			return nil // The register also includes recipients without an entrepreneur registration.
		}
		if !ogrnip.MatchString(event.Registration) {
			return fmt.Errorf("invalid entrepreneur registration in %q", d.ID)
		}
	default:
		return fmt.Errorf("invalid support recipient in %q", d.ID)
	}
	if len(d.Support) == 0 {
		return fmt.Errorf("missing support events in %q", d.ID)
	}
	for _, row := range d.Support {
		decision, err := time.Parse("02.01.2006", row.Decision)
		if err != nil || row.ID == "" || row.Form.Name == "" || row.Kind.Name == "" || row.Size.Value == "" || row.Size.Unit == "" {
			return fmt.Errorf("invalid support event in %q", d.ID)
		}
		event.EventOn, event.EventID = decision, row.ID
		event.Details = map[string]string{
			"provider_name": row.Provider, "support_form": row.Form.Name, "support_kind": row.Kind.Name,
			"support_size": row.Size.Value, "support_unit_code": row.Size.Unit, "violation_code": row.Violation,
		}
		if row.Ends != "" {
			end, err := time.Parse("02.01.2006", row.Ends)
			if err != nil {
				return fmt.Errorf("invalid support end date in %q", d.ID)
			}
			event.Details["ended_on"] = end.Format(time.DateOnly)
		}
		if err := yield(event); err != nil {
			return err
		}
	}
	return nil
}
