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
	"github.com/krunpy0/company-search/backend/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("could not load .env")
		os.Exit(1)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	if err := runAutoMigrate(ctx, dsn, migrations.Apply); err != nil {
		slog.Error("auto-migration failed", "error", err)
		os.Exit(1)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		slog.Error("invalid DATABASE_URL")
		os.Exit(1)
	}
	defer pool.Close()
	r := gin.New()
	limiters, err := configureRateLimiting(r, os.Getenv)
	if err != nil {
		slog.Error("invalid rate limit or proxy configuration", "error", err)
		os.Exit(1)
	}
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
	r.Use(limiters.IP())
	var lookup httpapi.PartyLookup
	enabled, err := strconv.ParseBool(env("DADATA_ENABLED", "false"))
	if err != nil {
		slog.Error("invalid DADATA_ENABLED")
		os.Exit(1)
	}
	if enabled {
		budget, _ := strconv.Atoi(os.Getenv("EXTERNAL_API_DAILY_BUDGET"))
		userBudget, _ := strconv.Atoi(env("USER_EXTERNAL_API_DAILY_BUDGET", "5"))
		// Company reads hold a snapshot connection while calling the provider.
		quotaPool, err := pgxpool.NewWithConfig(context.Background(), pool.Config())
		if err != nil {
			slog.Error("could not create quota pool")
			os.Exit(1)
		}
		defer quotaPool.Close()
		client, err := dadata.New(os.Getenv("DADATA_TOKEN"), budget, quotaPool, userBudget)
		if err != nil {
			slog.Error("DaData requires DADATA_TOKEN and positive EXTERNAL_API_DAILY_BUDGET and USER_EXTERNAL_API_DAILY_BUDGET")
			os.Exit(1)
		}
		lookup = client.Lookup
	}
	registryDetails, err := strconv.ParseBool(env("DADATA_REGISTRY_DETAILS_ENABLED", "false"))
	if err != nil || (registryDetails && !enabled) {
		slog.Error("DADATA_REGISTRY_DETAILS_ENABLED requires a boolean and DaData must be enabled to use it")
		os.Exit(1)
	}
	session := httpapi.RegisterAuthWithLimiters(r, pool, limiters)
	authenticated := r.Group("", session)
	httpapi.RegisterCompaniesWithLimiters(authenticated, pool, lookup, registryDetails, limiters)
	chatService, err := configureChat(pool, lookup)
	if err != nil {
		slog.Error("invalid chat configuration", "error", err)
		os.Exit(1)
	}
	httpapi.RegisterChatWithLimiters(authenticated, chatService, limiters)
	go httpapi.MaintainSessions(ctx, pool)
	srv := &http.Server{
		Addr: env("HTTP_ADDR", ":8080"), Handler: r,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("starting HTTP server", "address", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "error", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "error", err)
	}
}

func runAutoMigrate(ctx context.Context, dsn string, apply func(context.Context, string) error) error {
	autoMigrate, err := strconv.ParseBool(env("AUTO_MIGRATE", "true"))
	if err != nil {
		return errors.New("invalid AUTO_MIGRATE")
	}
	if !autoMigrate {
		slog.Info("skipping auto-migration", "reason", "AUTO_MIGRATE=false")
		return nil
	}
	return apply(ctx, dsn)
}

func configureRateLimiting(r *gin.Engine, getenv func(string) string) (*httpapi.RateLimiters, error) {
	proxies, err := httpapi.ParseTrustedProxies(getenv("TRUSTED_PROXIES"))
	if err != nil {
		return nil, err
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		return nil, err
	}
	cfg, err := httpapi.RateLimitConfigFromEnv(getenv)
	if err != nil {
		return nil, err
	}
	return httpapi.NewRateLimiters(cfg), nil
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
