package main

import (
	"strings"
	"testing"
	"time"
)

const testFingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validIdentityJSON(extra string) string {
	return `{"id":"abc","name":"laptop","port":4242,"fingerprint":"` + testFingerprint + `"` + extra + `}`
}

func TestDecodeIdentity(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{"valid", validIdentityJSON(""), false},
		{"missing id", `{"name":"laptop","port":4242,"fingerprint":"` + testFingerprint + `"}`, true},
		{"missing port", `{"id":"abc","name":"laptop","fingerprint":"` + testFingerprint + `"}`, true},
		{"port out of range", `{"id":"abc","name":"laptop","port":70000,"fingerprint":"` + testFingerprint + `"}`, true},
		{"missing fingerprint", `{"id":"abc","name":"laptop","port":4242}`, true},
		{"short fingerprint", `{"id":"abc","name":"laptop","port":4242,"fingerprint":"abcd"}`, true},
		{"empty payload", ``, true},
		{"garbage", `not json`, true},
		{"oversized", `{"id":"a","port":1,"fingerprint":"` + testFingerprint + `","name":"` + strings.Repeat("x", 600) + `"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeIdentity([]byte(tt.payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("decodeIdentity(%q) error = %v, wantErr %v", tt.payload, err, tt.wantErr)
			}
		})
	}
}

func TestDecodeIdentityClampsLongName(t *testing.T) {
	payload := `{"id":"abc","port":4242,"fingerprint":"` + testFingerprint + `","name":"` + strings.Repeat("n", 100) + `"}`
	id, err := decodeIdentity([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(id.Name) != maxPeerNameLen {
		t.Errorf("name not clamped: %d chars", len(id.Name))
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	in := discoveryIdentity{ID: "uuid-1", Name: "dev-machine", Port: 4242, Fingerprint: testFingerprint}
	out, err := decodeIdentity(encodeIdentity(in))
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("round trip mismatch: %+v != %+v", out, in)
	}
}

func TestRegistryObserveAddsAndRefreshes(t *testing.T) {
	changes := 0
	r := newPeerRegistry("self", func([]NearbyPeer) { changes++ })
	now := time.Now()

	r.observe(discoveryIdentity{ID: "p1", Name: "alpha"}, "10.0.0.2", now)
	if changes != 1 {
		t.Fatalf("expected 1 change after new peer, got %d", changes)
	}

	// Same peer, same name: refresh only, no event.
	r.observe(discoveryIdentity{ID: "p1", Name: "alpha"}, "10.0.0.2", now.Add(time.Second))
	if changes != 1 {
		t.Errorf("refresh must not emit a change, got %d", changes)
	}

	// Name change emits.
	r.observe(discoveryIdentity{ID: "p1", Name: "renamed"}, "10.0.0.2", now.Add(2*time.Second))
	if changes != 2 {
		t.Errorf("rename should emit, got %d changes", changes)
	}
}

func TestRegistryFiltersSelfButTracksHealth(t *testing.T) {
	r := newPeerRegistry("self", func(peers []NearbyPeer) {
		t.Errorf("self announcement must not emit peers: %v", peers)
	})
	now := time.Now()

	r.observe(discoveryIdentity{ID: "self", Name: "me"}, "10.0.0.1", now)
	if len(r.snapshot()) != 0 {
		t.Error("self must not appear in snapshot")
	}
	if !r.selfHealthy(now) {
		t.Error("self sighting must mark discovery healthy")
	}
	if r.selfHealthy(now.Add(peerTTL + time.Second)) {
		t.Error("stale self sighting must mark unhealthy")
	}
}

func TestRegistrySweepExpiresPeers(t *testing.T) {
	var last []NearbyPeer
	r := newPeerRegistry("self", func(peers []NearbyPeer) { last = peers })
	now := time.Now()

	r.observe(discoveryIdentity{ID: "p1", Name: "alpha"}, "10.0.0.2", now)
	r.observe(discoveryIdentity{ID: "p2", Name: "beta"}, "10.0.0.3", now.Add(3*time.Second))

	// p1 beyond TTL, p2 still fresh.
	r.sweep(now.Add(peerTTL + time.Second))

	if len(last) != 1 || last[0].ID != "p2" {
		t.Errorf("expected only p2 to survive sweep, got %v", last)
	}

	// Sweep with nothing to expire: no event.
	before := len(last)
	_ = before
	last = nil
	r.sweep(now.Add(peerTTL + 2*time.Second))
	if last != nil {
		t.Errorf("no-op sweep must not emit, got %v", last)
	}
}

func TestDecodeIdentityByeNeedsOnlyID(t *testing.T) {
	if _, err := decodeIdentity([]byte(`{"id":"abc","bye":true}`)); err != nil {
		t.Errorf("bye payload with only id must be valid: %v", err)
	}
	if _, err := decodeIdentity([]byte(`{"bye":true}`)); err == nil {
		t.Error("bye payload without id must be rejected")
	}
}

func TestRegistryByeRemovesPeerImmediately(t *testing.T) {
	var last []NearbyPeer
	emitted := 0
	r := newPeerRegistry("self", func(peers []NearbyPeer) { last = peers; emitted++ })
	now := time.Now()

	r.observe(discoveryIdentity{ID: "p1", Name: "alpha", Port: 1, Fingerprint: testFingerprint}, "10.0.0.2", now)
	if len(last) != 1 {
		t.Fatalf("peer should be present, got %v", last)
	}

	r.observe(discoveryIdentity{ID: "p1", Bye: true}, "10.0.0.2", now.Add(time.Second))
	if len(last) != 0 {
		t.Errorf("bye must remove the peer immediately, got %v", last)
	}

	// Bye for an unknown peer: no event.
	before := emitted
	r.observe(discoveryIdentity{ID: "ghost", Bye: true}, "10.0.0.9", now.Add(2*time.Second))
	if emitted != before {
		t.Error("bye for unknown peer must not emit")
	}
}

func TestRegistryByeSuppressesStraggler(t *testing.T) {
	var last []NearbyPeer
	r := newPeerRegistry("self", func(peers []NearbyPeer) { last = peers })
	now := time.Now()
	id := discoveryIdentity{ID: "p1", Name: "alpha", Port: 1, Fingerprint: testFingerprint}

	r.observe(id, "10.0.0.2", now)
	if len(last) != 1 {
		t.Fatalf("peer should be present, got %v", last)
	}

	// Goodbye removes it...
	r.observe(discoveryIdentity{ID: "p1", Bye: true}, "10.0.0.2", now.Add(time.Second))
	if len(last) != 0 {
		t.Fatalf("bye should remove the peer, got %v", last)
	}

	// ...a straggler normal announce just after must NOT bring it back.
	last = []NearbyPeer{{ID: "sentinel"}}
	r.observe(id, "10.0.0.2", now.Add(time.Second+500*time.Millisecond))
	if len(r.snapshot()) != 0 {
		t.Errorf("straggler announce resurrected a departed peer: %v", r.snapshot())
	}

	// After the suppression window, the peer may legitimately return.
	r.observe(id, "10.0.0.2", now.Add(time.Second+byeSuppression+time.Second))
	if len(r.snapshot()) != 1 {
		t.Errorf("peer should be allowed back after the suppression window, got %v", r.snapshot())
	}
}

func TestRegistryHigherGenBypassesSuppression(t *testing.T) {
	r := newPeerRegistry("self", nil)
	now := time.Now()
	base := discoveryIdentity{ID: "p1", Name: "alpha", Port: 1, Fingerprint: testFingerprint, Gen: 1}

	r.observe(base, "10.0.0.2", now)
	// Goodbye for generation 1.
	r.observe(discoveryIdentity{ID: "p1", Gen: 1, Bye: true}, "10.0.0.2", now.Add(time.Second))
	if len(r.snapshot()) != 0 {
		t.Fatalf("bye should remove peer, got %v", r.snapshot())
	}

	// An intentional unhide announces a higher generation — must reappear
	// immediately, even though we're still inside the suppression window.
	returning := base
	returning.Gen = 2
	r.observe(returning, "10.0.0.2", now.Add(time.Second+200*time.Millisecond))
	if len(r.snapshot()) != 1 {
		t.Errorf("higher-generation announce must bypass suppression, got %v", r.snapshot())
	}
}

func TestSweepPrunesByeWindow(t *testing.T) {
	r := newPeerRegistry("self", nil)
	now := time.Now()
	r.observe(discoveryIdentity{ID: "p1", Bye: true}, "10.0.0.2", now)

	r.mu.Lock()
	_, present := r.byeUntil["p1"]
	r.mu.Unlock()
	if !present {
		t.Fatal("bye window not recorded")
	}

	r.sweep(now.Add(byeSuppression + time.Second))
	r.mu.Lock()
	_, stillThere := r.byeUntil["p1"]
	r.mu.Unlock()
	if stillThere {
		t.Error("expired bye window should be pruned by sweep")
	}
}

func TestRegistryByeThenLegitReturnCarriesMachineID(t *testing.T) {
	r := newPeerRegistry("self", nil)
	now := time.Now()
	id := discoveryIdentity{ID: "p1", Name: "alpha", Port: 1, Fingerprint: testFingerprint, MachineID: "machine-xyz"}

	r.observe(id, "10.0.0.2", now)
	peers := r.snapshot()
	if len(peers) != 1 || peers[0].MachineID != "machine-xyz" {
		t.Errorf("machine id not carried into registry: %+v", peers)
	}
}

func TestRegistrySnapshotSorted(t *testing.T) {
	r := newPeerRegistry("self", nil)
	now := time.Now()
	r.observe(discoveryIdentity{ID: "1", Name: "zulu"}, "a", now)
	r.observe(discoveryIdentity{ID: "2", Name: "alpha"}, "b", now)
	r.observe(discoveryIdentity{ID: "3", Name: "mike"}, "c", now)

	snap := r.snapshot()
	if snap[0].Name != "alpha" || snap[1].Name != "mike" || snap[2].Name != "zulu" {
		t.Errorf("snapshot not sorted by name: %v", snap)
	}
}
