package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestConversation(t *testing.T) {
	var prompt bytes.Buffer
	var questions []string
	resets := 0
	err := conversation(strings.NewReader("\nКомпания\n/new\nДоходы?\n/exit\nignored\n"), &prompt, func(q string) error {
		questions = append(questions, q)
		if q == "Компания" {
			return errors.New("temporary failure")
		}
		return nil
	}, func() { resets++ })
	if err != nil || strings.Join(questions, "|") != "Компания|Доходы?" || resets != 1 || !strings.Contains(prompt.String(), "temporary failure") {
		t.Fatalf("%v %v %d %s", err, questions, resets, prompt.String())
	}
}
