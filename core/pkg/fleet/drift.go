package fleet

import "github.com/kubescape/opa-utils/reporthandling/apis"

// markDrift flags every control whose status in some cluster differs from the
// baseline cluster.
//
// Statuses are compared as they are, without collapsing them. A control the
// baseline failed and another cluster skipped is drift: skipped means the
// control did not apply there, which is a real difference between the two
// clusters and usually the more interesting one. Clusters whose scan did not
// complete are excluded, because a failed scan says nothing about posture.
func markDrift(report *FleetReport, baseline string) {
	if baseline == "" || !baselineIsUsable(report, baseline) {
		return
	}

	for i := range report.Controls {
		row := &report.Controls[i]
		want, ok := row.Status[baseline]
		if !ok {
			continue
		}
		for _, cluster := range report.Clusters {
			if cluster.Context == baseline || !cluster.Scanned {
				continue
			}
			if row.Status[cluster.Context] != want {
				row.Drift = true
				break
			}
		}
	}
}

// baselineIsUsable reports whether the named context produced results. Comparing
// against a cluster whose own scan failed would call every control drifted.
func baselineIsUsable(report *FleetReport, context string) bool {
	for _, cluster := range report.Clusters {
		if cluster.Context == context {
			return cluster.Scanned
		}
	}
	return false
}

// DriftedControls returns the controls that differ from the baseline.
func (r *FleetReport) DriftedControls() []ControlRow {
	var drifted []ControlRow
	for _, row := range r.Controls {
		if row.Drift {
			drifted = append(drifted, row)
		}
	}
	return drifted
}

// HasDrift reports whether any control differs from the baseline. CI can use
// this to decide an exit code.
func (r *FleetReport) HasDrift() bool {
	for _, row := range r.Controls {
		if row.Drift {
			return true
		}
	}
	return false
}

// StatusLabel renders a status for display, keeping not-scanned distinct from
// skipped.
func StatusLabel(status apis.ScanningStatus) string {
	switch status {
	case apis.StatusFailed:
		return "fail"
	case apis.StatusPassed:
		return "pass"
	case apis.StatusSkipped:
		return "skip"
	case NotScanned:
		return "-"
	default:
		return "?"
	}
}
