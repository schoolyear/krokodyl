package main

import (
	"sync"

	"github.com/sirupsen/logrus"
)

// A discoverySource feeds peers into a shared peerRegistry and reports its own
// availability. Multicast over the LAN is one source; Bluetooth (offline,
// no-infrastructure pairing) is another. Both end at registry.observe, so the
// transfer path downstream (control channel + croc) never needs to know how a
// peer was found.
type discoverySource interface {
	// label identifies the source in logs.
	label() string
	// start begins feeding registry.observe and reporting availability via
	// onState; the returned func stops it (idempotent).
	start(registry *peerRegistry, onState func(DiscoveryState)) (stop func())
}

// compositeDiscovery runs several sources against one registry and reports the
// channel as available when ANY source is available — discovery is usable if
// the peer can be reached over at least one medium.
type compositeDiscovery struct {
	sources []discoverySource
}

func newCompositeDiscovery(sources ...discoverySource) *compositeDiscovery {
	return &compositeDiscovery{sources: sources}
}

func (c *compositeDiscovery) start(registry *peerRegistry, onState func(DiscoveryState)) func() {
	if onState == nil {
		onState = func(DiscoveryState) {}
	}

	var mu sync.Mutex
	states := make([]bool, len(c.sources))

	emit := func() {
		mu.Lock()
		any := false
		for _, s := range states {
			any = any || s
		}
		mu.Unlock()
		onState(DiscoveryState{Available: any})
	}

	stops := make([]func(), 0, len(c.sources))
	for i, src := range c.sources {
		i := i
		logrus.Debugf("discovery: starting source %q", src.label())
		stops = append(stops, src.start(registry, func(st DiscoveryState) {
			mu.Lock()
			states[i] = st.Available
			mu.Unlock()
			emit()
		}))
	}

	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}

// multicastSource is the LAN discovery channel: announce + listen over UDP
// multicast. It carries the per-run identity and whether to announce
// (invisible mode listens only).
type multicastSource struct {
	identity discoveryIdentity
	announce bool
}

func (m *multicastSource) label() string { return "multicast" }
