package main

import (
	"errors"
	"log/slog"
	"net"
	"testing"
)

func TestBuildLoggerValid(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"DEBUG", slog.LevelDebug}, // case-insensitive
	}
	for _, tc := range cases {
		got, err := buildLogger(tc.in)
		if err != nil {
			t.Errorf("buildLogger(%q): unexpected error %v", tc.in, err)
			continue
		}
		if !got.Enabled(nil, tc.want) {
			t.Errorf("buildLogger(%q) level mismatch: %v not enabled", tc.in, tc.want)
		}
	}
}

func TestParseCIDRsEmpty(t *testing.T) {
	nets, err := parseCIDRs("")
	if err != nil || nets != nil {
		t.Errorf("empty string: nets=%v err=%v", nets, err)
	}
}

func TestParseCIDRsSingle(t *testing.T) {
	nets, err := parseCIDRs("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 1 {
		t.Fatalf("got %d nets, want 1", len(nets))
	}
	if !nets[0].Contains(net.ParseIP("10.1.2.3")) {
		t.Error("10.0.0.0/8 should contain 10.1.2.3")
	}
}

func TestParseCIDRsMulti(t *testing.T) {
	nets, err := parseCIDRs("10.0.0.0/8, 172.16.0.0/12")
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 2 {
		t.Fatalf("got %d nets, want 2", len(nets))
	}
}

func TestParseCIDRsBadInput(t *testing.T) {
	_, err := parseCIDRs("not-a-cidr")
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
	var fe flagError
	if !errors.As(err, &fe) {
		t.Errorf("error %v should be flagError", err)
	}
}

// Unknown values must produce a flag error, not silently fall back to info,
// otherwise typos like "--log-level wran" mask the intended verbosity.
func TestBuildLoggerRejectsUnknown(t *testing.T) {
	for _, in := range []string{"", "wran", "verbose", "trace", "off"} {
		_, err := buildLogger(in)
		if err == nil {
			t.Errorf("buildLogger(%q): expected error, got nil", in)
			continue
		}
		var fe flagError
		if !errors.As(err, &fe) {
			t.Errorf("buildLogger(%q): error %v is not a flagError", in, err)
		}
	}
}
