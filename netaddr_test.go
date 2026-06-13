package main

import (
	"net"
	"testing"
)

func TestLocalUnicastIPsExcludesLoopbackAndLinkLocal(t *testing.T) {
	for _, ip := range localUnicastIPs() {
		parsed := net.ParseIP(ip)
		if parsed == nil {
			t.Errorf("non-IP returned: %q", ip)
		}
		if parsed.IsLoopback() {
			t.Errorf("loopback address returned: %q", ip)
		}
		if parsed.IsLinkLocalUnicast() {
			t.Errorf("link-local address returned: %q", ip)
		}
		if parsed.To4() == nil {
			t.Errorf("non-IPv4 address returned: %q", ip)
		}
	}
}

func TestOrderedCandidatesPrioritizesRealLAN(t *testing.T) {
	// Hyper-V-style virtual addr advertised before the real LAN addr; the
	// real LAN one must be tried first.
	got := orderedCandidates([]string{"172.18.80.1", "192.168.1.50"}, "")
	if got[0] != "192.168.1.50" {
		t.Errorf("expected real-LAN address first, got %v", got)
	}
}

func TestOrderedCandidatesDedupAndSourceFallback(t *testing.T) {
	got := orderedCandidates([]string{"192.168.1.50", "10.0.0.2"}, "192.168.1.50")
	// packet source equals an advertised addr → no duplicate.
	if len(got) != 2 {
		t.Fatalf("expected 2 unique candidates, got %v", got)
	}
	seen := map[string]bool{}
	for _, ip := range got {
		if seen[ip] {
			t.Errorf("duplicate candidate %q in %v", ip, got)
		}
		seen[ip] = true
	}
}

func TestOrderedCandidatesAppendsNovelSource(t *testing.T) {
	got := orderedCandidates([]string{"192.168.1.50"}, "10.9.9.9")
	if len(got) != 2 || got[len(got)-1] != "10.9.9.9" {
		t.Errorf("packet source should be appended as fallback, got %v", got)
	}
}

func TestOrderedCandidatesEmpty(t *testing.T) {
	if got := orderedCandidates(nil, ""); len(got) != 0 {
		t.Errorf("expected no candidates, got %v", got)
	}
}

func TestMoveToFront(t *testing.T) {
	// Present: target jumps to front, order of the rest preserved, no dup.
	got := moveToFront([]string{"10.0.0.1", "172.20.10.2", "172.20.128.1"}, "172.20.10.2")
	want := []string{"172.20.10.2", "10.0.0.1", "172.20.128.1"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("moveToFront = %v, want %v", got, want)
		}
	}

	// Absent: target is prepended.
	got = moveToFront([]string{"192.168.1.5"}, "172.20.10.2")
	if len(got) != 2 || got[0] != "172.20.10.2" {
		t.Errorf("absent target should be prepended, got %v", got)
	}
}
