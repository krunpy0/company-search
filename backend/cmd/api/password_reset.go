package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/mail"
	"net/url"

	"github.com/krunpy0/company-search/backend/internal/httpapi"
)

func configurePasswordReset(getenv func(string) string) (httpapi.PasswordResetSender, error) {
	key, from := getenv("RESEND_API_KEY"), getenv("PASSWORD_RESET_FROM")
	if key == "" && from == "" {
		return nil, nil
	}
	origin, err := url.Parse(getenv("COMPANY_APP_ORIGIN"))
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil ||
		(origin.Path != "" && origin.Path != "/") || origin.ForceQuery || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("password reset requires an HTTPS COMPANY_APP_ORIGIN")
	}
	if _, err := mail.ParseAddress(from); key == "" || err != nil {
		return nil, errors.New("password reset requires RESEND_API_KEY and a valid PASSWORD_RESET_FROM")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return func(ctx context.Context, email, token string) error {
		link := origin.JoinPath("/reset-password")
		link.Fragment = "token=" + token
		message := "Ссылка действует 30 минут. Если вы не запрашивали смену пароля, проигнорируйте это письмо."
		payload, err := json.Marshal(map[string]string{
			"from": from, "to": email, "subject": "Сброс пароля Saruka",
			"text": "Установить новый пароль: " + link.String() + "\n\n" + message,
			"html": `<p><a href="` + html.EscapeString(link.String()) + `">Установить новый пароль</a></p><p>` + message + `</p>`,
		})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(payload))
		if err != nil {
			return errors.New("could not build the email request")
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return errors.New("could not reach the email provider")
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("email provider returned HTTP %d", resp.StatusCode)
		}
		return nil
	}, nil
}
