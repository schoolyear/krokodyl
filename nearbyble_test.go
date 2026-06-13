package main

import (
	"errors"
	"testing"
)

func TestNoopBLEUnavailable(t *testing.T) {
	r := noopBLE{}
	if r.available() {
		t.Error("noop radio must report unavailable")
	}
	if _, err := r.host(nil, bleHandshake{}); !errors.Is(err, errBLEUnavailable) {
		t.Errorf("host should return errBLEUnavailable, got %v", err)
	}
	if _, err := r.join(nil, bleHandshake{}); !errors.Is(err, errBLEUnavailable) {
		t.Errorf("join should return errBLEUnavailable, got %v", err)
	}
	r.close() // must not panic
}

// The default (shipped) build must not compile in Bluetooth: the feature
// degrades to guided manual hotspot pairing.
func TestDefaultBuildHasNoBLE(t *testing.T) {
	if bleBuildEnabled {
		t.Skip("running under -tags krokodyl_ble; radio path is compiled in")
	}
	if newBLERadio().available() {
		t.Error("default build must not have an available radio")
	}
}
