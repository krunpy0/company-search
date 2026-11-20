package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/company"
)

type snapshot struct {
	pgx.Tx
	request context.Context
	sources map[string]company.Source
	version string
}

type cursor struct {
	Query   string
	Version string
	Rank    int
	ID      string
}

func RegisterCompanies(r *gin.Engine, pool *pgxpool.Pool, lookup PartyLookup) {
	group := r.Group("/v1/companies")
	// Bound concurrent queries even when PostgreSQL or a broad name search is slow.
	slots := make(chan struct{}, 8)
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			c.Next()
		default:
			c.Header("Retry-After", "1")
			fail(c, 429, "busy", "Too many concurrent requests")
		}
	})
	read := func(handler func(*gin.Context, snapshot) error) gin.HandlerFunc {
		return func(c *gin.Context) {
			request := c.Request.Context()
			ctx, cancel := context.WithTimeout(request, 5*time.Second)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			if err == nil {
				defer func() {
					cleanup, stop := context.WithTimeout(context.WithoutCancel(request), time.Second)
					defer stop()
					_ = tx.Rollback(cleanup)
				}()
				s := snapshot{Tx: tx, request: request}
				s.sources, s.version, err = company.Sources(ctx, tx)
				if err == nil {
					err = handler(c, s)
				}
			}
			if err != nil {
				slog.Error("company read failed", "route", c.FullPath(), "request_id", requestID(c), "error_type", fmt.Sprintf("%T", err))
				fail(c, 503, "data_unavailable", "Local data temporarily unavailable")
			}
		}
	}
	group.GET("", read(func(c *gin.Context, s snapshot) error { return search(c, s, lookup) }))
	get := read(func(c *gin.Context, s snapshot) error { return detail(c, s, lookup) })
	group.GET("/:id", get)
	group.GET("/:id/financials", get)
	group.GET("/:id/sources", get)
}

func requestID(c *gin.Context) string {
	if id := c.GetString("request_id"); id != "" {
		return id
	}
	id := rand.Text()
	c.Set("request_id", id)
	c.Header("X-Request-ID", id)
	return id
}

func fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message, "request_id": requestID(c)}})
}

func search(c *gin.Context, s snapshot, lookup PartyLookup) error {
	query := strings.TrimSpace(c.Query("q"))
	mode, kind := c.DefaultQuery("mode", "auto"), c.Query("kind")
	if mode == "auto" {
		mode = "name"
		if digits(query) {
			mode = "inn"
			if len(query) == 13 || len(query) == 15 {
				mode = "registration"
			}
		}
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > 100 || (kind != "" && kind != "legal" && kind != "entrepreneur") ||
		!validQuery(mode, query) {
		fail(c, 400, "invalid_query", "Invalid q, mode, kind or limit")
		return nil
	}

	signature := sha256.Sum256([]byte(mode + "\x00" + kind + "\x00" + query))
	key := hex.EncodeToString(signature[:])
	page := cursor{Query: key, Version: s.version, Rank: -10002}
	if raw := c.Query("cursor"); raw != "" {
		if len(raw) > 4096 {
			fail(c, 400, "invalid_cursor", "Invalid cursor")
			return nil
		}
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(decoded, &page) != nil || page.Query != key ||
			page.Rank < -10001 || page.Rank > 0 || !validID(page.ID) {
			fail(c, 400, "invalid_cursor", "Invalid cursor")
			return nil
		}
		if page.Version != s.version {
			fail(c, 409, "stale_cursor", "Published releases changed. Restart the search")
			return nil
		}
	}

	items, err := company.Search(c.Request.Context(), s.Tx, mode, query, kind, page.Rank, page.ID, limit+1)
	if err != nil {
		return err
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[limit-1]
		data, err := json.Marshal(cursor{Query: key, Version: s.version, Rank: last.Rank, ID: last.ID})
		if err != nil {
			return err
		}
		encoded := base64.RawURLEncoding.EncodeToString(data)
		next = &encoded
	}
	p, err := providerSearch(s, lookup, mode, query, kind, page.ID == "")
	if err != nil {
		return err
	}
	type searchItem struct {
		company.Match
		DaData *registrationObservation `json:"dadata,omitempty"`
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
			output = append(output, searchItem{Match: company.Match{Identity: o.identity()}, DaData: &o})
		}
	}
	body := gin.H{"items": output, "next_cursor": next, "sources": s.sources,
		"coverage": coverage(s), "warnings": []gin.H{}}
	if mode == "name" && (p.state == "checked" || p.state == "limited") {
		body["external_suggestions"] = append([]registrationObservation{}, p.observations...)
	}
	p.apply(body)
	if len(output) == 0 && mode != "name" && p.state == "unavailable" {
		failProvider(c, p)
		return nil
	}
	c.JSON(200, body)
	return nil
}

