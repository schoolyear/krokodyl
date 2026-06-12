package main

import (
	"testing"
	"time"
)

func TestStallTrackerNotArmedWhileWaiting(t *testing.T) {
	var s stallTracker
	base := time.Now()

	// Inactive (waiting for a receiver): never stalls, however long it sits.
	s.observe(0, 0, false, base)
	if s.stalled(base.Add(time.Hour)) {
		t.Error("an unarmed (waiting) transfer must never report stalled")
	}
}

func TestStallTrackerTripsAfterWindow(t *testing.T) {
	var s stallTracker
	base := time.Now()

	s.observe(100, 5, true, base) // arms here
	if s.stalled(base.Add(stallTimeout - time.Second)) {
		t.Error("must not stall before the window elapses")
	}
	if !s.stalled(base.Add(stallTimeout + time.Second)) {
		t.Error("must stall after the window with no movement")
	}
}

func TestStallTrackerResetsOnMovement(t *testing.T) {
	var s stallTracker
	base := time.Now()

	s.observe(100, 5, true, base)
	// Movement just before the window would expire keeps it alive.
	s.observe(200, 6, true, base.Add(stallTimeout-time.Second))
	if s.stalled(base.Add(stallTimeout + time.Second)) {
		t.Error("byte movement must reset the stall timer")
	}
	// ...but a further window with no movement does trip.
	if !s.stalled(base.Add(stallTimeout - time.Second + stallTimeout + time.Second)) {
		t.Error("must stall once movement stops for a full window")
	}
}

func TestStallTrackerProgressOnlyCountsAsMovement(t *testing.T) {
	var s stallTracker
	base := time.Now()

	// Receive side may advance percent without the Sent counter moving.
	s.observe(0, 10, true, base)
	s.observe(0, 40, true, base.Add(stallTimeout-time.Second))
	if s.stalled(base.Add(stallTimeout + time.Second)) {
		t.Error("percent progress must count as movement")
	}
}

func TestStallTrackerSlowButMovingNeverTrips(t *testing.T) {
	var s stallTracker
	base := time.Now()
	s.observe(0, 0, true, base)

	// One byte every 10s for a long time — slow, but always moving.
	sent := int64(0)
	now := base
	for i := 0; i < 100; i++ {
		now = now.Add(10 * time.Second)
		sent += 1024
		s.observe(sent, 0, true, now)
		if s.stalled(now) {
			t.Fatalf("slow-but-moving transfer tripped the watchdog at step %d", i)
		}
	}
}

func TestStallTrackerIsArmed(t *testing.T) {
	var s stallTracker
	base := time.Now()

	if s.isArmed() {
		t.Error("fresh tracker must not be armed")
	}
	s.observe(0, 0, false, base)
	if s.isArmed() {
		t.Error("inactive samples must not arm the tracker")
	}
	s.observe(1, 0, true, base)
	if !s.isArmed() {
		t.Error("first active sample must arm the tracker")
	}
}
