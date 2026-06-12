package main

import (
	"testing"
	"time"
)

func TestRecoveryBudgetGivesUpAfterNoProgress(t *testing.T) {
	b := newRecoveryBudget()
	// First attempt reaching 30% is genuine progress (0 -> 30).
	if b.record(30) {
		t.Fatal("first progress must not give up")
	}
	// Then it stalls at the same 30% — no forward movement.
	for i := 0; i < maxNoProgressAttempts; i++ {
		if b.record(30) {
			t.Fatalf("gave up too early at no-progress attempt %d", i)
		}
	}
	if !b.record(30) {
		t.Error("should give up after exceeding the no-progress budget")
	}
}

func TestRecoveryBudgetResetsOnProgress(t *testing.T) {
	b := newRecoveryBudget()
	// Climbing progress across many attempts must never give up, even past
	// the no-progress cap count.
	for pct := 10; pct <= 90; pct += 10 {
		if b.record(pct) {
			t.Fatalf("gave up while still making progress at %d%%", pct)
		}
	}
	if b.bestPct != 90 {
		t.Errorf("best progress not tracked: %d", b.bestPct)
	}
}

func TestRecoveryBudgetMixedProgressThenStall(t *testing.T) {
	b := newRecoveryBudget()
	b.record(20) // progress
	b.record(50) // progress
	// Now it stalls at 50 repeatedly.
	giveUp := false
	for i := 0; i <= maxNoProgressAttempts; i++ {
		giveUp = b.record(50)
	}
	if !giveUp {
		t.Error("should give up after the budget of no-progress attempts following real progress")
	}
}

func TestOverallProgressOffsetsAndCaps(t *testing.T) {
	// First attempt: no base, shows the session percent directly.
	if got := overallProgress(0, 40); got != 40 {
		t.Errorf("base 0 + 40 = %d, want 40", got)
	}
	// Resume: 85% already done, session adds 10 of the remainder -> 95.
	if got := overallProgress(85, 10); got != 95 {
		t.Errorf("base 85 + 10 = %d, want 95", got)
	}
	// Never shows 100 mid-flight; completion sets that separately.
	if got := overallProgress(85, 30); got != 99 {
		t.Errorf("overshoot should cap at 99, got %d", got)
	}
}

func TestRecoveryBackoffIncreasesAndCaps(t *testing.T) {
	prev := time.Duration(0)
	for attempt := 0; attempt < 10; attempt++ {
		d := recoveryBackoff(attempt)
		if d < prev {
			t.Errorf("backoff decreased at attempt %d: %v < %v", attempt, d, prev)
		}
		if d > recoveryBackoffMax {
			t.Errorf("backoff exceeded cap at attempt %d: %v", attempt, d)
		}
		prev = d
	}
	if recoveryBackoff(0) != recoveryBackoffBase {
		t.Errorf("first backoff should be the base, got %v", recoveryBackoff(0))
	}
	if recoveryBackoff(100) != recoveryBackoffMax {
		t.Errorf("large attempt should cap at max, got %v", recoveryBackoff(100))
	}
}
