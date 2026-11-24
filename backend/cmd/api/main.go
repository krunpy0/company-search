package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/krunpy0/company-search/backend/internal/dadata"
	"github.com/krunpy0/company-search/backend/internal/httpapi"
)

func main() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("could not load .env")
		os.Exit(1)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		slog.Error("invalid DATABASE_URL")
		os.Exit(1)
	}
	defer pool.Close()
	r := gin.New()
	r.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		slog.Error("request panic", "route", c.FullPath())
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			c.JSON(503, gin.H{"status": "unready"})
			return
		}
		c.Status(204)
	})
	var lookup httpapi.PartyLookup
	enabled, err := strconv.ParseBool(env("DADATA_ENABLED", "false"))
	if err != nil {
		slog.Error("invalid DADATA_ENABLED")
		os.Exit(1)
	}
	if enabled {
		budget, _ := strconv.Atoi(os.Getenv("DADATA_DAILY_BUDGET"))
		client, err := dadata.New(os.Getenv("DADATA_TOKEN"), budget)
		if err != nil {
			slog.Error("DaData requires DADATA_TOKEN and a positive DADATA_DAILY_BUDGET")
			os.Exit(1)
		}
		lookup = client.Lookup
	}
	registryDetails, err := strconv.ParseBool(env("DADATA_REGISTRY_DETAILS_ENABLED", "false"))
	if err != nil || (registryDetails && !enabled) {
		slog.Error("DADATA_REGISTRY_DETAILS_ENABLED requires a boolean and DaData must be enabled to use it")
		os.Exit(1)
	}
	httpapi.RegisterCompanies(r, pool, lookup, registryDetails)
	srv := &http.Server{Addr: env("HTTP_ADDR", ":8080"), Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("starting HTTP server", "address", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "error", err)
			os.Exit(1)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown", "error", err)
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
