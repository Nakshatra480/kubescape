// Package fleet aggregates the results of scanning several clusters into a
// single report that can answer questions no single-cluster report can, such as
// which clusters fail a control and where a cluster has drifted from a baseline.
//
// Aggregation is deliberately separate from orchestration: everything in
// report.go and drift.go is a pure function over ClusterSnapshot values, so the
// matrix and the drift rules are tested without a cluster.
package fleet

import (
	"sort"
	"time"

	"github.com/kubescape/opa-utils/reporthandling/apis"
)

// NotScanned marks a control cell for a cluster whose scan did not complete. It
// is distinct from StatusSkipped, which means the scan ran and the control did
// not apply.
const NotScanned apis.ScanningStatus = "not-scanned"

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

// FleetReport is the aggregate of several single-cluster scans. It is additive:
// nothing here changes or embeds an existing report type.
type FleetReport struct {
	GeneratedAt time.Time        `json:"generationTime"`
	Baseline    string           `json:"baseline,omitempty"`
	Clusters    []ClusterSummary `json:"clusters"`
	Controls    []ControlRow     `json:"controls"`
}

// Build aggregates per-cluster snapshots into a fleet report.
//
// Clusters keep the order they were scanned in, which is the order the user
// listed them. Controls are sorted by ID so repeated runs produce identical
// output and a CI diff is meaningful.
//
// baseline names a context to compare against. When it is empty, or names a
// cluster that was not scanned successfully, no drift is computed.
func Build(snapshots []ClusterSnapshot, baseline string) *FleetReport {
	report := &FleetReport{
		GeneratedAt: time.Now().UTC(),
		Baseline:    baseline,
		Clusters:    summarizeClusters(snapshots),
		Controls:    buildMatrix(snapshots),
	}
	markDrift(report, baseline)
	return report
}

func summarizeClusters(snapshots []ClusterSnapshot) []ClusterSummary {
	summaries := make([]ClusterSummary, 0, len(snapshots))
	for _, snapshot := range snapshots {
		failed, passed, skipped := snapshot.counts()
		summary := ClusterSummary{
			Context:         snapshot.Context,
			ClusterName:     snapshot.ClusterName,
			Scanned:         snapshot.Scanned(),
			ComplianceScore: snapshot.ComplianceScore,
			Status:          snapshot.Status,
			Failed:          failed,
			Passed:          passed,
			Skipped:         skipped,
			DurationSeconds: snapshot.Duration.Seconds(),
		}
		if snapshot.Err != nil {
			summary.Error = snapshot.Err.Error()
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

// buildMatrix pivots per-cluster control results into one row per control.
func buildMatrix(snapshots []ClusterSnapshot) []ControlRow {
	rows := map[string]*ControlRow{}
	for _, snapshot := range snapshots {
		for id, control := range snapshot.Controls {
			row, ok := rows[id]
			if !ok {
				row = &ControlRow{
					ControlID:   id,
					Name:        control.Name,
					ScoreFactor: control.ScoreFactor,
					Status:      make(map[string]apis.ScanningStatus, len(snapshots)),
				}
				rows[id] = row
			}
			row.Status[snapshot.Context] = control.Status
		}
	}

	// A cluster that never reported a control is not the same as that cluster
	// passing it, so the gap is filled explicitly rather than left absent.
	for _, row := range rows {
		for _, snapshot := range snapshots {
			if _, ok := row.Status[snapshot.Context]; !ok {
				row.Status[snapshot.Context] = NotScanned
			}
		}
	}

	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	matrix := make([]ControlRow, 0, len(ids))
	for _, id := range ids {
		matrix = append(matrix, *rows[id])
	}
	return matrix
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
	for _, row := range r.Controls {
		if row.ControlID != controlID {
			continue
		}
		var failing []string
		for _, cluster := range r.Clusters {
			if row.Status[cluster.Context] == apis.StatusFailed {
				failing = append(failing, cluster.Context)
			}
		}
		return failing
	}
	return nil
}

// UnscannedClusters returns the contexts whose scan did not complete.
func (r *FleetReport) UnscannedClusters() []string {
	var failed []string
	for _, cluster := range r.Clusters {
		if !cluster.Scanned {
			failed = append(failed, cluster.Context)
		}
	}
	return failed
}
