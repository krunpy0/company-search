package fns

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDownloadMissingRelease(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	file, err := DownloadZIP(context.Background(), server.URL, 1024, time.Second)
	if file != nil || !errors.Is(err, ErrReleaseMissing) {
		t.Fatalf("missing release: file = %v, error = %v", file, err)
	}
}
