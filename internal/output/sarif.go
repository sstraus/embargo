package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Minimal SARIF 2.1.0 emitter. Only blocked dependencies are emitted as
// results (level "error"), which is what CI security dashboards consume.

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool        sarifTool         `json:"tool"`
	Results     []sarifResult     `json:"results"`
	Invocations []sarifInvocation `json:"invocations,omitempty"`
}

type sarifInvocation struct {
	ExecutionSuccessful        bool                `json:"executionSuccessful"`
	ToolExecutionNotifications []sarifNotification `json:"toolExecutionNotifications,omitempty"`
}

type sarifNotification struct {
	Level   string       `json:"level"`
	Message sarifMessage `json:"message"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

// SARIF writes blocked dependencies as a SARIF 2.1.0 log.
func SARIF(w io.Writer, r Report) error {
	const ruleID = "embargo/min-release-age"
	run := sarifRun{
		Tool: sarifTool{Driver: sarifDriver{
			Name:           "embargo",
			InformationURI: "https://github.com/sstraus/embargo",
			Rules:          []sarifRule{{ID: ruleID, Name: "MinimumReleaseAge"}},
		}},
		Results: []sarifResult{},
	}
	if len(r.Warnings) > 0 {
		notifications := make([]sarifNotification, 0, len(r.Warnings))
		for _, w := range r.Warnings {
			notifications = append(notifications, sarifNotification{
				Level:   "warning",
				Message: sarifMessage{Text: w},
			})
		}
		run.Invocations = []sarifInvocation{{
			ExecutionSuccessful:        true,
			ToolExecutionNotifications: notifications,
		}}
	}
	for _, d := range r.Blocked() {
		uri := d.Dependency.Source
		if uri == "" {
			uri = "."
		}
		run.Results = append(run.Results, sarifResult{
			RuleID:  ruleID,
			Level:   "error",
			Message: sarifMessage{Text: fmt.Sprintf("%s %s@%s blocked: %s", d.Dependency.Ecosystem, d.Dependency.Name, d.Dependency.Version, d.Reason)},
			Locations: []sarifLocation{{PhysicalLocation: sarifPhysical{
				ArtifactLocation: sarifArtifact{URI: uri},
			}}},
		})
	}
	log := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs:    []sarifRun{run},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}
