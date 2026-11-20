package main

import (
	"testing"
	"time"
)

func TestExpectedRelease(t *testing.T) {
	for _, tc := range []struct {
		dataset, now, want string
	}{
		{"rsmp", "2026-11-09T20:59:00Z", "2026-10-10"},
		{"rsmp", "2026-11-09T21:00:00Z", "2026-11-10"},
		{"revexp", "2027-01-09T21:00:00Z", "2026-12-25"},
		{"revexp", "2026-11-24T21:00:00Z", "2026-11-25"},
		{"sshr2019", "2026-11-24T21:00:00Z", "2026-11-25"},
		{"snr", "2026-11-24T21:00:00Z", "2026-11-25"},
		{"snrip", "2026-11-24T21:00:00Z", "2026-11-25"},
		{"debtam", "2026-11-24T21:00:00Z", "2026-11-25"},
		{"paytax", "2027-03-31T20:59:00Z", "2026-04-01"},
		{"paytax", "2027-03-31T21:00:00Z", "2027-04-01"},
	} {
		t.Run(tc.dataset+"/"+tc.now, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			if got := expectedRelease(tc.dataset, now).Format(time.DateOnly); got != tc.want {
				t.Fatalf("expected release = %s, want %s", got, tc.want)
			}
		})
	}
}
