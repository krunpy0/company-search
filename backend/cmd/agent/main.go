package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/krunpy0/company-search/backend/internal/agent"
	"github.com/krunpy0/company-search/backend/internal/company"
	"github.com/krunpy0/company-search/backend/internal/dadata"
	"github.com/krunpy0/company-search/backend/internal/checko"
	"github.com/krunpy0/company-search/backend/internal/tools"
)

func main() {
	steps := flag.Int("steps", 6, "maximum model requests (1–12)")
	budget := flag.Int("result-bytes", 24000, "maximum total tool result bytes (1024–64000)")
	chat := flag.Bool("chat", false, "continue an interactive conversation after the first answer")
	rawJSON := flag.Bool("json", false, "print structured results and source metadata")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: go run ./cmd/agent [flags] \"вопрос о компании\"")
		flag.PrintDefaults()
	}
	flag.Parse()
	question := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if utf8.RuneCountInString(question) > 2000 || *steps < 1 || *steps > 12 ||
		*budget < 1024 || *budget > 64000 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(question, *steps, *budget, *rawJSON, *chat || question == ""); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(question string, steps, budget int, rawJSON, chat bool) error {
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
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("не удалось разобрать DATABASE_URL")
	}
	defer pool.Close()
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(startup); err != nil {
		return errors.New("не удалось подключиться к PostgreSQL")
	}
	reader := company.Reader{Pool: pool}
	if value := os.Getenv("DADATA_AGENT_ENABLED"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return errors.New("некорректное значение DADATA_AGENT_ENABLED")
		}
		if enabled {
			dailyBudget, err := strconv.Atoi(os.Getenv("DADATA_DAILY_BUDGET"))
			if err != nil {
				return errors.New("задайте положительный целочисленный DADATA_DAILY_BUDGET")
			}
			client, err := dadata.New(os.Getenv("DADATA_TOKEN"), dailyBudget)
			if err != nil {
				return errors.New("для DaData задайте DADATA_TOKEN и положительный DADATA_DAILY_BUDGET")
			}
			reader.Lookup = client.Lookup
		}
	}
	events, err := eventSources()
	if err != nil {
		return err
	}
	run := agent.Run{
		Provider: provider,
		Tools:    tools.Executor{Reader: reader, Events: &events},
		Steps:    steps,
		Bytes:    budget,
	}
	session := &agent.Session{Run: run}
	ask := func(question string) (agent.Answer, error) {
		if utf8.RuneCountInString(question) > 2000 {
			return agent.Answer{}, errors.New("вопрос длиннее 2000 символов")
		}
		interrupt, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		turn, cancel := context.WithTimeout(interrupt, 2*time.Minute)
		defer cancel()
		return session.Ask(turn, question)
	}
	output := json.NewEncoder(os.Stdout)
	output.SetIndent("", "  ")
	output.SetEscapeHTML(false)
	emit := func(question string) error {
		answer, err := ask(question)
		if err != nil {
			return err
		}
		if rawJSON {
			return output.Encode(answer)
		}
		_, err = fmt.Fprintln(os.Stdout, agent.Text(answer))
		return err
	}
	if question != "" {
		if err := emit(question); err != nil {
			return err
		}
	}
	if !chat {
		return nil
	}
	fmt.Fprintln(os.Stderr, "Диалог. /new — новая компания, /exit — выход.")
	return conversation(os.Stdin, os.Stderr, emit, func() { session = &agent.Session{Run: run} })
}

func eventSources() (tools.Events, error) {
	var events tools.Events
	var enableChecko bool
	flags := []struct {
		name   string
		target *bool
	}{
		{"FNS_SUPPORT_AGENT_ENABLED", &events.Support},
		{"FNS_TAX_OFFENCES_AGENT_ENABLED", &events.TaxOffences},
		{"CHECKO_AGENT_ENABLED", &enableChecko},
	}
	for _, flag := range flags {
		if raw := os.Getenv(flag.name); raw != "" {
			enabled, err := strconv.ParseBool(raw)
			if err != nil {
				return events, fmt.Errorf("некорректное значение %s", flag.name)
			}
			*flag.target = enabled
		}
	}
	if enableChecko {
		budget := 100
		if raw := os.Getenv("CHECKO_DAILY_BUDGET"); raw != "" {
			var err error
			budget, err = strconv.Atoi(raw)
			if err != nil {
				return events, errors.New("задайте CHECKO_DAILY_BUDGET от 1 до 100")
			}
		}
		var err error
		events.Checko, err = checko.New(os.Getenv("CHECKO_API_KEY"), budget)
		if err != nil {
			return events, err
		}
	}
	return events, nil
}

func conversation(input io.Reader, prompt io.Writer, ask func(string) error, reset func()) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 16384)
	for {
		fmt.Fprint(prompt, "Вы> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		question := strings.TrimSpace(scanner.Text())
		switch question {
		case "":
			continue
		case "/exit":
			return nil
		case "/new":
			reset()
			continue
		}
		if err := ask(question); err != nil {
			fmt.Fprintln(prompt, err)
		}
	}
}
