package printer

import (
	"github.com/kubescape/kubescape/v3/core/cautils"
	"github.com/kubescape/kubescape/v3/core/pkg/resultshandling/evidence"
)

// Evidence is attached to the report the JSON printer owns rather than to the
// report object itself, for the same reason severity, labels and scan coverage
// are: the session object is submitted to the backend after local output is
// written, so enriching it would make --format change the uploaded payload.
type ResourceEvidence struct {
	ResourceID string            `json:"resourceID"`
	Kind       string            `json:"kind"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	Controls   []ControlEvidence `json:"controls"`
}

type ControlEvidence struct {
	ControlID string         `json:"controlID"`
	Name      string         `json:"name"`
	Items     []EvidenceItem `json:"items,omitempty"`
	// NoFieldEvidence marks a control that cannot name a field, such as a CIS
	// host check or a rule that fails an object for existing.
	NoFieldEvidence bool `json:"noFieldEvidence,omitempty"`
}

type EvidenceItem struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Value    string `json:"value,omitempty"`
	FixValue string `json:"fixValue,omitempty"`
	// Redacted means the value was withheld and --show-secrets would reveal it.
	Redacted bool `json:"redacted,omitempty"`
	// Masked means the scan replaced the value before reporting, so nothing can
	// reveal it.
	Masked bool `json:"masked,omitempty"`
}

// buildEvidenceReport resolves evidence for every failed resource. It is only
// called when --show-evidence is set, so the default JSON output is unchanged.
func buildEvidenceReport(opaSessionObj *cautils.OPASessionObj, showSecrets bool) []ResourceEvidence {
	blocks := collectEvidenceBlocks(opaSessionObj)
	if len(blocks) == 0 {
		return nil
	}
	policy := policyForSession(opaSessionObj, showSecrets)

	out := make([]ResourceEvidence, 0, len(blocks))
	for i := range blocks {
		view := evidence.NewResourceView(blocks[i].resource)
		controls := make([]ControlEvidence, 0, len(blocks[i].controls))
		for j := range blocks[i].controls {
			controls = append(controls, toControlEvidence(evidence.Collect(view, &blocks[i].controls[j], policy)))
		}
		out = append(out, ResourceEvidence{
			ResourceID: blocks[i].id,
			Kind:       blocks[i].resource.GetKind(),
			Namespace:  blocks[i].resource.GetNamespace(),
			Name:       blocks[i].resource.GetName(),
			Controls:   controls,
		})
	}
	return out
}

func toControlEvidence(ev evidence.ControlEvidence) ControlEvidence {
	out := ControlEvidence{
		ControlID:       ev.ControlID,
		Name:            ev.ControlName,
		NoFieldEvidence: ev.NoFieldEvidence,
	}
	for i := range ev.Items {
		item := ev.Items[i]
		out.Items = append(out.Items, EvidenceItem{
			Path:     item.Path,
			Kind:     string(item.Kind),
			Value:    evidenceReportValue(item),
			FixValue: item.FixValue,
			Redacted: item.Redacted,
			Masked:   item.Masked,
		})
	}
	return out
}

// A path that does not resolve carries no value at all, which a consumer reads
// as "the field is not set" rather than as an empty string.
func evidenceReportValue(item evidence.Item) string {
	if !item.HasValue {
		return ""
	}
	if item.Masked {
		return maskedPlaceholder
	}
	return item.Value
}
