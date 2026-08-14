package printer

import (
	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/kubescape/v3/core/pkg/resultshandling/evidence"
	"github.com/kubescape/opa-utils/reporthandling/results/v1/resourcesresults"
)

// Path resolution and redaction live in core/pkg/resultshandling/evidence so
// that the pretty, HTML and SARIF printers derive evidence the same way.

func enrichedPathsForField(control *resourcesresults.ResourceAssociatedControl, view *evidence.ResourceView, policy *evidence.Policy, getPath func(armotypes.PosturePaths) string) []string {
	var paths []string
	kind := view.Kind()
	for j := range control.ResourceAssociatedRules {
		for k := range control.ResourceAssociatedRules[j].Paths {
			p := getPath(control.ResourceAssociatedRules[j].Paths[k])
			if p == "" {
				continue
			}
			// Secret data keeps rendering as a bare path rather than as a
			// redacted value, so this output is unchanged from before.
			if !evidence.AlwaysWithheld(kind, p) {
				if val, _, ok := view.DisplayValue(p, policy); ok {
					paths = append(paths, p+" (current: "+val+")")
					continue
				}
			}
			paths = append(paths, p)
		}
	}
	return paths
}

func failedPathsWithCurrentValues(control *resourcesresults.ResourceAssociatedControl, view *evidence.ResourceView, policy *evidence.Policy) []string {
	return enrichedPathsForField(control, view, policy, func(p armotypes.PosturePaths) string { return p.FailedPath })
}

func reviewPathsWithCurrentValues(control *resourcesresults.ResourceAssociatedControl, view *evidence.ResourceView, policy *evidence.Policy) []string {
	return enrichedPathsForField(control, view, policy, func(p armotypes.PosturePaths) string { return p.ReviewPath })
}

func AssistedRemediationPathsWithCurrentValues(control *resourcesresults.ResourceAssociatedControl, view *evidence.ResourceView, policy *evidence.Policy) []string {
	paths := append(fixPathsToString(control, false), append(deletePathsToString(control), reviewPathsWithCurrentValues(control, view, policy)...)...)
	return appendFailedPathsIfNotInPaths(paths, failedPathsWithCurrentValues(control, view, policy))
}
