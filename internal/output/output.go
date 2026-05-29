// Package output renders policy results in human-readable, JSON, and SARIF
// formats.
package output

import (
	"time"

	"github.com/sstraus/embargo/internal/ecosystem"
)

// Report is the full result of a check, ready to render in any format.
type Report struct {
	Decisions  []ecosystem.PolicyDecision
	Warnings   []string
	MinimumAge time.Duration
	Now        time.Time
}

// Blocked returns the decisions that were denied.
func (r Report) Blocked() []ecosystem.PolicyDecision {
	var out []ecosystem.PolicyDecision
	for _, d := range r.Decisions {
		if !d.Allowed {
			out = append(out, d)
		}
	}
	return out
}

// Counts returns allowed and blocked totals.
func (r Report) Counts() (allowed, blocked int) {
	for _, d := range r.Decisions {
		if d.Allowed {
			allowed++
		} else {
			blocked++
		}
	}
	return allowed, blocked
}

// HasBlocked reports whether any decision was denied.
func (r Report) HasBlocked() bool {
	_, blocked := r.Counts()
	return blocked > 0
}
