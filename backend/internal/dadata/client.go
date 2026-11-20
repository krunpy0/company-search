package dadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const endpoint = "https://suggestions.dadata.ru"

type Party struct {
	INN, OGRN, Type, KPP, OKVED string
	DisplayName                 string `json:"-"`
	BranchType                  string `json:"branch_type"`
	Name                        struct {
		FullWithOPF  string `json:"full_with_opf"`
		ShortWithOPF string `json:"short_with_opf"`
	}
	State struct {
		Status           string
		RegistrationDate int64 `json:"registration_date"`
		ActualityDate    int64 `json:"actuality_date"`
	}
	Address struct{ Value string }
}

type Client struct {
	token, baseURL string
	http           *http.Client
	budget         int

	mu       sync.Mutex
	day      string
	used     int
	next     time.Time
	cooldown time.Time
}

func New(token string, budget int) (*Client, error) {
	if token == "" || strings.ContainsAny(token, " \t\r\n") || budget < 1 {
		return nil, errors.New("DaData requires a token and a positive daily budget")
	}
	return &Client{
		token: token, baseURL: endpoint, budget: budget,
		http: &http.Client{
			Timeout: 1500 * time.Millisecond,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) reserve(now time.Time) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	// shortcut: limits are process-local, use a shared budget before adding replicas.
	day := now.In(time.FixedZone("MSK", 3*60*60)).Format(time.DateOnly)
	if c.day != day {
		c.day, c.used = day, 0
	}
	switch {
	case now.Before(c.cooldown):
		return "provider_rate_limited"
	case c.used >= c.budget:
		return "provider_budget_exhausted"
	case now.Before(c.next):
		return "provider_rate_limited"
	}
	c.used++
	c.next = now.Add(time.Second)
	return ""
}

func (c *Client) backoff(retryAfter string, now time.Time) {
	until := now.Add(time.Second)
	if seconds, err := strconv.ParseInt(retryAfter, 10, 32); err == nil && seconds > 0 {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(retryAfter); err == nil && date.After(until) {
		until = date
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if until.After(c.cooldown) {
		c.cooldown = until
	}
}

// Lookup returns a public warning code on failure. Provider errors may contain
// sensitive data, so they never escape the client.
func (c *Client) Lookup(ctx context.Context, query, mode, kind string) ([]Party, bool, string) {
	method, count := "findById", 300
	if mode == "name" {
		method, count = "suggest", 20
	}
	payload := map[string]any{"query": query, "count": count}
	if mode != "name" && (len(query) == 10 || len(query) == 13) {
		payload["branch_type"] = "MAIN"
	}
	if kind == "legal" {
		payload["type"] = "LEGAL"
	} else if kind == "entrepreneur" {
		payload["type"] = "INDIVIDUAL"
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/suggestions/api/4_1/rs/"+method+"/party", bytes.NewReader(body))
	if err != nil || ctx.Err() != nil {
		return nil, false, "provider_unavailable"
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if warning := c.reserve(time.Now()); warning != "" {
		return nil, false, warning
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, false, transportWarning(err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusForbidden:
		return nil, false, "provider_forbidden"
	case http.StatusTooManyRequests:
		c.backoff(res.Header.Get("Retry-After"), time.Now())
		return nil, false, "provider_rate_limited"
	case http.StatusOK:
	default:
		return nil, false, "provider_unavailable"
	}
	const maxBody = 8 << 20
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return nil, false, transportWarning(err)
	}
	var result struct {
		Suggestions []struct {
			Value string
			Data  Party
		}
	}
	if len(data) > maxBody || json.Unmarshal(data, &result) != nil || result.Suggestions == nil {
		return nil, false, "provider_invalid_response"
	}
	parties := make([]Party, 0, min(count, len(result.Suggestions)))
	for _, suggestion := range result.Suggestions[:min(count, len(result.Suggestions))] {
		suggestion.Data.DisplayName = suggestion.Value
		parties = append(parties, suggestion.Data)
	}
	return parties, len(result.Suggestions) >= count, ""
}

func transportWarning(err error) string {
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "provider_timeout"
	}
	return "provider_unavailable"
}
