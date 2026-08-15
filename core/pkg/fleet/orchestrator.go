package fleet

import (
	"context"
	"fmt"
	"time"

	v2 "github.com/kubescape/opa-utils/reporthandling/v2"
)

// ScanFunc runs one complete single-cluster scan against the given kubeconfig
// context and returns its report.
//
// The orchestrator takes this as a function rather than calling the scanner
// directly so that ordering, timeouts and failure handling can be tested without
// a cluster. Production wiring is in cmd/scan.
type ScanFunc func(ctx context.Context, kubeContext string) (*v2.PostureReport, error)

// Orchestrator scans a list of kubeconfig contexts one at a time.
//
// Scanning is sequential by construction, and that is not a performance choice
// that can be revisited in isolation. The Kubernetes client configuration in
// k8s-interface is process-global: SetClusterContextName writes a package
// variable that LoadK8sConfig then reads to set another one, with no locking.
// Running two scans concurrently races on that state, and the likely outcome is
// not a crash but a report attributed to the wrong cluster. Concurrency needs
// that client to become per-scan first.
type Orchestrator struct {
	scan ScanFunc

	// PerClusterTimeout bounds one cluster's scan. Zero means no limit. Without
	// it a single unreachable cluster that hangs rather than refusing stalls
	// every cluster queued behind it.
	PerClusterTimeout time.Duration

	// OnClusterStart, when set, is called before each cluster is scanned so a
	// caller can show progress on a long fleet run.
	OnClusterStart func(kubeContext string, index, total int)
	// OnClusterDone, when set, is called after each cluster finishes.
	OnClusterDone func(snapshot ClusterSnapshot)
}

func NewOrchestrator(scan ScanFunc) *Orchestrator {
	return &Orchestrator{scan: scan}
}

// Run scans every context in order and returns one snapshot per context, in the
// same order.
//
// A cluster that fails to scan does not stop the run: for a real fleet, some
// clusters being unreachable is normal, and a partial answer is more useful than
// none. The failure is recorded on the snapshot so the report can say which
// clusters are missing rather than silently omitting them. A cancelled context
// does stop the run, since that is the caller asking to stop.
func (o *Orchestrator) Run(ctx context.Context, kubeContexts []string) ([]ClusterSnapshot, error) {
	if o.scan == nil {
		return nil, fmt.Errorf("fleet: no scan function configured")
	}
	if err := validateContexts(kubeContexts); err != nil {
		return nil, err
	}

	snapshots := make([]ClusterSnapshot, 0, len(kubeContexts))
	for i, kubeContext := range kubeContexts {
		if err := ctx.Err(); err != nil {
			return snapshots, err
		}
		if o.OnClusterStart != nil {
			o.OnClusterStart(kubeContext, i+1, len(kubeContexts))
		}

		snapshot := o.scanOne(ctx, kubeContext)
		snapshots = append(snapshots, snapshot)

		if o.OnClusterDone != nil {
			o.OnClusterDone(snapshot)
		}
	}

	return snapshots, nil
}

// scanOne runs a single cluster and reduces the result immediately. The full
// report goes out of scope here rather than being retained for the whole run.
func (o *Orchestrator) scanOne(ctx context.Context, kubeContext string) ClusterSnapshot {
	scanCtx := ctx
	if o.PerClusterTimeout > 0 {
		var cancel context.CancelFunc
		scanCtx, cancel = context.WithTimeout(ctx, o.PerClusterTimeout)
		defer cancel()
	}

	started := time.Now()
	report, err := o.scan(scanCtx, kubeContext)
	took := time.Since(started)

	switch {
	case err != nil:
		report = nil
	case report == nil:
		err = fmt.Errorf("fleet: scan of %q returned no report", kubeContext)
	}

	// A scan that ran out of its own budget reads better as a timeout than as
	// whatever the underlying call happened to return when it was torn down.
	if o.PerClusterTimeout > 0 && scanCtx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("fleet: scan of %q exceeded %s", kubeContext, o.PerClusterTimeout)
		report = nil
	}

	return Snapshot(kubeContext, report, err, took)
}

// validateContexts rejects empty and duplicate names before any scanning starts,
// so a typo fails immediately rather than after the first cluster has already
// been scanned.
func validateContexts(kubeContexts []string) error {
	if len(kubeContexts) == 0 {
		return fmt.Errorf("fleet: no cluster contexts given")
	}
	seen := make(map[string]struct{}, len(kubeContexts))
	for _, kubeContext := range kubeContexts {
		if kubeContext == "" {
			return fmt.Errorf("fleet: empty cluster context in list")
		}
		if _, dup := seen[kubeContext]; dup {
			return fmt.Errorf("fleet: duplicate cluster context %q", kubeContext)
		}
		seen[kubeContext] = struct{}{}
	}
	return nil
}
