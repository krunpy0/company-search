package checko

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrQuota = errors.New("Checko quota exhausted")

const baseURL = "https://api.checko.ru/v2/"

type Client struct {
	key, base string
	budget    int
	http      *http.Client
	mu        sync.Mutex
	day       string
	requests  int
}

func New(key string, budget int) (*Client, error) {
	if strings.TrimSpace(key) == "" || budget < 1 || budget > 100 {
		return nil, errors.New("set CHECKO_API_KEY and CHECKO_DAILY_BUDGET between 1 and 100")
	}
	return &Client{
		key: key, base: baseURL, budget: budget,
		http: &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}, nil
}

type identity struct {
	INN    string `json:"ИНН"`
	OGRN   string `json:"ОГРН"`
	OGRNIP string `json:"ОГРНИП"`
}

type response struct {
	Company      *identity       `json:"company"`
	Entrepreneur *identity       `json:"entrepreneur"`
	Data         json.RawMessage `json:"data"`
	Reports      struct {
		Links map[string]string `json:"Отчет"`
	} `json:"bo.nalog.ru"`
	Meta struct {
		Status       string `json:"status"`
		RequestCount int    `json:"today_request_count"`
	} `json:"meta"`
}

type eventPage struct {
	Pages   *int                         `json:"СтрВсего"`
	Page    *int                         `json:"СтрТекущ"`
	Records []map[string]json.RawMessage `json:"Записи"`
}

type Page struct {
	Items     []map[string]any
	Next      int
	Retrieved time.Time
}

type method struct {
	fields map[string]string
	number string
}

var methods = map[string]method{
	"legal-cases":         {map[string]string{"Номер": "case_number", "СтрКАД": "record_url", "Дата": "opened_on", "Суд": "court", "СуммИск": "claim_amount_rub"}, "Номер"},
	"enforcements":        {map[string]string{"ИспПрНомер": "proceeding_number", "ИспПрДата": "opened_on", "ПредмИсп": "subject", "СумДолг": "debt_amount_rub", "ОстЗадолж": "outstanding_amount_rub"}, "ИспПрНомер"},
	"inspections":         {map[string]string{"Номер": "inspection_number", "Статус": "status", "Заверш": "completed", "Наруш": "violations_reported", "ТипПров": "inspection_type", "ДатаНач": "started_on", "ДатаОконч": "ended_on"}, "Номер"},
	"contracts":           {map[string]string{"РегНомер": "contract_number", "СтрЕИС": "record_url", "Дата": "signed_on", "Цена": "price_rub"}, "РегНомер"},
	"fedresurs":           {map[string]string{"GUID": "message_id", "URL": "record_url", "Дата": "published_on", "ТипНаим": "message_type"}, "GUID"},
	"bankruptcy-messages": {map[string]string{"GUID": "message_id", "URL": "record_url", "Дата": "published_on", "ТипНаим": "message_type", "НомерДела": "case_number"}, "GUID"},
	"timeline":            {map[string]string{"Дата": "event_on", "Событие": "event"}, "Дата"},
	"finances":            {},
}

var moscow = time.FixedZone("MSK", 3*60*60)

