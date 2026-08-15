package fleet

import (
	"errors"
	"testing"
	"time"

	"github.com/kubescape/opa-utils/reporthandling/apis"
	"github.com/kubescape/opa-utils/reporthandling/results/v1/reportsummary"
	v2 "github.com/kubescape/opa-utils/reporthandling/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reportWith(clusterName string, compliance float32, controls map[string]apis.ScanningStatus) *v2.PostureReport {
	summaries := reportsummary.ControlSummaries{}
	for id, status := range controls {
		summaries[id] = reportsummary.ControlSummary{
			ControlID:   id,
			Name:        "control " + id,
			Status:      status,
			ScoreFactor: 7,
		}
	}
	return &v2.PostureReport{
		ClusterName: clusterName,
		SummaryDetails: reportsummary.SummaryDetails{
			Controls:        summaries,
			ComplianceScore: compliance,
			Status:          apis.StatusFailed,
		},
	}
}

func TestBuildMatrixKeepsClusterOrderAndSortsControls(t *testing.T) {
	results := []ClusterResult{
		{Context: "prod", Report: reportWith("prod-eu", 60, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusFailed, "C-0002": apis.StatusPassed,
		})},
		{Context: "staging", Report: reportWith("staging-eu", 90, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusPassed, "C-0002": apis.StatusPassed,
		})},
	}

	report := Build(results, "")

	assert.Equal(t, []string{"prod", "staging"}, report.Contexts(), "clusters keep the order the user listed")
	require.Len(t, report.Controls, 2)
	assert.Equal(t, "C-0002", report.Controls[0].ControlID, "controls sort by ID so repeat runs diff cleanly")
	assert.Equal(t, "C-0016", report.Controls[1].ControlID)

	assert.Equal(t, apis.StatusFailed, report.Controls[1].Status["prod"])
	assert.Equal(t, apis.StatusPassed, report.Controls[1].Status["staging"])
}

func TestBuildSummarisesEachCluster(t *testing.T) {
	results := []ClusterResult{
		{
			Context:  "prod",
			Duration: 3 * time.Second,
			Report: reportWith("prod-eu", 61.5, map[string]apis.ScanningStatus{
				"C-0001": apis.StatusFailed,
				"C-0002": apis.StatusPassed,
				"C-0003": apis.StatusSkipped,
			}),
		},
	}

	report := Build(results, "")

	require.Len(t, report.Clusters, 1)
	cluster := report.Clusters[0]
	assert.True(t, cluster.Scanned)
	assert.Equal(t, "prod-eu", cluster.ClusterName)
	assert.InDelta(t, 61.5, cluster.ComplianceScore, 0.01)
	assert.Equal(t, 1, cluster.Failed)
	assert.Equal(t, 1, cluster.Passed)
	assert.Equal(t, 1, cluster.Skipped)
	assert.InDelta(t, 3.0, cluster.DurationSeconds, 0.01)
}

// A cluster that could not be scanned has to stay in the report. Dropping it
// would make a partial fleet scan look like a complete one.
func TestBuildKeepsUnscannedClusters(t *testing.T) {
	results := []ClusterResult{
		{Context: "prod", Report: reportWith("prod-eu", 60, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed})},
		{Context: "dr", Err: errors.New("dial tcp: i/o timeout")},
	}

	report := Build(results, "")

	require.Len(t, report.Clusters, 2)
	assert.False(t, report.Clusters[1].Scanned)
	assert.Contains(t, report.Clusters[1].Error, "i/o timeout")
	assert.Equal(t, []string{"dr"}, report.UnscannedClusters())
}

// A cluster that never reported a control is not the same as that cluster
// passing it, so the cell is filled explicitly rather than left empty.
func TestBuildMarksMissingCellsAsNotScanned(t *testing.T) {
	results := []ClusterResult{
		{Context: "prod", Report: reportWith("prod-eu", 60, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed})},
		{Context: "dr", Err: errors.New("unreachable")},
	}

	report := Build(results, "")

	require.Len(t, report.Controls, 1)
	assert.Equal(t, NotScanned, report.Controls[0].Status["dr"])
	assert.Equal(t, "-", StatusLabel(report.Controls[0].Status["dr"]))
}

func TestFailingClusters(t *testing.T) {
	results := []ClusterResult{
		{Context: "prod", Report: reportWith("prod", 60, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed})},
		{Context: "staging", Report: reportWith("staging", 90, map[string]apis.ScanningStatus{"C-0016": apis.StatusPassed})},
		{Context: "dev", Report: reportWith("dev", 55, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed})},
	}

	report := Build(results, "")

	assert.Equal(t, []string{"prod", "dev"}, report.FailingClusters("C-0016"))
	assert.Empty(t, report.FailingClusters("C-9999"))
}

func TestBuildWithNoResults(t *testing.T) {
	report := Build(nil, "")

	assert.Empty(t, report.Clusters)
	assert.Empty(t, report.Controls)
	assert.False(t, report.HasDrift())
}

// CI needs to tell "every cluster was unreachable" apart from "the fleet is
// clean", so the counts have to make that distinguishable.
func TestUnscannedClustersCoversTheWholeFleet(t *testing.T) {
	report := Build([]ClusterResult{
		{Context: "a", Err: errors.New("context does not exist")},
		{Context: "b", Err: errors.New("context does not exist")},
	}, "")

	assert.Len(t, report.UnscannedClusters(), 2)
	assert.Len(t, report.Clusters, 2)
	assert.Empty(t, report.Controls, "no cluster reported, so there is no matrix")
}
