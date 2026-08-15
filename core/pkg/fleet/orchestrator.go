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
// directly so that ordering and failure handling can be tested without a
// cluster. Production wiring is in cmd/scan.
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
	// OnClusterStart, when set, is called before each cluster is scanned so a
	// caller can show progress on a long fleet run.
	OnClusterStart func(kubeContext string, index, total int)
	// OnClusterDone, when set, is called after each cluster finishes.
	OnClusterDone func(result ClusterResult)
}

func NewOrchestrator(scan ScanFunc) *Orchestrator {
	return &Orchestrator{scan: scan}
}

// Run scans every context in order and returns one result per context, in the
// same order.
//
// A cluster that fails to scan does not stop the run: for a real fleet, some
// clusters being unreachable is normal, and a partial answer is more useful than
// none. The failure is recorded on the result so the report can say which
// clusters are missing rather than silently omitting them. A cancelled context
// does stop the run, since that is the caller asking to stop.
func (o *Orchestrator) Run(ctx context.Context, kubeContexts []string) ([]ClusterResult, error) {
	if o.scan == nil {
		return nil, fmt.Errorf("fleet: no scan function configured")
	}
	if len(kubeContexts) == 0 {
		return nil, fmt.Errorf("fleet: no cluster contexts given")
	}
	if err := validateContexts(kubeContexts); err != nil {
		return nil, err
	}

	results := make([]ClusterResult, 0, len(kubeContexts))
	for i, kubeContext := range kubeContexts {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if o.OnClusterStart != nil {
			o.OnClusterStart(kubeContext, i+1, len(kubeContexts))
		}

		started := time.Now()
		report, err := o.scan(ctx, kubeContext)
		result := ClusterResult{
			Context:  kubeContext,
			Report:   report,
			Err:      err,
			Duration: time.Since(started),
		}
		if err == nil && report == nil {
			result.Err = fmt.Errorf("fleet: scan of %q returned no report", kubeContext)
		}

		results = append(results, result)
		if o.OnClusterDone != nil {
			o.OnClusterDone(result)
		}
	}

	return results, nil
}

// validateContexts rejects empty and duplicate names before any scanning starts,
// so a typo fails immediately rather than after the first cluster has already
// been scanned.
func validateContexts(kubeContexts []string) error {
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
