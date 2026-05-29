package output

import (
	"encoding/json"
	"io"
	"time"
)

type jsonDecision struct {
	Ecosystem string `json:"ecosystem"`
	Package   string `json:"package"`
	Version   string `json:"version"`
	Source    string `json:"source,omitempty"`
	Direct    bool   `json:"direct"`
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason"`
	Published string `json:"published,omitempty"`
}

type jsonReport struct {
	MinimumReleaseAge string         `json:"minimumReleaseAge"`
	Summary           jsonSummary    `json:"summary"`
	Decisions         []jsonDecision `json:"decisions"`
	Warnings          []string       `json:"warnings,omitempty"`
}

type jsonSummary struct {
	Checked int  `json:"checked"`
	Allowed int  `json:"allowed"`
	Blocked int  `json:"blocked"`
	Failed  bool `json:"failed"`
}

// JSON writes the report as indented JSON.
func JSON(w io.Writer, r Report) error {
	allowed, blocked := r.Counts()
	out := jsonReport{
		MinimumReleaseAge: r.MinimumAge.String(),
		Summary:           jsonSummary{Checked: allowed + blocked, Allowed: allowed, Blocked: blocked, Failed: blocked > 0},
		Warnings:          r.Warnings,
	}
	for _, d := range r.Decisions {
		jd := jsonDecision{
			Ecosystem: d.Dependency.Ecosystem,
			Package:   d.Dependency.Name,
			Version:   d.Dependency.Version,
			Source:    d.Dependency.Source,
			Direct:    d.Dependency.Direct,
			Allowed:   d.Allowed,
			Reason:    d.Reason,
		}
		if !d.Metadata.PublishedAt.IsZero() {
			jd.Published = d.Metadata.PublishedAt.UTC().Format(time.RFC3339)
		}
		out.Decisions = append(out.Decisions, jd)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
