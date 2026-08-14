package evidence

import (
	"strings"

	"github.com/kubescape/k8s-interface/workloadinterface"
	"github.com/kubescape/kubescape/v3/core/cautils"
	"github.com/kubescape/opa-utils/reporthandling/results/v1/resourcesresults"
)

type Kind string

const (
	KindFailed Kind = "failed"
	KindReview Kind = "review"
	KindDelete Kind = "delete"
	KindFix    Kind = "fix"
)

type Item struct {
	Path     string
	Kind     Kind
	Value    string
	HasValue bool
	Redacted bool
	// Masked marks a value the scan replaced before reporting. It is not the
	// same as Redacted: the original is gone by the time a printer runs, so
	// --show-secrets cannot bring it back and the output has to say so rather
	// than print the placeholder as if it were the field's real content.
	Masked   bool
	FixValue string
}

type ControlEvidence struct {
	ControlID   string
	ControlName string
	Items       []Item

	// About a quarter of the rule corpus cannot produce a field path at all:
	// CIS host-file checks read host scanner data, cloud rules read a provider
	// descriptor, and rules like naked-pods fail an object for existing rather
	// than for any one field.
	NoFieldEvidence bool
}

type ResourceView struct {
	obj  map[string]any
	kind string
}

func NewResourceView(resource workloadinterface.IMetadata) *ResourceView {
	if resource == nil {
		return &ResourceView{}
	}
	return NewObjectView(resource.GetObject(), resource.GetKind())
}

func NewObjectView(obj map[string]any, kind string) *ResourceView {
	return &ResourceView{obj: obj, kind: kind}
}

func (v *ResourceView) Kind() string { return v.kind }

func (v *ResourceView) DisplayValue(path string, policy *Policy) (string, bool, bool) {
	found, ok := ResolveRawPath(v.obj, path)
	if !ok {
		return "", false, false
	}
	if policy == nil {
		policy = DefaultPolicy(false)
	}
	display, redacted, ok := policy.Redact(v.kind, path, found)
	if !ok {
		return "", false, false
	}
	// Reading the sibling "name" is right for an env entry but wrong under a
	// reference, where that name belongs to the Secret and would hide the key
	// the user needs to see.
	if isStringValue(found) && !redacted && !isReferencePath(path) &&
		policy.SensitiveSibling(v.siblingName(path)) {
		display, redacted = RedactedPlaceholder, true
	}
	return display, redacted, true
}

func (v *ResourceView) siblingName(path string) string {
	if strings.HasSuffix(path, ".name") {
		return ""
	}
	parent, ok := ParentPath(path)
	if !ok {
		return ""
	}
	found, ok := ResolveRawPath(v.obj, parent+".name")
	if !ok {
		return ""
	}
	name, _ := ScalarToString(found)
	return name
}

func Collect(view *ResourceView, control *resourcesresults.ResourceAssociatedControl, policy *Policy) ControlEvidence {
	out := ControlEvidence{
		ControlID:   control.GetID(),
		ControlName: control.GetName(),
	}
	if policy == nil {
		policy = DefaultPolicy(false)
	}
	if view == nil {
		view = &ResourceView{}
	}

	seen := make(map[string]struct{})
	add := func(rawPath string, k Kind, fixValue string) {
		path := strings.TrimLeft(TrimValue(rawPath), ".")
		if path == "" {
			return
		}
		key := string(k) + " " + path
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}

		item := Item{Path: path, Kind: k, FixValue: fixValue}
		item.Value, item.Redacted, item.HasValue = view.DisplayValue(path, policy)
		item.Masked = item.HasValue && !item.Redacted && item.Value == cautils.MaskedValue
		out.Items = append(out.Items, item)
	}

	for i := range control.ResourceAssociatedRules {
		for _, p := range control.ResourceAssociatedRules[i].Paths {
			switch {
			case p.FailedPath != "":
				add(p.FailedPath, KindFailed, "")
			case p.ReviewPath != "":
				add(p.ReviewPath, KindReview, "")
			case p.DeletePath != "":
				add(p.DeletePath, KindDelete, "")
			case p.FixPath.Path != "":
				add(p.FixPath.Path, KindFix, p.FixPath.Value)
			}
		}
	}

	out.Items = dropRedundantFailedPaths(out.Items)
	out.NoFieldEvidence = len(out.Items) == 0
	return out
}

// Rules commonly emit the same field as both a failed path and a delete or
// review path. Failed paths are the deprecated form, so the more actionable
// kind wins, matching appendFailedPathsIfNotInPaths in the resource table.
func dropRedundantFailedPaths(items []Item) []Item {
	var covered map[string]struct{}
	for _, item := range items {
		if item.Kind == KindFailed {
			continue
		}
		if covered == nil {
			covered = make(map[string]struct{}, len(items))
		}
		covered[item.Path] = struct{}{}
	}
	if covered == nil {
		return items
	}

	kept := items[:0]
	for _, item := range items {
		if item.Kind == KindFailed {
			if _, dup := covered[item.Path]; dup {
				continue
			}
		}
		kept = append(kept, item)
	}
	return kept
}
