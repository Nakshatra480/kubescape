package fleet

import (
	"testing"

	"github.com/kubescape/opa-utils/reporthandling/apis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDriftFlagsControlsThatDifferFromBaseline(t *testing.T) {
	results := []ClusterSnapshot{
		scanned("staging", "staging", 90, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusPassed,
			"C-0017": apis.StatusPassed,
		}),
		scanned("prod", "prod", 60, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusFailed,
			"C-0017": apis.StatusPassed,
		}),
	}

	report := Build(results, "staging")

	require.Len(t, report.Controls, 2)
	assert.True(t, report.Controls[0].Drift, "C-0016 passes in staging and fails in prod")
	assert.False(t, report.Controls[1].Drift, "C-0017 agrees everywhere")

	assert.True(t, report.HasDrift())
	drifted := report.DriftedControls()
	require.Len(t, drifted, 1)
	assert.Equal(t, "C-0016", drifted[0].ControlID)
}

// Skipped means the control did not apply to that cluster, which is a real
// difference from a cluster where it failed. Collapsing skipped into pass would
// hide exactly the kind of divergence a fleet report exists to show.
func TestSkippedCountsAsDriftAgainstFailed(t *testing.T) {
	results := []ClusterSnapshot{
		scanned("staging", "staging", 90, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed}),
		scanned("prod", "prod", 95, map[string]apis.ScanningStatus{"C-0016": apis.StatusSkipped}),
	}

	report := Build(results, "staging")

	require.Len(t, report.Controls, 1)
	assert.True(t, report.Controls[0].Drift)
	assert.Equal(t, "skip", StatusLabel(report.Controls[0].Status["prod"]))
}

// A scan that failed says nothing about posture, so it must not be reported as
// drift. Otherwise one unreachable cluster would flag every control in the fleet.
func TestUnscannedClusterDoesNotCountAsDrift(t *testing.T) {
	results := []ClusterSnapshot{
		scanned("staging", "staging", 90, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusPassed,
			"C-0017": apis.StatusPassed,
		}),
		unreachable("dr", "unreachable"),
	}

	report := Build(results, "staging")

	for _, row := range report.Controls {
		assert.False(t, row.Drift, "control %s should not drift because of an unreachable cluster", row.ControlID)
	}
	assert.False(t, report.HasDrift())
}

func TestNoBaselineMeansNoDrift(t *testing.T) {
	results := []ClusterSnapshot{
		scanned("staging", "staging", 90, map[string]apis.ScanningStatus{"C-0016": apis.StatusPassed}),
		scanned("prod", "prod", 60, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed}),
	}

	report := Build(results, "")

	assert.False(t, report.HasDrift())
	assert.Empty(t, report.Baseline)
}

// Naming a baseline whose own scan failed cannot produce a meaningful
// comparison, so drift is skipped rather than computed against nothing.
func TestBaselineThatFailedToScanProducesNoDrift(t *testing.T) {
	results := []ClusterSnapshot{
		unreachable("staging", "unreachable"),
		scanned("prod", "prod", 60, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed}),
	}

	report := Build(results, "staging")

	assert.False(t, report.HasDrift())
}

func TestBaselineNotInFleetProducesNoDrift(t *testing.T) {
	results := []ClusterSnapshot{
		scanned("prod", "prod", 60, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed}),
	}

	report := Build(results, "does-not-exist")

	assert.False(t, report.HasDrift())
}

func TestStatusLabel(t *testing.T) {
	assert.Equal(t, "fail", StatusLabel(apis.StatusFailed))
	assert.Equal(t, "pass", StatusLabel(apis.StatusPassed))
	assert.Equal(t, "skip", StatusLabel(apis.StatusSkipped))
	assert.Equal(t, "-", StatusLabel(NotScanned))
	assert.Equal(t, "?", StatusLabel(apis.StatusUnknown))
}
