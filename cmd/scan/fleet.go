package scan

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/k8s-interface/k8sinterface"
	"github.com/kubescape/kubescape/v3/core/cautils"
	"github.com/kubescape/kubescape/v3/core/meta"
	"github.com/kubescape/kubescape/v3/core/pkg/fleet"
	apisv1 "github.com/kubescape/opa-utils/httpserver/apis/v1"
	v2 "github.com/kubescape/opa-utils/reporthandling/v2"
	"github.com/spf13/cobra"
)

const (
	defaultScanFormat = "pretty-printer"
	jsonScanFormat    = "json"
)

var fleetExample = fmt.Sprintf(`
  # Scan three clusters and print a control matrix
  %[1]s scan fleet --contexts prod,staging,dev

  # Compare every cluster against staging
  %[1]s scan fleet --contexts prod,staging --baseline staging

  # Show only the controls that differ from the baseline
  %[1]s scan fleet --contexts prod,staging --baseline staging --drift-only

  # Machine readable output for CI
  %[1]s scan fleet --contexts prod,staging --baseline staging --format json
`, cautils.ExecName())

type fleetOptions struct {
	contexts   []string
	baseline   string
	frameworks string
	driftOnly  bool
}

func getFleetCmd(ks meta.IKubescape, scanInfo *cautils.ScanInfo) *cobra.Command {
	opts := fleetOptions{}

	cmd := &cobra.Command{
		Use:     "fleet --contexts <context list> [flags]",
		Short:   "Scan several clusters and aggregate the results into one report",
		Long:    "Scan a list of kubeconfig contexts one at a time and report which clusters fail which controls, and where they differ from a baseline cluster.",
		Example: fleetExample,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			if err := validateFleetFormat(scanInfo.Format); err != nil {
				return err
			}
			return runFleetScan(cmd.Context(), cmd.OutOrStdout(), ks, scanInfo, opts)
		},
	}

	cmd.PersistentFlags().StringSliceVar(&opts.contexts, "contexts", nil, "Comma-separated kubeconfig contexts to scan, e.g. --contexts prod,staging")
	cmd.PersistentFlags().StringVar(&opts.baseline, "baseline", "", "Context to compare the other clusters against. Controls whose result differs from it are flagged as drift")
	cmd.PersistentFlags().StringVar(&opts.frameworks, "frameworks", "nsa", "Comma-separated frameworks to scan each cluster with")
	cmd.PersistentFlags().BoolVar(&opts.driftOnly, "drift-only", false, "Show only the controls that differ from the baseline. Requires --baseline")

	return cmd
}

func (o fleetOptions) validate() error {
	if len(o.contexts) == 0 {
		return fmt.Errorf("--contexts is required, e.g. --contexts prod,staging")
	}
	if len(o.contexts) < 2 {
		return fmt.Errorf("--contexts needs at least two clusters, use a normal scan for one")
	}
	if o.driftOnly && o.baseline == "" {
		return fmt.Errorf("--drift-only requires --baseline")
	}
	if o.baseline != "" && !containsContext(o.contexts, o.baseline) {
		return fmt.Errorf("--baseline %q is not in --contexts", o.baseline)
	}
	return nil
}

// validateFleetFormat checks the shared --format flag rather than defining a
// second one. A subcommand flag of the same name shadows the parent, which also
// silently drops its -f shorthand and makes fleet the only scan subcommand
// where -f fails.
func validateFleetFormat(format string) error {
	switch format {
	case "", defaultScanFormat, jsonScanFormat:
		return nil
	default:
		return fmt.Errorf("scan fleet supports --format %s or %s, got %q", defaultScanFormat, jsonScanFormat, format)
	}
}

func fleetOutputIsJSON(format string) bool {
	return format == jsonScanFormat
}

func containsContext(contexts []string, want string) bool {
	for _, context := range contexts {
		if context == want {
			return true
		}
	}
	return false
}

