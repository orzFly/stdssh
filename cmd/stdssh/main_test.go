package main

import (
	"errors"
	"log/slog"
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
