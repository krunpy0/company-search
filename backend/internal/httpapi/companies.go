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
	sources map[string]company.Source
	version string
}

type cursor struct {
	Query   string
	Version string
	Rank    int
	ID      string
}

func RegisterCompanies(r *gin.Engine, pool *pgxpool.Pool) {
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
			ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			if err == nil {
				defer tx.Rollback(ctx)
				var s snapshot
				s.Tx = tx
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
	group.GET("", read(search))
	group.GET("/:id", read(detail))
	group.GET("/:id/financials", read(detail))
	group.GET("/:id/sources", read(detail))
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

func search(c *gin.Context, s snapshot) error {
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
	c.JSON(200, gin.H{"items": items, "next_cursor": next, "sources": s.sources,
		"coverage": coverage(s), "warnings": []any{}})
	return nil
}

func coverage(s snapshot) gin.H {
	local := "partial"
	if len(s.sources) == 0 {
		local = "not_loaded"
	}
	return gin.H{"local": local, "dadata": "disabled"}
}

func detail(c *gin.Context, s snapshot) error {
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
	switch len(candidates) {
	case 0:
		fail(c, 404, "company_not_found", "No matching company in the published local data")
		return nil
	case 1:
	default:
		keys := make([]string, 0, len(candidates))
		for _, item := range candidates {
			keys = append(keys, item.ID)
		}
		c.JSON(409, gin.H{"error": gin.H{"code": "ambiguous_identity", "message": "Select a registration",
			"request_id": requestID(c), "candidates": keys}})
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
		if len(registrations) > 1 {
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
			"registration": gin.H{"state": "not_loaded"},
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
