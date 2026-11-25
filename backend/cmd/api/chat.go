package main

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krunpy0/company-search/backend/internal/agent"
	"github.com/krunpy0/company-search/backend/internal/chat"
	"github.com/krunpy0/company-search/backend/internal/company"
	"github.com/krunpy0/company-search/backend/internal/httpapi"
	"github.com/krunpy0/company-search/backend/internal/quota"
	"github.com/krunpy0/company-search/backend/internal/tools"
)

func configureChat(pool *pgxpool.Pool, lookup httpapi.PartyLookup) (chat.Service, error) {
	service := chat.Service{Store: chat.Store{Pool: pool}}
	enabled, err := strconv.ParseBool(env("CHAT_ENABLED", "false"))
	if err != nil {
		return service, errors.New("CHAT_ENABLED must be a boolean")
	}
	if !enabled {
		return service, nil
	}

	budget, err := strconv.ParseInt(env("CHAT_DAILY_TOKEN_BUDGET", "125000"), 10, 64)
	if err != nil || budget <= 0 {
		return service, errors.New("CHAT_DAILY_TOKEN_BUDGET must be a positive integer")
	}
	provider, err := agent.NewChatCompletions(os.Getenv("OPENAI_BASE_URL"), os.Getenv("OPENAI_MODEL"), os.Getenv("OPENAI_API_KEY"))
	if err != nil {
		return service, err
	}

	titleProvider := provider
	titleKey := strings.TrimSpace(os.Getenv("CHAT_TITLE_OPENAI_API_KEY"))
	titleURL := strings.TrimSpace(os.Getenv("CHAT_TITLE_OPENAI_BASE_URL"))
	titleModel := strings.TrimSpace(os.Getenv("CHAT_TITLE_OPENAI_MODEL"))
	if titleKey != "" && titleURL != "" && titleModel != "" {
		titleProvider, err = agent.NewChatCompletions(titleURL, titleModel, titleKey)
		if err != nil {
			return service, errors.New("invalid CHAT_TITLE_OPENAI_BASE_URL or CHAT_TITLE_OPENAI_MODEL")
		}
	}
	tokenBudget := quota.NewTokenBudget(pool, budget)
	service.GenerateTitle = titleProvider.WithTokenBudget(tokenBudget).ChatTitle

	reader := company.Reader{Pool: pool}
	dadata, err := strconv.ParseBool(env("CHAT_DADATA_ENABLED", "false"))
	if err != nil || (dadata && lookup == nil) {
		return service, errors.New("CHAT_DADATA_ENABLED must be a boolean and requires DADATA_ENABLED=true")
	}
	if dadata {
		reader.Lookup = lookup
	}
	var events tools.Events
	events.Support, err = strconv.ParseBool(env("FNS_SUPPORT_AGENT_ENABLED", "false"))
	if err != nil {
		return service, errors.New("FNS_SUPPORT_AGENT_ENABLED must be a boolean")
	}
	events.TaxOffences, err = strconv.ParseBool(env("FNS_TAX_OFFENCES_AGENT_ENABLED", "false"))
	if err != nil {
		return service, errors.New("FNS_TAX_OFFENCES_AGENT_ENABLED must be a boolean")
	}

	service.Run = agent.Run{
		Provider: provider.WithTokenBudget(tokenBudget),
		Tools:    tools.Executor{Reader: reader, Events: &events},
		Steps:    6,
		Bytes:    16000,
	}
	return service, nil
}
