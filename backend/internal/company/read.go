package company

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Operation string

const (
	ReadSearch     Operation = "searchCompanies"
	ReadProfile    Operation = "getCompany"
	ReadFinancials Operation = "getCompanyFinancials"
	ReadTaxes      Operation = "getCompanyTaxes"
	ReadSources    Operation = "getCompanySources"
)

type Reader struct {
	Pool                   *pgxpool.Pool
	Lookup                 PartyLookup
	IncludeRegistryDetails bool
}

type Body map[string]any

type Result struct {
	Body     Body
	Error    *ReadError
	Warnings []Body
}

type ReadError struct {
	Status     int      `json:"-"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Candidates []string `json:"candidates,omitempty"`
}

func Rejected(status int, code, message string) Result {
	return Result{Error: &ReadError{Status: status, Code: code, Message: message}}
}

func (r Reader) Read(ctx context.Context, operation Operation, id string, params url.Values) (Result, error) {
	switch operation {
	case ReadSearch:
		return r.search(ctx, params)
	case ReadProfile, ReadFinancials, ReadTaxes, ReadSources:
		return r.detail(ctx, operation, id, params)
	default:
		return Rejected(400, "invalid_operation", "Unknown company operation"), nil
	}
}

func (r Reader) snapshot(request context.Context, read func(context.Context, snapshot) (Result, error)) (Result, error) {
	ctx, cancel := context.WithTimeout(request, 5*time.Second)
	defer cancel()
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Result{}, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(request), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	s := snapshot{Tx: tx, request: request}
	s.sources, s.version, err = Sources(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	return read(ctx, s)
}

func queryParam(params url.Values, key string) (string, bool) {
	return params.Get(key), params.Has(key)
}

func defaultParam(params url.Values, key, fallback string) string {
	if params.Has(key) {
		return params.Get(key)
	}
	return fallback
}

type snapshot struct {
	pgx.Tx
	request context.Context
	sources map[string]Source
	version string
}

type cursor struct {
	Query   string
	Version string
	Rank    int
	ID      string
}

func (r Reader) search(ctx context.Context, params url.Values) (Result, error) {
	query := strings.TrimSpace(params.Get("q"))
	mode, kind := defaultParam(params, "mode", "auto"), params.Get("kind")
	if mode == "auto" {
		mode = "name"
		if digits(query) {
			mode = "inn"
			if len(query) == 13 || len(query) == 15 {
				mode = "registration"
			}
		}
	}
	limit, err := strconv.Atoi(defaultParam(params, "limit", "20"))
	if err != nil || limit < 1 || limit > 100 || (kind != "" && kind != "legal" && kind != "entrepreneur") ||
		!validQuery(mode, query) {
		return Rejected(400, "invalid_query", "Invalid q, mode, kind or limit"), nil
	}

	signature := sha256.Sum256([]byte(mode + "\x00" + kind + "\x00" + query))
	key := hex.EncodeToString(signature[:])
	page := cursor{Query: key, Rank: -10002}
	if raw := params.Get("cursor"); raw != "" {
		if len(raw) > 4096 {
			return Rejected(400, "invalid_cursor", "Invalid cursor"), nil
		}
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(decoded, &page) != nil || page.Query != key ||
			page.Rank < -10001 || page.Rank > 0 || !validID(page.ID) {
			return Rejected(400, "invalid_cursor", "Invalid cursor"), nil
		}
	}
	return r.snapshot(ctx, func(ctx context.Context, s snapshot) (Result, error) {
		if page.ID != "" && page.Version != s.version {
			return Rejected(409, "stale_cursor", "Published releases changed. Restart the search"), nil
		}
		items, err := Search(ctx, s.Tx, mode, query, kind, page.Rank, page.ID, limit+1)
		if err != nil {
			return Result{}, err
		}
		var next *string
		if len(items) > limit {
			items = items[:limit]
			last := items[limit-1]
			data, err := json.Marshal(cursor{Query: key, Version: s.version, Rank: last.Rank, ID: last.ID})
			if err != nil {
				return Result{}, err
			}
			encoded := base64.RawURLEncoding.EncodeToString(data)
			next = &encoded
		}
		p, err := providerSearch(s, r.Lookup, mode, query, kind, page.ID == "", r.IncludeRegistryDetails)
		if err != nil {
			return Result{}, err
		}
		output := make([]searchItem, 0, len(items))
		index := make(map[string]int, len(items))
		for _, item := range items {
			index[item.ID] = len(output)
			output = append(output, searchItem{Match: item})
		}
		for _, o := range p.observations {
			if i, ok := index[o.ID]; ok {
				p.compare(o, output[i].Identity, nil)
				if mode != "name" {
					output[i].DaData = &o
				}
			} else if mode != "name" && !p.published[o.Registration] {
				output = append(output, searchItem{Match: Match{Identity: o.identity()}, DaData: &o})
			}
		}
		body := Body{"items": output, "next_cursor": next, "sources": s.sources,
			"coverage": coverage(s), "warnings": []Body{}}
		if mode == "name" && (p.state == "checked" || p.state == "limited") {
			body["external_suggestions"] = append([]registrationObservation{}, p.observations...)
		}
		p.apply(body)
		if len(output) == 0 && mode != "name" && p.state == "unavailable" {
			return p.unavailable(), nil
		}
		return Result{Body: body}, nil
	})
}

func coverage(s snapshot) Body {
	local := "partial"
	if len(s.sources) == 0 {
		local = "not_loaded"
	}
	return Body{"local": local, "dadata": "disabled"}
}

func (r Reader) detail(ctx context.Context, operation Operation, id string, params url.Values) (Result, error) {
	if !validID(id) {
		return Rejected(400, "invalid_id", "Use reg:<registration number> or inn:<INN>"), nil
	}
	var year int
	isTaxes := operation == ReadTaxes
	isFinancials := operation == ReadFinancials
	asOf, section := "", ""
	limit, offset := 100, 0
	if isTaxes {
		section = defaultParam(params, "section", "regimes")
		if section != "regimes" && section != "payments" && section != "debts" {
			return Rejected(400, "invalid_query", "Invalid tax section"), nil
		}
		if raw, present := queryParam(params, "as_of"); present {
			if _, err := time.Parse(time.DateOnly, raw); err != nil || section == "payments" {
				return Rejected(400, "invalid_query", "Invalid observation date"), nil
			}
			asOf = raw
		}
	}
	if raw, present := queryParam(params, "year"); present && (isFinancials || isTaxes) {
		var err error
		year, err = strconv.Atoi(raw)
		if err != nil || len(raw) != 4 || !digits(raw) || year < 1900 || (isTaxes && section != "payments") {
			return Rejected(400, "invalid_query", "Invalid report year"), nil
		}
	}

	if isTaxes {
		for _, param := range []struct {
			name     string
			value    *int
			min, max int
		}{
			{"limit", &limit, 1, 100}, {"offset", &offset, 0, 1000000},
		} {
			if raw, present := queryParam(params, param.name); present {
				value, err := strconv.Atoi(raw)
				if err != nil || !digits(raw) || value < param.min || value > param.max || section == "regimes" {
					return Rejected(400, "invalid_query", "Invalid tax pagination"), nil
				}
				*param.value = value
			}
		}
	}

	return r.snapshot(ctx, func(ctx context.Context, s snapshot) (Result, error) {
		mode, number, _ := strings.Cut(id, ":")
		candidates, err := Resolve(ctx, s.Tx, mode, number)
		if err != nil {
			return Result{}, err
		}
		providerMode := "inn"
		if mode == "reg" {
			providerMode = "registration"
		}
		// The local-only routes need DaData just to resolve an otherwise unknown ID.
		useProvider := operation == ReadProfile || operation == ReadSources || len(candidates) == 0
		p, err := providerSearch(s, r.Lookup, providerMode, number, "", useProvider && len(candidates) < 2, r.IncludeRegistryDetails && operation == ReadProfile)
		if err != nil {
			return Result{}, err
		}
		local := make(map[string]bool, len(candidates))
		for _, candidate := range candidates {
			local[candidate.ID] = true
		}
		for _, o := range p.observations {
			if !local[o.ID] {
				if len(candidates) == 1 && candidates[0].Registration == nil {
					// The local row describes the taxpayer, not a second registration.
					candidates = candidates[:0]
				}
				candidates = append(candidates, Detail{Identity: o.identity()})
			}
		}
		switch len(candidates) {
		case 0:
			if p.state == "unavailable" {
				return p.unavailable(), nil
			}
			result := Rejected(404, "company_not_found", "No matching company in the checked sources")
			result.Warnings = p.warnings
			return result, nil
		case 1:
		default:
			result := Rejected(409, "ambiguous_identity", "Select a registration")
			result.Warnings = p.warnings
			for _, item := range candidates {
				result.Error.Candidates = append(result.Error.Candidates, item.ID)
			}
			return result, nil
		}

		item := candidates[0]
		regimes := TaxRegimes{}
		var money MoneyTax
		moneyDataset := ""
		if section == "payments" {
			moneyDataset = "paytax"
			period := ""
			if year != 0 {
				period = strconv.Itoa(year)
			}
			money, err = MoneyTaxes(ctx, s.Tx, item, moneyDataset, period, limit, offset)
		} else if section == "debts" {
			moneyDataset = "debtam"
			money, err = MoneyTaxes(ctx, s.Tx, item, moneyDataset, asOf, limit, offset)
		}
		if err != nil {
			return Result{}, err
		}
		if !isFinancials && moneyDataset == "" {
			regimes, err = Regimes(ctx, s.Tx, item, asOf)
			if err != nil {
				return Result{}, err
			}
		}
		financials := []Financial{}
		if item.Kind == "legal" && !isTaxes {
			financials, err = Financials(ctx, s.Tx, item.INN)
			if err != nil {
				return Result{}, err
			}
			if item.Name == nil && len(financials) > 0 {
				latest := financials[0]
				item.Name = &Name{Value: latest.DisplayName, Evidence: latest.Evidence}
			}
		}
		headcounts := []Headcount{}
		if !isFinancials && !isTaxes {
			headcounts, err = Headcounts(ctx, s.Tx, item)
			if err != nil {
				return Result{}, err
			}
		}
		headcountState := availability(s, "sshr2019", 0, len(headcounts) > 0)
		if item.Kind == "entrepreneur" {
			// The dedicated headcount release only covers legal entities.
			headcountState = "not_applicable"
			if len(headcounts) > 0 {
				headcountState = "found"
			}
		}
		warnings := make([]Body, 0)
		if item.Registration != nil && item.Kind == "legal" {
			taxpayerDatasets := make([]string, 0, 3)
			if len(financials) > 0 {
				taxpayerDatasets = append(taxpayerDatasets, "revexp")
			}
			for _, h := range headcounts {
				if h.Scope == "taxpayer" {
					taxpayerDatasets = append(taxpayerDatasets, "sshr2019")
					break
				}
			}
			if len(regimes.Items) > 0 {
				taxpayerDatasets = append(taxpayerDatasets, "snr")
			}
			if len(money.Items) > 0 {
				taxpayerDatasets = append(taxpayerDatasets, moneyDataset)
			}
			if len(taxpayerDatasets) > 0 {
				registrations, err := Resolve(ctx, s.Tx, "inn", item.INN)
				if err != nil {
					return Result{}, err
				}
				if len(registrations) > 1 || (len(registrations) == 1 && registrations[0].Registration != nil && registrations[0].ID != item.ID) {
					for _, dataset := range taxpayerDatasets {
						warnings = append(warnings, Body{"source": dataset, "code": "ambiguous_taxpayer_registration"})
					}
				}
			}
		}

		years := make([]int, 0, len(financials))
		filtered := make([]Financial, 0, len(financials))
		for _, f := range financials {
			years = append(years, f.Year)
			if year == 0 || year == f.Year {
				filtered = append(filtered, f)
			}
		}
		financeState := availability(s, "revexp", year, len(filtered) > 0)
		if item.Kind == "entrepreneur" {
			financeState = "not_applicable"
		}
		smeState := availability(s, "rsmp", 0, item.SME != nil)

		body := Body{"id": item.ID, "sources": s.sources, "coverage": coverage(s), "warnings": warnings}
		registrationState := "not_loaded"
		for _, o := range p.observations {
			if o.ID == item.ID {
				p.compare(o, item.Identity, item.SME)
				body["dadata"] = o
				registrationState = "found"
				break
			}
		}
		if p.state == "unavailable" {
			registrationState = "unavailable"
		}
		if p.state == "checked" && registrationState == "not_loaded" {
			registrationState = "not_published"
		}
		if registrationState != "found" {
			for _, warning := range p.warnings {
				if warning["code"] == "provider_identity_conflict" {
					registrationState = "identity_conflict"
					break
				}
			}
		}
		p.apply(body)
		switch {
		case isFinancials:
			body["items"], body["years"], body["state"] = filtered, years, financeState
		case moneyDataset != "":
			body["items"], body["state"], body["next_offset"] = money.Items, money.State, money.NextOffset
		case isTaxes:
			body["items"], body["as_of"], body["state"] = regimes.Items, regimes.AsOf, regimes.State
		case operation == ReadSources:
			observations := make([]Body, 0)
			if item.SME != nil {
				observations = append(observations, Body{"section": "sme", "evidence": item.SME.Evidence})
			}
			for _, f := range financials {
				observations = append(observations, Body{"section": "financials", "year": f.Year, "evidence": f.Evidence})
			}
			for _, h := range headcounts {
				observations = append(observations, Body{"section": "headcount", "year": h.Year, "evidence": h.Evidence})
			}
			for _, r := range regimes.Items {
				observations = append(observations, Body{"section": "regimes", "evidence": r.Evidence})
			}
			body["observations"] = observations
		default:
			body["company"] = item
			body["headcounts"] = headcounts
			body["sections"] = Body{
				"registration": Body{"state": registrationState},
				"sme":          Body{"state": smeState},
				"financials":   Body{"state": financeState, "years": years},
				"headcount":    Body{"state": headcountState},
				"taxes":        Body{"state": regimes.State},
			}
		}
		return Result{Body: body}, nil
	})
}

func availability(s snapshot, dataset string, year int, found bool) string {
	if found {
		return "found"
	}
	for _, source := range s.sources {
		if source.Dataset == dataset && (year == 0 || source.Period == strconv.Itoa(year)) {
			return "not_published"
		}
	}
	return "not_loaded"
}

func validID(id string) bool {
	mode, number, _ := strings.Cut(id, ":")
	switch mode {
	case "inn":
		return validQuery("inn", number)
	case "reg":
		return validQuery("registration", number)
	}
	return false
}

func validQuery(mode, value string) bool {
	switch mode {
	case "name":
		n := utf8.RuneCountInString(value)
		if n < 3 || n > 200 || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
			return false
		}
		return strings.ContainsFunc(value, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) })
	case "inn":
		if !digits(value) {
			return false
		}
		switch len(value) {
		case 10:
			return checkDigit(value, []int{2, 4, 10, 3, 5, 9, 4, 6, 8})
		case 12:
			return checkDigit(value, []int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}) &&
				checkDigit(value, []int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8})
		}
	case "registration":
		if digits(value) && (len(value) == 13 || len(value) == 15) {
			prefix, _ := strconv.ParseInt(value[:len(value)-1], 10, 64)
			divisor := int64(len(value) - 2)
			return prefix%divisor%10 == int64(value[len(value)-1]-'0')
		}
	}
	return false
}

func digits(value string) bool {
	return value != "" && !strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' })
}

func checkDigit(value string, weights []int) bool {
	sum := 0
	for i, weight := range weights {
		sum += int(value[i]-'0') * weight
	}
	return sum%11%10 == int(value[len(weights)]-'0')
}
