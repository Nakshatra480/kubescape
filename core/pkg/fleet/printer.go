package fleet

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// maxControlNameWidth keeps the matrix readable once a fleet has more than a
// couple of clusters, since every extra cluster adds a column.
const maxControlNameWidth = 40

// PrintTable renders the cluster summary followed by the control by cluster
// matrix. onlyDrift limits the matrix to controls that differ from the baseline,
// which is the common case once a fleet grows past a handful of clusters.
func PrintTable(w io.Writer, report *FleetReport, onlyDrift bool) {
	printClusterSummary(w, report)

	rows := report.Controls
	if onlyDrift {
		rows = report.DriftedControls()
	}
	if len(rows) == 0 {
		fmt.Fprintf(w, "\n%s\n", emptyMatrixReason(report, onlyDrift))
		return
	}

	printMatrix(w, report, rows)

	if report.Baseline != "" {
		fmt.Fprintf(w, "\nBaseline: %s. %d of %d controls differ from it.\n",
			report.Baseline, len(report.DriftedControls()), len(report.Controls))
	}
}

func emptyMatrixReason(report *FleetReport, onlyDrift bool) string {
	switch {
	case onlyDrift:
		return fmt.Sprintf("No drift from baseline %q.", report.Baseline)
	case len(report.UnscannedClusters()) == len(report.Clusters):
		return "No controls reported: no cluster was scanned."
	default:
		return "No controls reported."
	}
}

func printClusterSummary(w io.Writer, report *FleetReport) {
	clusters := newTable(w, table.Row{"Context", "Cluster", "Scanned", "Compliance", "Failed", "Passed", "Skipped"})

	for _, cluster := range report.Clusters {
		scanned, compliance := "yes", fmt.Sprintf("%.1f%%", cluster.ComplianceScore)
		if !cluster.Scanned {
			scanned, compliance = "no", "-"
		}
		clusters.AppendRow(table.Row{
			cluster.Context, cluster.ClusterName, scanned, compliance,
			cluster.Failed, cluster.Passed, cluster.Skipped,
		})
	}
	clusters.Render()

	for _, cluster := range report.Clusters {
		if !cluster.Scanned {
			fmt.Fprintf(w, "%s was not scanned: %s\n", cluster.Context, cluster.Error)
		}
	}
}

func printMatrix(w io.Writer, report *FleetReport, rows []ControlRow) {
	contexts := report.Contexts()

	header := table.Row{"Control", "Name"}
	for _, kubeContext := range contexts {
		header = append(header, kubeContext)
	}
	if report.Baseline != "" {
		header = append(header, "Drift")
	}

	fmt.Fprintf(w, "\n")
	matrix := newTable(w, header)

	for _, row := range rows {
		line := table.Row{row.ControlID, truncate(row.Name, maxControlNameWidth)}
		for _, kubeContext := range contexts {
			line = append(line, StatusLabel(row.Status[kubeContext]))
		}
		if report.Baseline != "" {
			line = append(line, driftMark(row.Drift))
		}
		matrix.AppendRow(line)
	}
	matrix.Render()
}

func newTable(w io.Writer, header table.Row) table.Writer {
	t := table.NewWriter()
	t.SetOutputMirror(w)
	t.AppendHeader(header)
	t.Style().Options.SeparateRows = false
	t.Style().Format.Header = text.FormatDefault
	return t
}

// PrintJSON writes the fleet report for CI and further processing.
func PrintJSON(w io.Writer, report *FleetReport) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func driftMark(drift bool) string {
	if drift {
		return "yes"
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
