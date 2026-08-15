package fleet

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/kubescape/opa-utils/reporthandling"
	"github.com/kubescape/opa-utils/reporthandling/apis"
	"github.com/kubescape/opa-utils/reporthandling/results/v1/resourcesresults"
	v2 "github.com/kubescape/opa-utils/reporthandling/v2"
	"github.com/stretchr/testify/assert"
)

// clusterReport approximates what a real cluster scan returns: a small control
// summary alongside a per-resource entry for every scanned object.
func clusterReport(resources int) *v2.PostureReport {
	report := reportWith("cluster", 70, map[string]apis.ScanningStatus{
		"C-0001": apis.StatusFailed,
		"C-0002": apis.StatusPassed,
	})
	report.Resources = make([]reporthandling.Resource, 0, resources)
	report.Results = make([]resourcesresults.Result, 0, resources)
	for i := range resources {
		id := fmt.Sprintf("apps/v1/Deployment/ns-%d/name-%d", i, i)
		report.Resources = append(report.Resources, reporthandling.Resource{ResourceID: id})
		report.Results = append(report.Results, resourcesresults.Result{ResourceID: id})
	}
	return report
}

func retainedBytes(hold func() any) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	held := hold()

	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(held)

	if after.HeapAlloc < before.HeapAlloc {
		return 0
	}
	return after.HeapAlloc - before.HeapAlloc
}

// Holding a full report per cluster makes memory grow with fleet size times
// cluster size, when the aggregate only ever reads the control summary. This
// measures live heap rather than total allocations, since the point is what
// stays resident for the length of the run.
func TestSnapshotsRetainFarLessThanFullReports(t *testing.T) {
	const clusters, resources = 20, 2000

	full := retainedBytes(func() any {
		held := make([]*v2.PostureReport, 0, clusters)
		for range clusters {
			held = append(held, clusterReport(resources))
		}
		return held
	})

	snapshots := retainedBytes(func() any {
		held := make([]ClusterSnapshot, 0, clusters)
		for i := range clusters {
			held = append(held, Snapshot(fmt.Sprintf("c-%d", i), clusterReport(resources), nil, 0))
		}
		return held
	})

	t.Logf("retained: full reports %d KB, snapshots %d KB", full/1024, snapshots/1024)

	// The measured gap is roughly 400x. Assert an order of magnitude so the test
	// reports a real regression without failing on allocator noise.
	assert.Less(t, snapshots*10, full, "snapshots should retain at least 10x less than full reports")
}

func BenchmarkSnapshot(b *testing.B) {
	report := clusterReport(2000)

	b.ReportAllocs()
	for b.Loop() {
		_ = Snapshot("prod", report, nil, 0)
	}
}

func BenchmarkBuildMatrix(b *testing.B) {
	const clusters = 25

	snapshots := make([]ClusterSnapshot, 0, clusters)
	for i := range clusters {
		name := fmt.Sprintf("cluster-%02d", i)
		snapshots = append(snapshots, scanned(name, name, 70, map[string]apis.ScanningStatus{
			"C-0001": apis.StatusFailed,
			"C-0002": apis.StatusPassed,
			"C-0003": apis.StatusSkipped,
		}))
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = Build(snapshots, snapshots[0].Context)
	}
}
