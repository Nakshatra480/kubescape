package printer

import (
	"sort"
	"strings"

	"github.com/kubescape/k8s-interface/workloadinterface"
	"github.com/kubescape/kubescape/v3/core/cautils"
	"github.com/kubescape/kubescape/v3/core/pkg/resultshandling/evidence"
	"github.com/kubescape/opa-utils/reporthandling/results/v1/resourcesresults"
)

// Paths run long, so the value column stops aligning past this width rather
// than pushing every line off a normal terminal.
const (
	maxEvidencePathWidth = 64
	maskedPlaceholder    = "<hidden by scan>"

	// A failed resource costs roughly twenty lines of evidence, so a cluster
	// scan would otherwise bury the summary under thousands of them. Verbose
	// mode lifts the limit, matching how the rest of the printer treats volume.
	defaultEvidenceResourceLimit = 20
)

func policyForSession(opaSessionObj *cautils.OPASessionObj, reveal bool) *evidence.Policy {
	if opaSessionObj == nil {
		return evidence.DefaultPolicy(reveal)
	}
	return evidence.NewPolicy(opaSessionObj.RegoInputData.PostureControlInputs, reveal)
}

type evidenceBlock struct {
	title    string
	id       string
	resource workloadinterface.IMetadata
	controls []resourcesresults.ResourceAssociatedControl
}

// Runs independently of --view: the paths are otherwise reachable only through
// "--view resource --verbose", which is why a scan says a control failed but
// not why (#1563).
func (pp *PrettyPrinter) printEvidence(opaSessionObj *cautils.OPASessionObj) {
	blocks := collectEvidenceBlocks(opaSessionObj)
	if len(blocks) == 0 {
		return
	}

	policy := policyForSession(opaSessionObj, pp.showSecrets)

	shown := blocks
	if !pp.verboseMode && len(shown) > defaultEvidenceResourceLimit {
		shown = shown[:defaultEvidenceResourceLimit]
	}

	cautils.InfoDisplay(pp.writer, "\nEvidence:\n")
	var anyRedacted, anyMasked bool
	for i := range shown {
		cautils.SimpleDisplay(pp.writer, "\n  %s\n", shown[i].title)
		view := evidence.NewResourceView(shown[i].resource)
		for j := range shown[i].controls {
			ev := evidence.Collect(view, &shown[i].controls[j], policy)
			for k := range ev.Items {
				anyRedacted = anyRedacted || ev.Items[k].Redacted
				anyMasked = anyMasked || ev.Items[k].Masked
			}
			pp.printControlEvidence(ev)
		}
	}

	if remaining := len(blocks) - len(shown); remaining > 0 {
		cautils.SimpleDisplay(pp.writer, "\n  %d more failed resources have evidence. Use --verbose to show them all.\n", remaining)
	}

	if anyRedacted {
		cautils.SimpleDisplay(pp.writer, "\n  %s: hidden because the value looks like a credential. Use --show-secrets to reveal.\n", evidence.RedactedPlaceholder)
	}
	if anyMasked {
		cautils.SimpleDisplay(pp.writer, "\n  %s: replaced by the scan before reporting. The original is not retained, so no flag can show it.\n", maskedPlaceholder)
	}
}

func collectEvidenceBlocks(opaSessionObj *cautils.OPASessionObj) []evidenceBlock {
	blocks := make([]evidenceBlock, 0, len(opaSessionObj.ResourcesResult))
	for resourceID, result := range opaSessionObj.ResourcesResult {
		if !result.GetStatus(nil).IsFailed() {
			continue
		}
		resource, ok := opaSessionObj.AllResources[resourceID]
		if !ok {
			continue
		}
		var failed []resourcesresults.ResourceAssociatedControl
		for _, ctl := range result.ListControls() {
			if ctl.GetStatus(nil).IsFailed() {
				failed = append(failed, ctl)
			}
		}
		if len(failed) == 0 {
			continue
		}
		sort.Slice(failed, func(i, j int) bool { return failed[i].GetID() < failed[j].GetID() })
		blocks = append(blocks, evidenceBlock{
			title:    resourceTitle(resource),
			id:       resourceID,
			resource: resource,
			controls: failed,
		})
	}

	// ResourcesResult is a map, and a title is not unique on its own, so the id
	// breaks ties to keep the output stable across runs.
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].title != blocks[j].title {
			return blocks[i].title < blocks[j].title
		}
		return blocks[i].id < blocks[j].id
	})
	return blocks
}

func (pp *PrettyPrinter) printControlEvidence(ev evidence.ControlEvidence) {
	cautils.SimpleDisplay(pp.writer, "    %s %s\n", ev.ControlID, ev.ControlName)

	if ev.NoFieldEvidence {
		cautils.SimpleDisplay(pp.writer, "      no field-level evidence for this control\n")
		return
	}

	width := 0
	for i := range ev.Items {
		if n := len(ev.Items[i].Path); n > width && n <= maxEvidencePathWidth {
			width = n
		}
	}

	for i := range ev.Items {
		item := ev.Items[i]
		cautils.SimpleDisplay(pp.writer, "      %-*s  %s\n", width, item.Path, evidenceValue(item))
		if item.Kind == evidence.KindFix && item.FixValue != "" {
			// A path longer than the column width pushes its own value right,
			// so the continuation follows that path rather than the column.
			cautils.SimpleDisplay(pp.writer, "      %*s  expected: %s\n", max(width, len(item.Path)), "", item.FixValue)
		}
	}
}

func evidenceValue(item evidence.Item) string {
	switch {
	case item.Masked:
		return maskedPlaceholder
	case item.HasValue:
		return item.Value
	case item.Kind == evidence.KindDelete:
		return "(to remove)"
	default:
		return "(not set)"
	}
}

func resourceTitle(resource workloadinterface.IMetadata) string {
	parts := []string{resource.GetKind()}
	if ns := resource.GetNamespace(); ns != "" {
		parts = append(parts, ns)
	}
	return strings.Join(append(parts, resource.GetName()), "/")
}
