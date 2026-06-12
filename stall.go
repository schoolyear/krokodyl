package main

import (
	"sync"
	"time"
)

// A transfer that loses the network mid-flight otherwise sits frozen at some
// percentage until croc's own socket timeout (minutes, sometimes never).
// stallTracker watches byte/percent movement and reports a stall once an
// active transfer stops moving for stallTimeout. It arms only after the
// transfer is actually moving, so the legitimate "waiting for a receiver"
// phase (handled by the watchdog's connect grace) never looks stalled. Any
// real forward movement resets it, so slow-but-moving links are never failed.
const (
	stallTimeout       = 30 * time.Second
	stallCheckInterval = 5 * time.Second
)

type stallTracker struct {
	mu           sync.Mutex
	armed        bool
	lastSent     int64
	lastProgress int
	lastMovement time.Time
}

// observe records one progress sample. active is true once the transfer is
// sending/receiving. Movement is any increase in bytes sent or percent done;
// counters only ever go up within a transfer, so non-increases are ignored
// (and a spurious reset can't fake progress).
func (s *stallTracker) observe(sent int64, progress int, active bool, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !active {
		return
	}
	if !s.armed {
		s.armed = true
		s.lastSent = sent
		s.lastProgress = progress
		s.lastMovement = now
		return
	}
	if sent > s.lastSent || progress > s.lastProgress {
		s.lastSent = sent
		s.lastProgress = progress
		s.lastMovement = now
	}
}

// stalled reports whether an armed transfer has had no movement for longer
// than stallTimeout. An unarmed tracker (still waiting, never moved) never
// stalls.
func (s *stallTracker) stalled(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.armed && now.Sub(s.lastMovement) > stallTimeout
}

// isArmed reports whether the transfer has started moving. Before that, the
// watchdog uses a connect grace instead of the stall timeout.
func (s *stallTracker) isArmed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.armed
}
