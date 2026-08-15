// Package fleet aggregates the results of scanning several clusters into a
// single report that can answer questions no single-cluster report can, such as
// which clusters fail a control and where a cluster has drifted from a baseline.
//
// Aggregation is deliberately separate from orchestration: everything in this
// file is a pure function over PostureReport values, so it can be tested
// without a cluster.
package fleet

import (
	"sort"
	"time"

	"github.com/kubescape/opa-utils/reporthandling/apis"
	v2 "github.com/kubescape/opa-utils/reporthandling/v2"
)

// NotScanned marks a control cell for a cluster whose scan did not complete. It
// is distinct from StatusSkipped, which means the scan ran and the control did
// not apply.
const NotScanned apis.ScanningStatus = "not-scanned"

// ClusterResult is the outcome of scanning one kubeconfig context. Report is nil
// when Err is set.
type ClusterResult struct {
	Context  string
	Report   *v2.PostureReport
	Err      error
	Duration time.Duration
}

// ClusterSummary is the per-cluster header of a fleet report.
type ClusterSummary struct {
	Context         string              `json:"context"`
	ClusterName     string              `json:"clusterName,omitempty"`
	Scanned         bool                `json:"scanned"`
	Error           string              `json:"error,omitempty"`
	ComplianceScore float32             `json:"complianceScore"`
	Status          apis.ScanningStatus `json:"status,omitempty"`
	Failed          int                 `json:"failedControls"`
	Passed          int                 `json:"passedControls"`
	Skipped         int                 `json:"skippedControls"`
	DurationSeconds float64             `json:"durationSeconds"`
}

// ControlRow is one row of the control by cluster matrix.
type ControlRow struct {
	ControlID   string                         `json:"controlID"`
	Name        string                         `json:"name"`
	ScoreFactor float32                        `json:"scoreFactor"`
	Status      map[string]apis.ScanningStatus `json:"status"`
	Drift       bool                           `json:"drift,omitempty"`
}

// FleetReport is the aggregate of several single-cluster reports. It is additive:
// nothing here changes or embeds an existing report type.
type FleetReport struct {
	GeneratedAt time.Time        `json:"generationTime"`
	Baseline    string           `json:"baseline,omitempty"`
	Clusters    []ClusterSummary `json:"clusters"`
	Controls    []ControlRow     `json:"controls"`
}

// Build aggregates per-cluster results into a fleet report. Clusters keep the
// order they were scanned in, which is the order the user listed them. Controls
// are sorted by ID so repeated runs produce identical output and a CI diff is
// meaningful.
//
// baseline names a context to compare against. When it is empty, or names a
// cluster that was not scanned successfully, no drift is computed.
func Build(results []ClusterResult, baseline string) *FleetReport {
	report := &FleetReport{
		GeneratedAt: time.Now().UTC(),
		Clusters:    make([]ClusterSummary, 0, len(results)),
		Controls:    []ControlRow{},
	}

	rows := map[string]*ControlRow{}
	for _, result := range results {
		report.Clusters = append(report.Clusters, summarize(result))
		if result.Report == nil {
			continue
		}
		for id, control := range result.Report.SummaryDetails.Controls {
			row, ok := rows[id]
			if !ok {
				row = &ControlRow{
					ControlID:   id,
					Name:        control.Name,
					ScoreFactor: control.ScoreFactor,
					Status:      map[string]apis.ScanningStatus{},
				}
				rows[id] = row
			}
			row.Status[result.Context] = control.Status
		}
	}

	// A control the baseline knows about but another cluster never reported is
	// not the same as that cluster passing it, so fill the gap explicitly.
	for _, row := range rows {
		for _, result := range results {
			if _, ok := row.Status[result.Context]; !ok {
				row.Status[result.Context] = NotScanned
			}
		}
	}

	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		report.Controls = append(report.Controls, *rows[id])
	}

	report.Baseline = baseline
	markDrift(report, baseline)
	return report
}

func summarize(result ClusterResult) ClusterSummary {
	summary := ClusterSummary{
		Context:         result.Context,
		Scanned:         result.Err == nil && result.Report != nil,
		DurationSeconds: result.Duration.Seconds(),
	}
	if result.Err != nil {
		summary.Error = result.Err.Error()
	}
	if result.Report == nil {
		return summary
	}

	summary.ClusterName = result.Report.ClusterName
	summary.ComplianceScore = result.Report.SummaryDetails.ComplianceScore
	summary.Status = result.Report.SummaryDetails.Status
	for _, control := range result.Report.SummaryDetails.Controls {
		switch control.Status {
		case apis.StatusFailed:
			summary.Failed++
		case apis.StatusPassed:
			summary.Passed++
		case apis.StatusSkipped:
			summary.Skipped++
		}
	}
	return summary
}

// Contexts returns the cluster contexts in report order.
func (r *FleetReport) Contexts() []string {
	contexts := make([]string, 0, len(r.Clusters))
	for _, cluster := range r.Clusters {
		contexts = append(contexts, cluster.Context)
	}
	return contexts
}

// FailingClusters returns the contexts where the given control failed.
func (r *FleetReport) FailingClusters(controlID string) []string {
	var failing []string
	for _, row := range r.Controls {
		if row.ControlID != controlID {
			continue
		}
		for _, cluster := range r.Clusters {
			if row.Status[cluster.Context] == apis.StatusFailed {
				failing = append(failing, cluster.Context)
			}
		}
	}
	return failing
}
