package main

import (
	"sync"
	"testing"
	"time"
)

// fakeSource is a controllable discoverySource for testing the composite.
type fakeSource struct {
	name    string
	onState func(DiscoveryState)
	stopped bool
	mu      sync.Mutex
}

func (f *fakeSource) label() string { return f.name }

func (f *fakeSource) start(_ *peerRegistry, onState func(DiscoveryState)) func() {
	f.mu.Lock()
	f.onState = onState
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.stopped = true
		f.mu.Unlock()
	}
}

func (f *fakeSource) emit(available bool) {
	f.mu.Lock()
	cb := f.onState
	f.mu.Unlock()
	cb(DiscoveryState{Available: available})
}

func TestCompositeDiscoveryAvailabilityIsOr(t *testing.T) {
	a := &fakeSource{name: "a"}
	b := &fakeSource{name: "b"}
	comp := newCompositeDiscovery(a, b)

	var mu sync.Mutex
	var last DiscoveryState
	stop := comp.start(newPeerRegistry("self", nil), func(st DiscoveryState) {
		mu.Lock()
		last = st
		mu.Unlock()
	})
	defer stop()

	get := func() bool { mu.Lock(); defer mu.Unlock(); return last.Available }

	a.emit(false)
	b.emit(false)
	if get() {
		t.Error("both sources unavailable → composite must be unavailable")
	}
	b.emit(true)
	if !get() {
		t.Error("one source available → composite must be available")
	}
	b.emit(false)
	if get() {
		t.Error("last available source went down → composite must be unavailable")
	}
}

func TestCompositeDiscoveryStopsAllSources(t *testing.T) {
	a := &fakeSource{name: "a"}
	b := &fakeSource{name: "b"}
	comp := newCompositeDiscovery(a, b)
	stop := comp.start(newPeerRegistry("self", nil), nil)
	stop()

	a.mu.Lock()
	b.mu.Lock()
	defer a.mu.Unlock()
	defer b.mu.Unlock()
	if !a.stopped || !b.stopped {
		t.Errorf("stop must propagate to every source (a=%v b=%v)", a.stopped, b.stopped)
	}
}

// A non-multicast source feeding registry.observe must surface peers exactly
// like multicast does — the seam that lets Bluetooth contribute peers.
func TestRegistryAcceptsPeersFromAnySource(t *testing.T) {
	var mu sync.Mutex
	var peers []NearbyPeer
	reg := newPeerRegistry("self", func(p []NearbyPeer) {
		mu.Lock()
		peers = p
		mu.Unlock()
	})

	reg.observe(discoveryIdentity{
		ID: "ble-peer", Name: "Brave Otter", Port: 4321,
		Fingerprint: "ab", Addrs: []string{"192.168.137.2"},
	}, "192.168.137.2", time.Now())

	mu.Lock()
	defer mu.Unlock()
	if len(peers) != 1 || peers[0].ID != "ble-peer" || peers[0].Port != 4321 {
		t.Fatalf("peer from a non-multicast source not surfaced: %+v", peers)
	}
}
