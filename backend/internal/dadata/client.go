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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/company"
	"github.com/krunpy0/company-search/backend/internal/quota"
)

const endpoint = "https://suggestions.dadata.ru"

type party struct {
	INN, OGRN, Type, KPP, OKVED string
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
	Address    struct{ Value string }
	Management struct {
		Name, Post string
		StartDate  int64 `json:"start_date"`
	}
}

type Client struct {
	token, baseURL string
	http           *http.Client
	reserve        func(context.Context) string
	setCooldown    func(context.Context, time.Time) error
}

func New(token string, budget int, pool *pgxpool.Pool, userBudget int) (*Client, error) {
	if token == "" || strings.ContainsAny(token, " \t\r\n") || budget < 1 || pool == nil || userBudget < 1 {
		return nil, errors.New("DaData requires a token, database and positive budgets")
	}
	gate := quota.New(pool, "dadata", token, budget, userBudget)
	return &Client{
		token: token, baseURL: endpoint, reserve: gate.Reserve, setCooldown: gate.Backoff,
		http: &http.Client{
			Timeout: 1500 * time.Millisecond,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) backoff(ctx context.Context, retryAfter string, now time.Time) error {
	until := now.Add(time.Second)
	if seconds, err := strconv.ParseInt(retryAfter, 10, 32); err == nil && seconds > 0 {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(retryAfter); err == nil && date.After(until) {
		until = date
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	return c.setCooldown(ctx, until)
}

// Lookup returns a public warning code on failure. Provider errors may contain
// sensitive data, so they never escape the client.
func (c *Client) Lookup(ctx context.Context, query, mode, kind string) ([]company.RegistryParty, bool, string) {
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
	if warning := c.reserve(ctx); warning != "" {
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
		if err := c.backoff(ctx, res.Header.Get("Retry-After"), time.Now()); err != nil {
			return nil, false, "provider_unavailable"
		}
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
			Data  party
		}
	}
	if len(data) > maxBody || json.Unmarshal(data, &result) != nil || result.Suggestions == nil {
		return nil, false, "provider_invalid_response"
	}
	parties := make([]company.RegistryParty, 0, min(count, len(result.Suggestions)))
	for _, suggestion := range result.Suggestions[:min(count, len(result.Suggestions))] {
		raw := suggestion.Data
		p := company.RegistryParty{
			INN: raw.INN, Registration: raw.OGRN, KPP: raw.KPP, OKVED: raw.OKVED,
			Name: strings.TrimSpace(raw.Name.ShortWithOPF), Status: raw.State.Status,
			RegisteredOn:  registryDate(raw.State.RegistrationDate),
			LastChangedOn: registryDate(raw.State.ActualityDate),
		}
		if p.Name == "" {
			p.Name = strings.TrimSpace(raw.Name.FullWithOPF)
		}
		if p.Name == "" {
			p.Name = strings.TrimSpace(suggestion.Value)
		}
		switch {
		case raw.Type == "LEGAL" && raw.BranchType == "MAIN":
			p.Kind, p.Address = "legal", strings.TrimSpace(raw.Address.Value)
			if mode != "name" {
				p.Manager = company.RegistryManager{
					Name:      strings.TrimSpace(raw.Management.Name),
					Title:     strings.TrimSpace(raw.Management.Post),
					StartedOn: registryDate(raw.Management.StartDate),
				}
			}
		case raw.Type == "INDIVIDUAL" && raw.BranchType != "BRANCH":
			p.Kind = "entrepreneur"
			if mode != "name" {
				p.ResidenceCity = strings.TrimSpace(raw.Address.Value)
			}
		default:
			continue
		}
		parties = append(parties, p)
	}
	return parties, len(result.Suggestions) >= count, ""
}

func registryDate(timestamp int64) string {
	if timestamp <= 0 {
		return ""
	}
	return time.UnixMilli(timestamp).In(time.FixedZone("MSK", 3*60*60)).Format(time.DateOnly)
}

func transportWarning(err error) string {
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "provider_timeout"
	}
	return "provider_unavailable"
}
