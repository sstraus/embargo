package output

import (
	"fmt"
	"io"
	"time"

	"github.com/sstraus/embargo/internal/policy"
)

// Human writes a readable report: a detailed block per blocked dependency,
// then warnings and a summary line.
func Human(w io.Writer, r Report) error {
	for _, d := range r.Blocked() {
		fmt.Fprintln(w, "BLOCKED")
		fmt.Fprintf(w, "ecosystem: %s\n", d.Dependency.Ecosystem)
		fmt.Fprintf(w, "package: %s\n", d.Dependency.Name)
		fmt.Fprintf(w, "version: %s\n", d.Dependency.Version)
		if !d.Metadata.PublishedAt.IsZero() {
			fmt.Fprintf(w, "published: %s\n", d.Metadata.PublishedAt.UTC().Format(time.RFC3339))
			fmt.Fprintf(w, "age: %s\n", policy.HumanDuration(r.Now.Sub(d.Metadata.PublishedAt)))
		}
		fmt.Fprintf(w, "required minimum: %s\n", policy.HumanDuration(r.MinimumAge))
		if d.Dependency.Source != "" {
			fmt.Fprintf(w, "source: %s\n", d.Dependency.Source)
		}
		fmt.Fprintf(w, "reason: %s\n", d.Reason)
		fmt.Fprintln(w)
	}

	for _, warn := range r.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warn)
	}

	allowed, blocked := r.Counts()
	if blocked == 0 {
		fmt.Fprintf(w, "OK: %d dependencies checked, all allowed\n", allowed)
	} else {
		fmt.Fprintf(w, "FAILED: %d blocked, %d allowed (%d checked)\n", blocked, allowed, allowed+blocked)
	}
	return nil
}
