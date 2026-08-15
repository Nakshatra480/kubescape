package fleet

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/kubescape/opa-utils/reporthandling/apis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func twoClusterReport(t *testing.T) *FleetReport {
	t.Helper()
	return Build([]ClusterSnapshot{
		scanned("staging", "staging-eu", 90, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusPassed,
			"C-0017": apis.StatusPassed,
		}),
		scanned("prod", "prod-eu", 60, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusFailed,
			"C-0017": apis.StatusPassed,
		}),
	}, "staging")
}

func TestPrintTableShowsMatrixAndDrift(t *testing.T) {
	var out bytes.Buffer

	PrintTable(&out, twoClusterReport(t), false)
	got := out.String()

	assert.Contains(t, got, "staging")
	assert.Contains(t, got, "prod")
	assert.Contains(t, got, "staging-eu")
	assert.Contains(t, got, "C-0016")
	assert.Contains(t, got, "C-0017")
	assert.Contains(t, got, "fail")
	assert.Contains(t, got, "pass")
	assert.Contains(t, got, "Baseline: staging")
	assert.Contains(t, got, "1 of 2 controls differ")
}

func TestPrintTableDriftOnlyHidesAgreeingControls(t *testing.T) {
	var out bytes.Buffer

	PrintTable(&out, twoClusterReport(t), true)
	got := out.String()

	assert.Contains(t, got, "C-0016", "the drifting control is shown")
	assert.NotContains(t, got, "C-0017", "controls that agree everywhere are hidden")
}

func TestPrintTableWithNoDriftSaysSo(t *testing.T) {
	report := Build([]ClusterSnapshot{
		scanned("staging", "staging", 90, map[string]apis.ScanningStatus{"C-0016": apis.StatusPassed}),
		scanned("prod", "prod", 90, map[string]apis.ScanningStatus{"C-0016": apis.StatusPassed}),
	}, "staging")

	var out bytes.Buffer
	PrintTable(&out, report, true)

	assert.Contains(t, out.String(), "No drift from baseline")
}

// A partial fleet scan must say which clusters are missing, or it reads as a
// complete answer.
func TestPrintTableNamesUnscannedClusters(t *testing.T) {
	report := Build([]ClusterSnapshot{
		scanned("prod", "prod", 60, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed}),
		unreachable("dr", "dial tcp: i/o timeout"),
	}, "")

	var out bytes.Buffer
	PrintTable(&out, report, false)
	got := out.String()

	assert.Contains(t, got, "dr was not scanned")
	assert.Contains(t, got, "i/o timeout")
}

func TestPrintJSONRoundTrips(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, PrintJSON(&out, twoClusterReport(t)))

	var decoded FleetReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	assert.Equal(t, "staging", decoded.Baseline)
	require.Len(t, decoded.Clusters, 2)
	require.Len(t, decoded.Controls, 2)
	assert.True(t, decoded.HasDrift())
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "short", truncate("short", 10))
	assert.Equal(t, "exactlyten", truncate("exactlyten", 10))
	assert.Equal(t, "aaaaaaa...", truncate("aaaaaaaaaaaaaaa", 10))
	assert.Equal(t, "ab", truncate("abc", 2))
}

// A golden rendering so a future refactor of the aggregate or the printer is
// provably behaviour-preserving without needing a cluster.
func TestPrintTableGoldenOutput(t *testing.T) {
	report := Build([]ClusterSnapshot{
		scanned("staging", "staging-eu", 90, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusPassed,
			"C-0017": apis.StatusPassed,
		}),
		scanned("prod", "prod-eu", 60, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusFailed,
			"C-0017": apis.StatusPassed,
		}),
		unreachable("dr", "dial tcp: i/o timeout"),
	}, "staging")

	var out bytes.Buffer
	PrintTable(&out, report, false)

	want := `+---------+------------+---------+------------+--------+--------+---------+
| Context | Cluster    | Scanned | Compliance | Failed | Passed | Skipped |
+---------+------------+---------+------------+--------+--------+---------+
| staging | staging-eu | yes     | 90.0%      |      0 |      2 |       0 |
| prod    | prod-eu    | yes     | 60.0%      |      1 |      1 |       0 |
| dr      |            | no      | -          |      0 |      0 |       0 |
+---------+------------+---------+------------+--------+--------+---------+
dr was not scanned: dial tcp: i/o timeout

+---------+----------------+---------+------+----+-------+
| Control | Name           | staging | prod | dr | Drift |
+---------+----------------+---------+------+----+-------+
| C-0016  | control C-0016 | pass    | fail | -  | yes   |
| C-0017  | control C-0017 | pass    | pass | -  |       |
+---------+----------------+---------+------+----+-------+

Baseline: staging. 1 of 2 controls differ from it.
`

	assert.Equal(t, want, out.String())
}
