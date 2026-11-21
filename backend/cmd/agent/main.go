package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/krunpy0/company-search/backend/internal/agent"
	"github.com/krunpy0/company-search/backend/internal/company"
	"github.com/krunpy0/company-search/backend/internal/tools"
)

func main() {
	steps := flag.Int("steps", 6, "maximum model requests (1–12)")
	budget := flag.Int("result-bytes", 24000, "maximum total tool result bytes (1024–64000)")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: go run ./cmd/agent [flags] \"вопрос о компании\"")
		flag.PrintDefaults()
	}
	flag.Parse()
	question := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if question == "" || utf8.RuneCountInString(question) > 2000 || *steps < 1 || *steps > 12 ||
		*budget < 1024 || *budget > 64000 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(question, *steps, *budget); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(question string, steps, budget int) error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("не удалось прочитать .env")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("задайте DATABASE_URL")
	}
	provider, err := agent.NewChatCompletions(os.Getenv("OPENAI_BASE_URL"), os.Getenv("OPENAI_MODEL"), os.Getenv("OPENAI_API_KEY"))
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 2*time.Minute)
	defer timeout()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("не удалось разобрать DATABASE_URL")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return errors.New("не удалось подключиться к PostgreSQL")
	}
	// DaData terms for transmitting observations to the model are unconfirmed.
	run := agent.Run{
		Provider: provider,
		Tools:    tools.Executor{Reader: company.Reader{Pool: pool}},
		Steps:    steps,
		Bytes:    budget,
	}
	answer, err := run.Ask(ctx, question)
	if err != nil {
		return err
	}
	output := json.NewEncoder(os.Stdout)
	output.SetIndent("", "  ")
	output.SetEscapeHTML(false)
	return output.Encode(answer)
}