func coverage(s snapshot) gin.H {
	local := "partial"
	if len(s.sources) == 0 {
		local = "not_loaded"
	}
	return gin.H{"local": local, "dadata": "disabled"}
}

func detail(c *gin.Context, s snapshot, lookup PartyLookup) error {
	id := c.Param("id")
	if !validID(id) {
		fail(c, 400, "invalid_id", "Use reg:<registration number> or inn:<INN>")
		return nil
	}
	var year int
	route := c.FullPath()
	if raw, present := c.GetQuery("year"); present && strings.HasSuffix(route, "/financials") {
		var err error
		year, err = strconv.Atoi(raw)
		if err != nil || len(raw) != 4 || !digits(raw) || year < 1900 {
			fail(c, 400, "invalid_query", "Invalid report year")
			return nil
		}
	}

	ctx := c.Request.Context()
	mode, number, _ := strings.Cut(id, ":")
	candidates, err := company.Resolve(ctx, s.Tx, mode, number)
	if err != nil {
		return err
	}
	providerMode := "inn"
	if mode == "reg" {
		providerMode = "registration"
	}
	// The local-only routes need DaData just to resolve an otherwise unknown ID.
	useProvider := route == "/v1/companies/:id" || strings.HasSuffix(route, "/sources") || len(candidates) == 0
	p, err := providerSearch(s, lookup, providerMode, number, "", useProvider && len(candidates) < 2)
	if err != nil {
		return err
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
			candidates = append(candidates, company.Detail{Identity: o.identity()})
		}
	}
	switch len(candidates) {
	case 0:
		if p.state == "unavailable" {
			failProvider(c, p)
		} else if len(p.warnings) > 0 {
			c.AbortWithStatusJSON(404, gin.H{
				"error":    gin.H{"code": "company_not_found", "message": "No matching company in the checked sources", "request_id": requestID(c)},
				"warnings": p.warnings,
			})
		} else {
			fail(c, 404, "company_not_found", "No matching company in the checked sources")
		}
		return nil
	case 1:
	default:
		keys := make([]string, 0, len(candidates))
		for _, item := range candidates {
			keys = append(keys, item.ID)
		}
		body := gin.H{"error": gin.H{"code": "ambiguous_identity", "message": "Select a registration",
			"request_id": requestID(c), "candidates": keys}}
		if len(p.warnings) > 0 {
			body["warnings"] = p.warnings
		}
		c.JSON(409, body)
		return nil
	}

	item := candidates[0]
	financials := []company.Financial{}
	if item.Kind == "legal" {
		financials, err = company.Financials(ctx, s.Tx, item.INN)
		if err != nil {
			return err
		}
		if item.Name == nil && len(financials) > 0 {
			latest := financials[0]
			item.Name = &company.Name{Value: latest.DisplayName, Evidence: latest.Evidence}
		}
	}
	warnings := make([]gin.H, 0)
	if item.Registration != nil && len(financials) > 0 {
		registrations, err := company.Resolve(ctx, s.Tx, "inn", item.INN)
		if err != nil {
			return err
		}
		if len(registrations) > 1 || (len(registrations) == 1 && registrations[0].Registration != nil && registrations[0].ID != item.ID) {
			warnings = append(warnings, gin.H{"source": "revexp", "code": "ambiguous_taxpayer_registration"})
		}
	}

	years := make([]int, 0, len(financials))
	filtered := make([]company.Financial, 0, len(financials))
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

	body := gin.H{"id": item.ID, "sources": s.sources, "coverage": coverage(s), "warnings": warnings}
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
	p.apply(body)
	switch {
	case strings.HasSuffix(route, "/financials"):
		body["items"], body["years"], body["state"] = filtered, years, financeState
	case strings.HasSuffix(route, "/sources"):
		observations := make([]gin.H, 0)
		if item.SME != nil {
			observations = append(observations, gin.H{"section": "sme", "evidence": item.SME.Evidence})
		}
		for _, f := range financials {
			observations = append(observations, gin.H{"section": "financials", "year": f.Year, "evidence": f.Evidence})
		}
		body["observations"] = observations
	default:
		body["company"] = item
		body["sections"] = gin.H{
			"registration": gin.H{"state": registrationState},
			"sme":          gin.H{"state": smeState},
			"financials":   gin.H{"state": financeState, "years": years},
			"taxes":        gin.H{"state": "not_loaded"},
		}
	}
	c.JSON(200, body)
	return nil
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
