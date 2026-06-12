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
