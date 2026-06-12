package main

import "time"

// On a flaky link a transfer drops repeatedly; rather than failing, krokodyl
// re-runs the worker with the same code (the receiver resumes from its
// preserved partial) and keeps doing so as long as attempts make forward
// progress. recoveryBudget encodes the give-up policy: stop only after a
// bounded number of attempts that gained no new ground, so a link that drops
// every 30s but advances each time still completes, while a dead link stops
// cleanly.
const (
	maxNoProgressAttempts = 5
	recoveryBackoffBase   = 2 * time.Second
	recoveryBackoffMax    = 10 * time.Second

	// connectGraceInitial allows a human to share/enter a code on the first
	// attempt; connectGraceRetry is shorter because a reconnecting peer
	// should rejoin the relay room quickly.
	connectGraceInitial = 5 * time.Minute
	connectGraceRetry   = 45 * time.Second
)

// connectGrace returns the no-movement grace for an attempt number.
func connectGrace(attempt int) time.Duration {
	if attempt == 0 {
		return connectGraceInitial
	}
	return connectGraceRetry
}

type recoveryBudget struct {
	bestPct       int
	noProgress    int
	maxNoProgress int
}

func newRecoveryBudget() *recoveryBudget {
	return &recoveryBudget{maxNoProgress: maxNoProgressAttempts}
}

// record folds in a finished attempt's peak progress and reports whether to
// give up. An attempt that beat the best progress so far resets the
// no-progress counter; otherwise it counts against the budget.
func (b *recoveryBudget) record(attemptPeakPct int) (giveUp bool) {
	if attemptPeakPct > b.bestPct {
		b.bestPct = attemptPeakPct
		b.noProgress = 0
		return false
	}
	b.noProgress++
	return b.noProgress >= b.maxNoProgress
}

// overallProgress maps a worker's per-session percent into the band above the
// progress already achieved, capped at 99 (only completion shows 100). Lives
// here with the rest of the recovery math.
func overallProgress(basePct, sessionPct int) int {
	display := basePct + sessionPct
	if display > 99 {
		return 99
	}
	return display
}

// recoveryBackoffFn is the backoff the retry loop actually sleeps; a var so
// tests can collapse the waits to zero.
var recoveryBackoffFn = recoveryBackoff

// recoveryBackoff grows the wait between attempts and caps it, so a hard-down
// link is retried gently rather than in a tight loop.
func recoveryBackoff(attempt int) time.Duration {
	d := recoveryBackoffBase
	for i := 0; i < attempt; i++ {
		d *= 2
		if d >= recoveryBackoffMax {
			return recoveryBackoffMax
		}
	}
	if d > recoveryBackoffMax {
		return recoveryBackoffMax
	}
	return d
}
