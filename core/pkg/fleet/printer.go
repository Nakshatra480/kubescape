package fleet

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// PrintTable renders the control by cluster matrix. onlyDrift limits the rows to
// controls that differ from the baseline, which is the common case once a fleet
// grows past a handful of clusters.
func PrintTable(w io.Writer, report *FleetReport, onlyDrift bool) {
	contexts := report.Contexts()

	printClusterSummary(w, report)

	rows := report.Controls
	if onlyDrift {
		rows = report.DriftedControls()
	}
	if len(rows) == 0 {
		if onlyDrift {
			fmt.Fprintf(w, "\nNo drift from baseline %q.\n", report.Baseline)
		} else {
			fmt.Fprintf(w, "\nNo controls reported.\n")
		}
		return
	}

	header := table.Row{"Control", "Name"}
	for _, context := range contexts {
		header = append(header, context)
	}
	if report.Baseline != "" {
		header = append(header, "Drift")
	}

	matrix := table.NewWriter()
	matrix.SetOutputMirror(w)
	matrix.AppendHeader(header)
	matrix.Style().Options.SeparateRows = false
	matrix.Style().Format.Header = text.FormatDefault

	for _, row := range rows {
		line := table.Row{row.ControlID, truncate(row.Name, 40)}
		for _, context := range contexts {
			line = append(line, StatusLabel(row.Status[context]))
		}
		if report.Baseline != "" {
			line = append(line, driftMark(row.Drift))
		}
		matrix.AppendRow(line)
	}

	fmt.Fprintf(w, "\n")
	matrix.Render()

	if report.Baseline != "" {
		fmt.Fprintf(w, "\nBaseline: %s. %d of %d controls differ from it.\n",
			report.Baseline, len(report.DriftedControls()), len(report.Controls))
	}
}

func printClusterSummary(w io.Writer, report *FleetReport) {
	clusters := table.NewWriter()
	clusters.SetOutputMirror(w)
	clusters.AppendHeader(table.Row{"Context", "Cluster", "Scanned", "Compliance", "Failed", "Passed", "Skipped"})
	clusters.Style().Options.SeparateRows = false
	clusters.Style().Format.Header = text.FormatDefault

	for _, cluster := range report.Clusters {
		scanned := "yes"
		if !cluster.Scanned {
			scanned = "no"
		}
		compliance := fmt.Sprintf("%.1f%%", cluster.ComplianceScore)
		if !cluster.Scanned {
			compliance = "-"
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
