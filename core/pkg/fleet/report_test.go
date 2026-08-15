package fleet

import (
	"errors"
	"fmt"
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

// scanned builds the snapshot a successful cluster scan produces.
func scanned(kubeContext, clusterName string, compliance float32, controls map[string]apis.ScanningStatus) ClusterSnapshot {
	return Snapshot(kubeContext, reportWith(clusterName, compliance, controls), nil, 0)
}

// unreachable builds the snapshot a failed cluster scan produces.
func unreachable(kubeContext, reason string) ClusterSnapshot {
	return Snapshot(kubeContext, nil, errors.New(reason), 0)
}

func TestBuildMatrixKeepsClusterOrderAndSortsControls(t *testing.T) {
	report := Build([]ClusterSnapshot{
		scanned("prod", "prod-eu", 60, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusFailed, "C-0002": apis.StatusPassed,
		}),
		scanned("staging", "staging-eu", 90, map[string]apis.ScanningStatus{
			"C-0016": apis.StatusPassed, "C-0002": apis.StatusPassed,
		}),
	}, "")

	assert.Equal(t, []string{"prod", "staging"}, report.Contexts(), "clusters keep the order the user listed")
	require.Len(t, report.Controls, 2)
	assert.Equal(t, "C-0002", report.Controls[0].ControlID, "controls sort by ID so repeat runs diff cleanly")
	assert.Equal(t, "C-0016", report.Controls[1].ControlID)

	assert.Equal(t, apis.StatusFailed, report.Controls[1].Status["prod"])
	assert.Equal(t, apis.StatusPassed, report.Controls[1].Status["staging"])
}

func TestBuildSummarisesEachCluster(t *testing.T) {
	snapshot := Snapshot("prod", reportWith("prod-eu", 61.5, map[string]apis.ScanningStatus{
		"C-0001": apis.StatusFailed,
		"C-0002": apis.StatusPassed,
		"C-0003": apis.StatusSkipped,
	}), nil, 3*time.Second)

	report := Build([]ClusterSnapshot{snapshot}, "")

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
	report := Build([]ClusterSnapshot{
		scanned("prod", "prod-eu", 60, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed}),
		unreachable("dr", "dial tcp: i/o timeout"),
	}, "")

	require.Len(t, report.Clusters, 2)
	assert.False(t, report.Clusters[1].Scanned)
	assert.Contains(t, report.Clusters[1].Error, "i/o timeout")
	assert.Equal(t, []string{"dr"}, report.UnscannedClusters())
}

// A cluster that never reported a control is not the same as that cluster
// passing it, so the cell is filled explicitly rather than left empty.
func TestBuildMarksMissingCellsAsNotScanned(t *testing.T) {
	report := Build([]ClusterSnapshot{
		scanned("prod", "prod-eu", 60, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed}),
		unreachable("dr", "unreachable"),
	}, "")

	require.Len(t, report.Controls, 1)
	assert.Equal(t, NotScanned, report.Controls[0].Status["dr"])
	assert.Equal(t, "-", StatusLabel(report.Controls[0].Status["dr"]))
}

// Clusters do not all run the same control set: one may be on a Kubernetes
// version where a control does not apply, or scanned with a narrower framework.
func TestBuildHandlesClustersWithDifferentControlSets(t *testing.T) {
	report := Build([]ClusterSnapshot{
		scanned("prod", "prod", 60, map[string]apis.ScanningStatus{
			"C-0001": apis.StatusFailed,
			"C-0002": apis.StatusPassed,
		}),
		scanned("edge", "edge", 80, map[string]apis.ScanningStatus{
			"C-0002": apis.StatusPassed,
			"C-0003": apis.StatusFailed,
		}),
	}, "")

	require.Len(t, report.Controls, 3, "the matrix is the union of every cluster's controls")
	assert.Equal(t, NotScanned, report.Controls[0].Status["edge"], "C-0001 was never reported by edge")
	assert.Equal(t, NotScanned, report.Controls[2].Status["prod"], "C-0003 was never reported by prod")
	assert.Equal(t, apis.StatusPassed, report.Controls[1].Status["prod"])
}

