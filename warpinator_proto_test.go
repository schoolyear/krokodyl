package main

import "testing"

func TestWarpServiceTXT(t *testing.T) {
	txt := warpServiceTXT("krokodyl-host")
	if txt["api-version"] != warpAPIVersion {
		t.Errorf("api-version = %q, want %q", txt["api-version"], warpAPIVersion)
	}
	if txt["hostname"] != "krokodyl-host" {
		t.Errorf("hostname not set: %v", txt)
	}
	if txt["type"] != "real" {
		t.Errorf("type should be 'real' for a live service, got %q", txt["type"])
	}
}

func TestWarpOfferSummary(t *testing.T) {
	files, _ := warpOfferSummary(warpTransferRequest{Count: 1, NameIfSingle: "photo.jpg", Size: 2048})
	if len(files) != 1 || files[0] != "photo.jpg" {
		t.Errorf("single-file summary wrong: %v", files)
	}

	multi, msize := warpOfferSummary(warpTransferRequest{Count: 3, TopDirs: []string{"a", "b"}, Size: 9000})
	if len(multi) != 2 || msize != 9000 {
		t.Errorf("multi summary wrong: %v size=%d", multi, msize)
	}

	// Hostile control chars in a single name must be sanitized for the prompt.
	dirty, _ := warpOfferSummary(warpTransferRequest{Count: 1, NameIfSingle: "evil\r\nname"})
	if dirty[0] != "evilname" {
		t.Errorf("name not sanitized: %q", dirty[0])
	}

	// Empty → placeholder, never an empty list.
	empty, _ := warpOfferSummary(warpTransferRequest{Count: 0})
	if len(empty) == 0 {
		t.Error("summary must never be empty")
	}
}

func TestWarpOurInfo(t *testing.T) {
	if warpOurInfo("").DisplayName != "krokodyl" {
		t.Error("blank name should fall back to krokodyl")
	}
	if warpOurInfo("Brave Otter").DisplayName != "Brave Otter" {
		t.Error("name not carried")
	}
}
