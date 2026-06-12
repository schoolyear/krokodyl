package main

import (
	"regexp"
	"testing"
)

var deviceNamePattern = regexp.MustCompile(`^[A-Z][a-z]+ [A-Z][a-z]+$`)

func TestRandomDeviceNameFormat(t *testing.T) {
	for i := 0; i < 100; i++ {
		name := randomDeviceName()
		if !deviceNamePattern.MatchString(name) {
			t.Fatalf("name %q does not match Adjective Animal format", name)
		}
	}
}

func TestRandomDeviceNameVaries(t *testing.T) {
	// With 32x32 = 1024 combinations, 50 draws should yield several distinct
	// names; identical-every-time would mean the RNG pick is broken.
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		seen[randomDeviceName()] = true
	}
	if len(seen) < 5 {
		t.Errorf("expected varied names, got only %d distinct in 50 draws", len(seen))
	}
}

func TestSanitizeDisplayName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain name unchanged", "Brave Otter", "Brave Otter"},
		{"unicode kept", "Mädchen 龍", "Mädchen 龍"},
		{"newline stripped (log forging)", "evil\n2026-01-01 FAKE line", "evil2026-01-01 FAKE line"},
		{"carriage return stripped", "a\rb", "ab"},
		{"ANSI escape stripped", "\x1b[31mred\x1b[0m", "[31mred[0m"},
		{"DEL stripped", "a\x7fb", "ab"},
		{"RLO stripped (BiDi spoof)", "abc‮xyz", "abcxyz"},
		{"isolates stripped", "⁦hidden⁩", "hidden"},
		{"LRM RLM ALM stripped", "a‎b‏c؜d", "abcd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeDisplayName(tt.in); got != tt.want {
				t.Errorf("sanitizeDisplayName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