func TestFailingClusters(t *testing.T) {
	report := Build([]ClusterSnapshot{
		scanned("prod", "prod", 60, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed}),
		scanned("staging", "staging", 90, map[string]apis.ScanningStatus{"C-0016": apis.StatusPassed}),
		scanned("dev", "dev", 55, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed}),
	}, "")

	assert.Equal(t, []string{"prod", "dev"}, report.FailingClusters("C-0016"))
	assert.Empty(t, report.FailingClusters("C-9999"))
}

func TestBuildWithNoSnapshots(t *testing.T) {
	report := Build(nil, "")

	assert.Empty(t, report.Clusters)
	assert.Empty(t, report.Controls)
	assert.False(t, report.HasDrift())
}

// CI needs to tell "every cluster was unreachable" apart from "the fleet is
// clean", so the counts have to make that distinguishable.
func TestUnscannedClustersCoversTheWholeFleet(t *testing.T) {
	report := Build([]ClusterSnapshot{
		unreachable("a", "context does not exist"),
		unreachable("b", "context does not exist"),
	}, "")

	assert.Len(t, report.UnscannedClusters(), 2)
	assert.Len(t, report.Clusters, 2)
	assert.Empty(t, report.Controls, "no cluster reported, so there is no matrix")
}

// A fleet is normally larger than the two clusters most tests use.
func TestBuildScalesToALargerFleet(t *testing.T) {
	const clusters = 25

	snapshots := make([]ClusterSnapshot, 0, clusters)
	for i := range clusters {
		status := apis.StatusPassed
		if i%3 == 0 {
			status = apis.StatusFailed
		}
		name := fmt.Sprintf("cluster-%02d", i)
		snapshots = append(snapshots, scanned(name, name, 70, map[string]apis.ScanningStatus{
			"C-0001": status,
			"C-0002": apis.StatusPassed,
		}))
	}

	report := Build(snapshots, snapshots[1].Context)

	assert.Len(t, report.Clusters, clusters)
	require.Len(t, report.Controls, 2)
	for _, row := range report.Controls {
		assert.Len(t, row.Status, clusters, "every cluster gets a cell in every row")
	}
	assert.True(t, report.HasDrift())
}

// Snapshot keeps only what the aggregate reads, so a fleet run does not retain a
// full report per cluster.
func TestSnapshotKeepsOnlyWhatTheAggregateReads(t *testing.T) {
	snapshot := Snapshot("prod", reportWith("prod-eu", 61.5, map[string]apis.ScanningStatus{
		"C-0016": apis.StatusFailed,
	}), nil, time.Second)

	assert.Equal(t, "prod", snapshot.Context)
	assert.Equal(t, "prod-eu", snapshot.ClusterName)
	assert.InDelta(t, 61.5, snapshot.ComplianceScore, 0.01)
	require.Len(t, snapshot.Controls, 1)
	assert.Equal(t, apis.StatusFailed, snapshot.Controls["C-0016"].Status)
	assert.Equal(t, float32(7), snapshot.Controls["C-0016"].ScoreFactor)
	assert.True(t, snapshot.Scanned())
	assert.InDelta(t, 1.0, snapshot.Duration.Seconds(), 0.01)
}

func TestSnapshotOfAFailedScan(t *testing.T) {
	snapshot := Snapshot("dr", nil, errors.New("unreachable"), time.Second)

	assert.False(t, snapshot.Scanned())
	assert.Nil(t, snapshot.Controls)
	assert.Equal(t, "dr", snapshot.Context)
	assert.Error(t, snapshot.Err)
}

// A fleet report is only a like-for-like comparison if every cluster ran the
// same frameworks, so the report records what each one actually ran.
func TestSnapshotRecordsFrameworksActuallyScanned(t *testing.T) {
	report := reportWith("prod", 70, map[string]apis.ScanningStatus{"C-0016": apis.StatusFailed})
	report.SummaryDetails.Frameworks = []reportsummary.FrameworkSummary{
		{Name: "nsa"}, {Name: "mitre"},
	}

	snapshot := Snapshot("prod", report, nil, 0)

	assert.Equal(t, []string{"mitre", "nsa"}, snapshot.Frameworks, "sorted so repeat runs match")

	fleetReport := Build([]ClusterSnapshot{snapshot}, "")
	assert.Equal(t, []string{"mitre", "nsa"}, fleetReport.Clusters[0].Frameworks)
}

func TestSnapshotOfAFailedScanRecordsNoFrameworks(t *testing.T) {
	assert.Nil(t, Snapshot("dr", nil, errors.New("unreachable"), 0).Frameworks)
}
