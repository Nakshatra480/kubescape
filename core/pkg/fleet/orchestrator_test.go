package fleet

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubescape/opa-utils/reporthandling/apis"
	v2 "github.com/kubescape/opa-utils/reporthandling/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func passingScan(kubeContext string) *v2.PostureReport {
	return reportWith(kubeContext+"-cluster", 80, map[string]apis.ScanningStatus{"C-0016": apis.StatusPassed})
}

// The whole design depends on never scanning two clusters at once, because the
// Kubernetes client config in k8s-interface is process-global. This asserts the
// orchestrator holds that line.
func TestRunNeverScansTwoClustersAtOnce(t *testing.T) {
	var inFlight, maxInFlight int32

	orchestrator := NewOrchestrator(func(_ context.Context, kubeContext string) (*v2.PostureReport, error) {
		current := atomic.AddInt32(&inFlight, 1)
		for {
			observed := atomic.LoadInt32(&maxInFlight)
			if current <= observed || atomic.CompareAndSwapInt32(&maxInFlight, observed, current) {
				break
			}
		}
		defer atomic.AddInt32(&inFlight, -1)
		return passingScan(kubeContext), nil
	})

	_, err := orchestrator.Run(context.Background(), []string{"a", "b", "c", "d"})

	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&maxInFlight), "scans must run one at a time")
}

func TestRunScansInTheOrderGiven(t *testing.T) {
	var order []string

	orchestrator := NewOrchestrator(func(_ context.Context, kubeContext string) (*v2.PostureReport, error) {
		order = append(order, kubeContext)
		return passingScan(kubeContext), nil
	})

	snapshots, err := orchestrator.Run(context.Background(), []string{"prod", "staging", "dev"})

	require.NoError(t, err)
	assert.Equal(t, []string{"prod", "staging", "dev"}, order)
	require.Len(t, snapshots, 3)
	assert.Equal(t, "prod", snapshots[0].Context)
	assert.Equal(t, "dev", snapshots[2].Context)
}

// For a real fleet some clusters are always unreachable, so one failure must not
// throw away the results from every other cluster.
func TestRunContinuesAfterAClusterFails(t *testing.T) {
	orchestrator := NewOrchestrator(func(_ context.Context, kubeContext string) (*v2.PostureReport, error) {
		if kubeContext == "dr" {
			return nil, errors.New("dial tcp: i/o timeout")
		}
		return passingScan(kubeContext), nil
	})

	snapshots, err := orchestrator.Run(context.Background(), []string{"prod", "dr", "staging"})

	require.NoError(t, err)
	require.Len(t, snapshots, 3)
	assert.NoError(t, snapshots[0].Err)
	assert.Error(t, snapshots[1].Err)
	assert.False(t, snapshots[1].Scanned())
	assert.NoError(t, snapshots[2].Err, "the cluster after the failure is still scanned")
}

func TestRunStopsWhenTheCallerCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	scanned := 0

	orchestrator := NewOrchestrator(func(_ context.Context, kubeContext string) (*v2.PostureReport, error) {
		scanned++
		cancel()
		return passingScan(kubeContext), nil
	})

	snapshots, err := orchestrator.Run(ctx, []string{"a", "b", "c"})

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, scanned, "cancelling stops the run rather than finishing the list")
	assert.Len(t, snapshots, 1)
}

// A scanner that returns neither report nor error would otherwise produce a
// cluster row that claims success with nothing in it.
func TestRunTreatsAMissingReportAsAFailure(t *testing.T) {
	orchestrator := NewOrchestrator(func(_ context.Context, _ string) (*v2.PostureReport, error) {
		return nil, nil
	})

	snapshots, err := orchestrator.Run(context.Background(), []string{"prod"})

	require.NoError(t, err)
	require.Len(t, snapshots, 1)
	assert.Error(t, snapshots[0].Err)
}

