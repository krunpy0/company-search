package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShutdownDrainsActiveRequest(t *testing.T) {
	started, draining, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-finish
		io.WriteString(w, "answer")
	}))
	defer server.Close()
	server.Config.RegisterOnShutdown(func() { close(draining) })
	response := make(chan string, 1)
	go func() {
		resp, err := server.Client().Get(server.URL)
		if err != nil {
			response <- err.Error()
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		response <- string(body)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(finish)
		t.Fatal("request did not reach the handler")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- shutdown(server.Config) }()
	select {
	case <-draining:
		close(finish)
	case <-time.After(5 * time.Second):
		close(finish)
		t.Fatal("shutdown did not start")
	}
	if got := <-response; got != "answer" {
		t.Fatalf("active request got %q", got)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}
