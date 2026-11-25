package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/krunpy0/company-search/backend/internal/chat"
	"github.com/krunpy0/company-search/backend/internal/quota"
)

var chatRegistration = regexp.MustCompile(`^(reg:)?([0-9]{13}|[0-9]{15})$`)

func RegisterChat(r gin.IRouter, service chat.Service) {
	group := r.Group("/v1/chat/dialogues", func(c *gin.Context) {
		if _, ok := quota.UserID(c.Request.Context()); !ok {
			rejectSession(c)
		}
	})
	group.POST("", func(c *gin.Context) {
		var input struct {
			Registration string `json:"registration"`
		}
		if !chatJSON(c, &input) {
			return
		}
		var registration *string
		if input.Registration != "" {
			if !chatRegistration.MatchString(input.Registration) {
				fail(c, 400, "invalid_registration", "Provide an OGRN or OGRNIP")
				return
			}
			selected := "reg:" + strings.TrimPrefix(input.Registration, "reg:")
			registration = &selected
		}
		user, _ := quota.UserID(c.Request.Context())
		d, err := service.Create(c.Request.Context(), user, registration)
		if err != nil {
			chatFailure(c, err)
			return
		}
		c.JSON(201, d)
	})
	group.GET("", func(c *gin.Context) {
		before, limit, ok := chatPage(c)
		if !ok {
			return
		}
		user, _ := quota.UserID(c.Request.Context())
		items, err := service.List(c.Request.Context(), user, before, limit+1)
		if err != nil {
			chatFailure(c, err)
			return
		}
		response := gin.H{}
		if len(items) > limit {
			items = items[:limit]
			response["next_before_id"] = strconv.FormatInt(items[limit-1].ID, 10)
		}
		response["dialogues"] = items
		c.JSON(200, response)
	})
	group.GET("/:id/messages", func(c *gin.Context) {
		id, ok := chatID(c)
		if !ok {
			return
		}
		before, limit, ok := chatPage(c)
		if !ok {
			return
		}
		user, _ := quota.UserID(c.Request.Context())
		dialogue, items, err := service.History(c.Request.Context(), user, id, before, limit+1)
		if err != nil {
			chatFailure(c, err)
			return
		}
		response := gin.H{"dialogue": dialogue}
		if len(items) > limit {
			items = items[:limit]
			response["next_before_id"] = strconv.FormatInt(items[limit-1].ID, 10)
		}
		response["messages"] = items
		c.JSON(200, response)
	})
	slots := make(chan struct{}, 8)
	group.POST("/:id/messages", func(c *gin.Context) {
		id, ok := chatID(c)
		if !ok {
			return
		}
		var input struct {
			Content string `json:"content"`
		}
		if !chatJSON(c, &input) {
			return
		}
		input.Content = strings.TrimSpace(input.Content)
		if input.Content == "" || utf8.RuneCountInString(input.Content) > 2000 || strings.ContainsRune(input.Content, 0) {
			fail(c, 400, "invalid_message", "Provide a message of 1 to 2000 characters")
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			fail(c, 429, "busy", "Too many concurrent chat requests")
			return
		}
		user, _ := quota.UserID(c.Request.Context())
		turn, err := service.Send(c.Request.Context(), user, id, input.Content)
		if err != nil {
			chatFailure(c, err)
			return
		}
		c.JSON(201, turn)
	})
}

func chatJSON(c *gin.Context, input any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	raw, err := io.ReadAll(c.Request.Body)
	raw = bytes.TrimSpace(raw)
	if err == nil && len(raw) > 0 && raw[0] == '{' && utf8.Valid(raw) && json.Unmarshal(raw, input) == nil {
		return true
	}
	fail(c, 400, "invalid_json", "Provide a valid JSON object")
	return false
}

func chatID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err == nil && id > 0 {
		return id, true
	}
	fail(c, 400, "invalid_dialogue_id", "Provide a positive dialogue ID")
	return 0, false
}

func chatPage(c *gin.Context) (int64, int, bool) {
	before, err := strconv.ParseInt(c.DefaultQuery("before_id", "0"), 10, 64)
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err == nil && before >= 0 && limitErr == nil && limit >= 1 && limit <= 50 {
		return before, limit, true
	}
	fail(c, 400, "invalid_pagination", "Provide a valid cursor and a limit from 1 to 50")
	return 0, 0, false
}

func chatFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, chat.ErrNotFound):
		fail(c, 404, "dialogue_not_found", "Dialogue not found")
	case errors.Is(err, chat.ErrInProgress):
		fail(c, 409, "turn_in_progress", "A message is already being processed in this dialogue")
	case errors.Is(err, chat.ErrSelection):
		fail(c, 400, "registration_selection_required", "Choose an OGRN or OGRNIP from the dialogue candidates")
	case errors.Is(err, chat.ErrMessageBudget):
		fail(c, 429, "message_budget_exhausted", "Daily message budget exhausted")
	case errors.Is(err, quota.ErrTokenBudget):
		fail(c, 429, "token_budget_exhausted", "Daily model token budget exhausted")
	case errors.Is(err, chat.ErrModelUnavailable):
		fail(c, 503, "model_unavailable", "Model temporarily unavailable")
	default:
		fail(c, 503, "chat_unavailable", "Chat storage temporarily unavailable")
	}
}