func (c *Client) Fetch(ctx context.Context, endpoint, mode, number, law, role string, page, limit int) (Page, error) {
	spec, ok := methods[endpoint]
	if !ok {
		return Page{}, errors.New("unsupported Checko method")
	}
	params := map[string]any{"key": c.key}
	unpaged := endpoint == "finances" || endpoint == "timeline"
	if !unpaged {
		params["page"], params["limit"], params["sort"] = page, limit, "-date"
	}
	if mode == "reg" {
		params["ogrn"] = number
	} else {
		params["inn"] = number
	}
	if endpoint == "contracts" {
		params["law"] = law
	}
	if role != "" {
		params["role"] = role
	}
	data, err := json.Marshal(params)
	if err != nil {
		return Page{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+endpoint, bytes.NewReader(data))
	if err != nil {
		return Page{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	day := time.Now().In(moscow).Format(time.DateOnly)
	c.mu.Lock()
	if c.day != day {
		c.day, c.requests = day, 0
	}
	if c.requests >= c.budget {
		c.mu.Unlock()
		return Page{}, ErrQuota
	}
	c.requests++
	c.mu.Unlock()

	resp, err := c.http.Do(req)
	if err != nil {
		return Page{}, errors.New("Checko request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		c.stopForDay(day)
		return Page{}, ErrQuota
	}
	if resp.StatusCode != http.StatusOK {
		return Page{}, fmt.Errorf("Checko returned HTTP %d", resp.StatusCode)
	}
	const maxResponse = 2 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return Page{}, errors.New("Checko response unreadable or too large")
	}
	var result response
	if json.Unmarshal(raw, &result) != nil || result.Meta.Status != "ok" || len(result.Data) == 0 {
		return Page{}, errors.New("Checko returned an unexpected page")
	}
	if result.Meta.RequestCount >= 100 {
		c.stopForDay(day)
	}
	subject := result.Company
	if len(number) == 15 {
		subject = result.Entrepreneur
	}
	if subject == nil || (mode == "reg" && subject.OGRN != number && subject.OGRNIP != number) ||
		(mode == "inn" && subject.INN != number) {
		return Page{}, errors.New("Checko response identity does not match the request")
	}
	out := Page{Items: make([]map[string]any, 0, limit), Retrieved: time.Now().UTC()}
	if endpoint == "finances" {
		return financialPage(result, out, page, limit)
	}
	var events eventPage
	if endpoint == "timeline" {
		if err := json.Unmarshal(result.Data, &events.Records); err != nil {
			return Page{}, errors.New("invalid Checko timeline")
		}
		slices.SortStableFunc(events.Records, func(a, b map[string]json.RawMessage) int {
			return bytes.Compare(b["Дата"], a["Дата"])
		})
		events.Records, out.Next = paginate(events.Records, page, limit)
	} else if json.Unmarshal(result.Data, &events) != nil || events.Pages == nil || *events.Pages < 0 || len(events.Records) > limit {
		return Page{}, errors.New("invalid Checko event page")
	} else if *events.Pages > 0 && page <= *events.Pages &&
		(events.Page == nil || *events.Page != page || len(events.Records) == 0) {
		return Page{}, errors.New("Checko response did not contain the requested page")
	} else if page < *events.Pages {
		out.Next = page + 1
	}
	for _, record := range events.Records {
		if scalar(record[spec.number]) == nil || scalar(record[spec.number]) == "" {
			return Page{}, errors.New("Checko event is missing its identifier")
		}
		item := make(map[string]any)
		for field, name := range spec.fields {
			if value := scalar(record[field]); value != nil {
				if name == "record_url" && !recordURL(endpoint, value) {
					continue
				}
				item[name] = value
			}
		}
		if role != "" {
			item["role_filter"] = role
		} else if endpoint == "legal-cases" && subject.INN != "" {
			var roles []string
			if partyListed(record["Ист"], subject.INN) {
				roles = append(roles, "plaintiff")
			}
			if partyListed(record["Ответ"], subject.INN) {
				roles = append(roles, "defendant")
			}
			if len(roles) != 0 {
				item["roles"] = roles
			}
		}
		if law != "" {
			item["law"] = law
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func partyListed(raw json.RawMessage, inn string) bool {
	var parties []identity
	if json.Unmarshal(raw, &parties) != nil {
		return false
	}
	return slices.ContainsFunc(parties, func(party identity) bool { return party.INN == inn })
}

func financialPage(result response, out Page, page, limit int) (Page, error) {
	var reports map[string]map[string]json.Number
	if err := json.Unmarshal(result.Data, &reports); err != nil {
		return Page{}, errors.New("invalid Checko financial reports")
	}
	years := make([]string, 0, len(reports))
	for year, lines := range reports {
		n, err := strconv.Atoi(year)
		if err == nil && len(year) == 4 && n >= 2011 && n <= time.Now().Year() && len(lines) != 0 {
			years = append(years, year)
		}
	}
	slices.Sort(years)
	slices.Reverse(years)
	years, out.Next = paginate(years, page, limit)
	for _, year := range years {
		row := map[string]any{"report_year": year, "currency": "RUB"}
		for code, name := range map[string]string{
			"2110": "revenue", "2400": "net_profit", "1600": "total_assets",
			"1300": "equity", "1400": "long_term_liabilities", "1500": "short_term_liabilities",
		} {
			if value := reports[year][code]; value != "" {
				row[name] = value
			}
		}
		if url := result.Reports.Links[year]; recordURL("finances", url) {
			row["record_url"] = url
		}
		out.Items = append(out.Items, row)
	}
	return out, nil
}

func paginate[T any](items []T, page, limit int) ([]T, int) {
	start := min((page-1)*limit, len(items))
	end := min(start+limit, len(items))
	next := 0
	if end < len(items) {
		next = page + 1
	}
	return items[start:end], next
}

func (c *Client) stopForDay(day string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.day == day {
		c.requests = c.budget
	}
}

func scalar(raw json.RawMessage) any {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if dec.Decode(&value) != nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		if len(v) <= 1500 {
			return v
		}
	case bool, json.Number:
		return v
	}
	return nil
}

func recordURL(endpoint string, value any) bool {
	raw, ok := value.(string)
	if !ok {
		return false
	}
	link, err := url.Parse(raw)
	if err != nil || link.Scheme != "https" || link.User != nil {
		return false
	}
	switch endpoint {
	case "legal-cases":
		return link.Host == "kad.arbitr.ru"
	case "contracts":
		return link.Host == "zakupki.gov.ru"
	case "fedresurs", "bankruptcy-messages":
		return link.Host == "fedresurs.ru" || link.Host == "bankrot.fedresurs.ru"
	case "finances":
		return link.Host == "bo.nalog.gov.ru" || link.Host == "bo.nalog.ru"
	}
	return false
}