func runFleetScan(ctx context.Context, out io.Writer, ks meta.IKubescape, scanInfo *cautils.ScanInfo, opts fleetOptions) error {
	orchestrator := fleet.NewOrchestrator(clusterScanner(ks, scanInfo, opts.frameworks))
	orchestrator.OnClusterStart = func(kubeContext string, index, total int) {
		logger.L().Info(fmt.Sprintf("scanning cluster %d of %d", index, total), helpers.String("context", kubeContext))
	}
	orchestrator.OnClusterDone = func(snapshot fleet.ClusterSnapshot) {
		if snapshot.Err != nil {
			logger.L().Warning("cluster scan failed", helpers.String("context", snapshot.Context), helpers.Error(snapshot.Err))
		}
	}
	orchestrator.PerClusterTimeout = scanInfo.ScanTimeout

	snapshots, err := orchestrator.Run(ctx, opts.contexts)
	if err != nil {
		return err
	}

	report := fleet.Build(snapshots, opts.baseline)

	if fleetOutputIsJSON(scanInfo.Format) {
		if err := fleet.PrintJSON(out, report); err != nil {
			return err
		}
	} else {
		fleet.PrintTable(out, report, opts.driftOnly)
	}

	// A run where nothing could be scanned has produced no posture information
	// at all, so it must not look like a clean scan to CI. Partial failure is
	// left as a success for now: how much of a fleet has to be reachable before
	// the run counts is a policy question, and it is one of the open questions
	// for this project.
	if len(report.UnscannedClusters()) == len(report.Clusters) {
		return fmt.Errorf("no cluster could be scanned: %s", strings.Join(report.UnscannedClusters(), ", "))
	}
	return nil
}

// clusterScanner re-points the process at one cluster and runs a complete,
// unmodified single-cluster scan.
//
// Re-pointing is safe here only because the orchestrator is sequential. The
// context name and the loaded config are process-global in k8s-interface, so two
// of these running at once would race and could attribute a report to the wrong
// cluster.
func clusterScanner(ks meta.IKubescape, scanInfo *cautils.ScanInfo, frameworks string) fleet.ScanFunc {
	return func(_ context.Context, kubeContext string) (*v2.PostureReport, error) {
		k8sinterface.SetClusterContextName(kubeContext)
		if err := k8sinterface.LoadK8sConfig(); err != nil {
			return nil, fmt.Errorf("context %q: %w", kubeContext, err)
		}

		// ScanInfo is copied by value, so slice fields still share their backing
		// array with the caller. Init appends to UseFrom, so that one is copied
		// rather than aliased.
		perCluster := *scanInfo
		perCluster.UseFrom = append([]string(nil), scanInfo.UseFrom...)
		perCluster.SetScanType(cautils.ScanTypeFramework)
		perCluster.SetKubeconfigSelection(scanInfo.KubeconfigPath(), kubeContext)

		names := strings.Split(frameworks, ",")
		results, err := ks.Scan(&perCluster, cautils.BuildPolicyIdentifiers(names, apisv1.KindFramework))
		if err != nil {
			return nil, fmt.Errorf("context %q: %w", kubeContext, err)
		}

		data := results.GetData()
		if data == nil {
			return nil, fmt.Errorf("context %q: scan produced no data", kubeContext)
		}
		// Report.ClusterName is filled in by the printers when they finalize a
		// single-cluster report, which a fleet scan never reaches, so name the
		// cluster here from the same source they use.
		if data.Report.ClusterName == "" {
			data.Report.ClusterName = cautils.AdoptClusterName(scannedContextName(data, kubeContext))
		}
		return data.Report, nil
	}
}

func scannedContextName(data *cautils.OPASessionObj, fallback string) string {
	if data.Metadata != nil {
		if cluster := data.Metadata.ContextMetadata.ClusterContextMetadata; cluster != nil && cluster.ContextName != "" {
			return cluster.ContextName
		}
	}
	return fallback
}
