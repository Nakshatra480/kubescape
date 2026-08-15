package fleet

import (
	"time"

	"github.com/kubescape/opa-utils/reporthandling/apis"
	v2 "github.com/kubescape/opa-utils/reporthandling/v2"
)

// ControlOutcome is what a fleet report needs to know about one control on one
// cluster.
type ControlOutcome struct {
	Name        string
	ScoreFactor float32
	Status      apis.ScanningStatus
}

// ClusterSnapshot is the compact result of scanning one cluster.
//
// A PostureReport carries every scanned resource and every per-resource result,
// which for a real cluster is far larger than the summary a fleet report reads.
// Holding one per cluster for the length of the run would make memory grow with
// the size of the fleet times the size of each cluster, when the aggregate only
// ever reads four fields. Snapshotting on the way out of each scan lets the full
// report be collected immediately, so memory grows with the number of controls
// instead.
type ClusterSnapshot struct {
	Context         string
	ClusterName     string
	ComplianceScore float32
	Status          apis.ScanningStatus
	Controls        map[string]ControlOutcome
	Err             error
	Duration        time.Duration
}

// Snapshot reduces a single-cluster report to what the fleet aggregate reads.
// report may be nil, which is how a failed cluster is recorded.
func Snapshot(kubeContext string, report *v2.PostureReport, err error, took time.Duration) ClusterSnapshot {
	snapshot := ClusterSnapshot{
		Context:  kubeContext,
		Err:      err,
		Duration: took,
	}
	if report == nil {
		return snapshot
	}

	snapshot.ClusterName = report.ClusterName
	snapshot.ComplianceScore = report.SummaryDetails.ComplianceScore
	snapshot.Status = report.SummaryDetails.Status
	snapshot.Controls = make(map[string]ControlOutcome, len(report.SummaryDetails.Controls))
	for id, control := range report.SummaryDetails.Controls {
		snapshot.Controls[id] = ControlOutcome{
			Name:        control.Name,
			ScoreFactor: control.ScoreFactor,
			Status:      control.Status,
		}
	}
	return snapshot
}

// Scanned reports whether this cluster produced results.
func (s ClusterSnapshot) Scanned() bool {
	return s.Err == nil && s.Controls != nil
}

func (s ClusterSnapshot) counts() (failed, passed, skipped int) {
	for _, control := range s.Controls {
		switch control.Status {
		case apis.StatusFailed:
			failed++
		case apis.StatusPassed:
			passed++
		case apis.StatusSkipped:
			skipped++
		}
	}
	return failed, passed, skipped
}
