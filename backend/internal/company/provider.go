package company

import (
	"context"
	"slices"
	"strings"
	"time"
)

type PartyLookup func(context.Context, string, string, string) ([]RegistryParty, bool, string)

// RegistryParty contains normalized fields from a registry adapter.
type RegistryParty struct {
	INN, Registration, Kind, KPP, OKVED, Name, Status   string
	RegisteredOn, LastChangedOn, Address, ResidenceCity string
	Manager                                             RegistryManager
}

type RegistryManager struct {
	Name      string `json:"name"`
	Title     string `json:"title,omitempty"`
	StartedOn string `json:"started_on,omitempty"`
}

type searchItem struct {
	Match
	DaData *registrationObservation `json:"dadata,omitempty"`
}

type registrationObservation struct {
	SourceRef     string           `json:"source_ref,omitempty"`
	ID            string           `json:"id"`
	INN           string           `json:"inn"`
	Registration  string           `json:"registration_number"`
	Kind          string           `json:"kind"`
	Name          string           `json:"name,omitempty"`
	KPP           string           `json:"kpp,omitempty"`
	OKVED         string           `json:"okved,omitempty"`
	Status        string           `json:"status,omitempty"`
	RegisteredOn  string           `json:"registered_on,omitempty"`
	LastChangedOn string           `json:"last_changed_on,omitempty"`
	Address       string           `json:"address,omitempty"`
	Manager       *RegistryManager `json:"manager,omitempty"`
	ResidenceCity string           `json:"residence_city,omitempty"`
	Source        string           `json:"source"`
	SourceURL     string           `json:"source_url"`
	AsOf          *string          `json:"as_of"`
	RetrievedAt   string           `json:"retrieved_at"`
}

func (o registrationObservation) identity() Identity {
	return Identity{ID: o.ID, INN: o.INN, Kind: o.Kind, Registration: &o.Registration}
}

type providerResult struct {
	state        string
	observations []registrationObservation
	warnings     []Body
	published    map[string]bool
}

func (p *providerResult) warn(code string) {
	for _, warning := range p.warnings {
		if warning["code"] == code {
			return
		}
	}
	p.warnings = append(p.warnings, Body{"source": "dadata", "code": code})
}

func providerSearch(s snapshot, lookup PartyLookup, mode, query, kind string, firstPage, includeDetails bool) (providerResult, error) {
	p := providerResult{state: "disabled"}
	if lookup == nil {
		return p, nil
	}
	p.state = "not_requested"
	if !firstPage {
		return p, nil
	}
	upstream, cancel := context.WithTimeout(s.request, 1500*time.Millisecond)
	parties, limited, warning := lookup(upstream, query, mode, kind)
	cancel()
	if warning != "" {
		p.state = "unavailable"
		p.warn(warning)
		return p, nil
	}
	p.state = "checked"
	if limited || mode == "name" {
		p.state = "limited"
	}
	sourceURL := "https://dadata.ru/api/find-party/"
	if mode == "name" {
		sourceURL = "https://dadata.ru/api/suggest/party/"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	seen := make(map[string]string)
	for _, raw := range parties {
		o, ok := normalizeParty(raw, includeDetails && mode != "name")
		if !ok || (kind != "" && o.Kind != kind) {
			continue
		}
		if (mode == "inn" && o.INN != query) || (mode == "registration" && o.Registration != query) {
			if mode == "inn" {
				seen[o.Registration] = ""
			}
			p.warn("provider_identity_conflict")
			continue
		}
		if inn, exists := seen[o.Registration]; exists {
			if o.INN != inn {
				seen[o.Registration] = ""
				p.warn("provider_identity_conflict")
			}
			continue
		}
		seen[o.Registration] = o.INN
		o.Source, o.SourceURL, o.RetrievedAt = "dadata", sourceURL, now
		p.observations = append(p.observations, o)
	}
	if len(p.observations) == 0 {
		return p, nil
	}
	numbers := make([]string, 0, len(p.observations))
	for _, observation := range p.observations {
		numbers = append(numbers, observation.Registration)
	}
	ctx, cancel := context.WithTimeout(s.request, 500*time.Millisecond)
	defer cancel()
	rows, err := s.Query(ctx, `
		SELECT r.reg_no, r.inn, EXISTS (
			SELECT 1 FROM published_releases p JOIN sme_observations o ON o.release_id = p.release_id
			WHERE p.dataset_code = 'rsmp' AND p.period_key = 'current' AND o.reg_no = r.reg_no
		) OR EXISTS (
			SELECT 1 FROM tax_regime_observations o
			WHERE o.release_id = (
				SELECT release_id FROM published_releases
				WHERE dataset_code = 'snrip' ORDER BY period_key DESC LIMIT 1
			) AND o.reg_no = r.reg_no
		)
		FROM registrations r WHERE r.reg_no = ANY($1::text[])`, numbers)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.published = make(map[string]bool)
	for rows.Next() {
		var number, inn string
		var published bool
		if err := rows.Scan(&number, &inn, &published); err != nil {
			return p, err
		}
		if claimed := seen[number]; claimed != "" && claimed != inn {
			seen[number] = ""
			p.warn("provider_identity_conflict")
		}
		p.published[number] = published
	}
	p.observations = slices.DeleteFunc(p.observations, func(o registrationObservation) bool {
		return seen[o.Registration] == ""
	})
	return p, rows.Err()
}

func normalizeParty(raw RegistryParty, includeDetails bool) (registrationObservation, bool) {
	o := registrationObservation{
		ID: "reg:" + raw.Registration, INN: raw.INN, Registration: raw.Registration, Kind: raw.Kind,
		Name: raw.Name, OKVED: raw.OKVED, Status: raw.Status,
		RegisteredOn: raw.RegisteredOn, LastChangedOn: raw.LastChangedOn,
	}
	switch {
	case raw.Kind == "legal" && len(raw.INN) == 10 && len(raw.Registration) == 13:
		o.KPP, o.Address = raw.KPP, raw.Address
		if includeDetails && raw.Manager.Name != "" {
			o.Manager = &raw.Manager
		}
	case raw.Kind == "entrepreneur" && len(raw.INN) == 12 && len(raw.Registration) == 15:
		if includeDetails {
			o.ResidenceCity = raw.ResidenceCity
		}
	default:
		return o, false
	}
	return o, validQuery("inn", o.INN) && validQuery("registration", o.Registration)
}

func (p *providerResult) compare(o registrationObservation, identity Identity, sme *SME) {
	if identity.Name != nil && o.Name != "" && !strings.EqualFold(identity.Name.Value, o.Name) {
		p.warn("provider_name_differs")
	}
	if sme != nil && sme.OKVED != nil && o.OKVED != "" && *sme.OKVED != o.OKVED {
		p.warn("provider_okved_differs")
	}
}

func (p providerResult) apply(body Body) {
	body["coverage"].(Body)["dadata"] = p.state
	body["warnings"] = append(body["warnings"].([]Body), p.warnings...)
}

func (p providerResult) unavailable() Result {
	result := Rejected(503, "provider_unavailable", "DaData temporarily unavailable")
	result.Warnings = p.warnings
	return result
}
