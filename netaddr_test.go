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