func TestRunReportsProgress(t *testing.T) {
	var started, done int

	orchestrator := NewOrchestrator(func(_ context.Context, kubeContext string) (*v2.PostureReport, error) {
		return passingScan(kubeContext), nil
	})
	orchestrator.OnClusterStart = func(_ string, index, total int) {
		started++
		assert.Equal(t, 2, total)
		assert.Equal(t, started, index)
	}
	orchestrator.OnClusterDone = func(ClusterSnapshot) { done++ }

	_, err := orchestrator.Run(context.Background(), []string{"a", "b"})

	require.NoError(t, err)
	assert.Equal(t, 2, started)
	assert.Equal(t, 2, done)
}

// Bad input fails before any cluster is touched, so a typo does not cost a full
// scan of the clusters listed before it.
func TestRunRejectsBadContextListsBeforeScanning(t *testing.T) {
	cases := []struct {
		name     string
		contexts []string
		wantErr  string
	}{
		{name: "empty list", contexts: nil, wantErr: "no cluster contexts"},
		{name: "empty name", contexts: []string{"prod", ""}, wantErr: "empty cluster context"},
		{name: "duplicate", contexts: []string{"prod", "prod"}, wantErr: "duplicate cluster context"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scanned := 0
			orchestrator := NewOrchestrator(func(_ context.Context, kubeContext string) (*v2.PostureReport, error) {
				scanned++
				return passingScan(kubeContext), nil
			})

			_, err := orchestrator.Run(context.Background(), tc.contexts)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Zero(t, scanned, "nothing is scanned when the list is rejected")
		})
	}
}

func TestRunWithoutAScanFunc(t *testing.T) {
	_, err := (&Orchestrator{}).Run(context.Background(), []string{"prod"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no scan function")
}

// A cluster that hangs rather than refusing would otherwise stall every cluster
// queued behind it.
func TestRunBoundsEachClusterWithATimeout(t *testing.T) {
	orchestrator := NewOrchestrator(func(ctx context.Context, kubeContext string) (*v2.PostureReport, error) {
		if kubeContext == "hangs" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return passingScan(kubeContext), nil
	})
	orchestrator.PerClusterTimeout = 50 * time.Millisecond

	started := time.Now()
	snapshots, err := orchestrator.Run(context.Background(), []string{"hangs", "fine"})

	require.NoError(t, err)
	require.Len(t, snapshots, 2)
	assert.False(t, snapshots[0].Scanned())
	assert.Contains(t, snapshots[0].Err.Error(), "exceeded 50ms")
	assert.True(t, snapshots[1].Scanned(), "the cluster behind the hung one is still scanned")
	assert.Less(t, time.Since(started), 2*time.Second)
}

// The timeout is per cluster, so a slow fleet is not cut short as long as each
// cluster individually finishes in time.
func TestPerClusterTimeoutIsNotAFleetBudget(t *testing.T) {
	orchestrator := NewOrchestrator(func(_ context.Context, kubeContext string) (*v2.PostureReport, error) {
		time.Sleep(20 * time.Millisecond)
		return passingScan(kubeContext), nil
	})
	orchestrator.PerClusterTimeout = 100 * time.Millisecond

	snapshots, err := orchestrator.Run(context.Background(), []string{"a", "b", "c", "d", "e"})

	require.NoError(t, err)
	for _, snapshot := range snapshots {
		assert.True(t, snapshot.Scanned(), "%s should finish inside its own budget", snapshot.Context)
	}
}

func TestRunRecordsHowLongEachClusterTook(t *testing.T) {
	orchestrator := NewOrchestrator(func(_ context.Context, kubeContext string) (*v2.PostureReport, error) {
		time.Sleep(10 * time.Millisecond)
		return passingScan(kubeContext), nil
	})

	snapshots, err := orchestrator.Run(context.Background(), []string{"a"})

	require.NoError(t, err)
	assert.Greater(t, snapshots[0].Duration, time.Duration(0))
}
