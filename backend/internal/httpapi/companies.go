package httpapi

import (
	"crypto/rand"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/company"
	"log/slog"
)

type PartyLookup = company.PartyLookup

func RegisterCompaniesWithLimiters(r gin.IRouter, pool *pgxpool.Pool, lookup PartyLookup, registryDetails bool, limiters *RateLimiters) {
	if limiters == nil {
		limiters = NewRateLimiters(DefaultRateLimitConfig())
	}
	group := r.Group("/v1/companies")
	// Bound concurrent queries even when PostgreSQL or a broad name search is slow.
	slots := make(chan struct{}, 8)
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
	}, limiters.API(), func(c *gin.Context) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			c.Next()
		default:
			c.Header("Retry-After", "1")
			fail(c, 429, "busy", "Too many concurrent requests")
		}
	})

	reader := company.Reader{Pool: pool, Lookup: lookup, IncludeRegistryDetails: registryDetails}
	read := func(operation company.Operation) gin.HandlerFunc {
		return func(c *gin.Context) {
			result, err := reader.Read(c.Request.Context(), operation, c.Param("id"), c.Request.URL.Query())
			if err != nil {
				slog.Error("company read failed", "route", c.FullPath(), "request_id", requestID(c), "error_type", fmt.Sprintf("%T", err))
				fail(c, 503, "data_unavailable", "Local data temporarily unavailable")
				return
			}
			if rejection := result.Error; rejection != nil {
				details := gin.H{"code": rejection.Code, "message": rejection.Message, "request_id": requestID(c)}
				if len(rejection.Candidates) > 0 {
					details["candidates"] = rejection.Candidates
				}
				body := gin.H{"error": details}
				if len(result.Warnings) > 0 {
					body["warnings"] = result.Warnings
				}
				c.AbortWithStatusJSON(rejection.Status, body)
				return
			}
			c.JSON(200, result.Body)
		}
	}
	group.GET("", read(company.ReadSearch))
	group.GET("/:id", read(company.ReadProfile))
	group.GET("/:id/financials", read(company.ReadFinancials))
	group.GET("/:id/taxes", read(company.ReadTaxes))
	group.GET("/:id/sources", read(company.ReadSources))
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
