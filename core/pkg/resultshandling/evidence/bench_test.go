package evidence

import (
	"fmt"
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	corev1 "k8s.io/api/core/v1"

	"github.com/kubescape/opa-utils/reporthandling/results/v1/resourcesresults"
)

// realisticWorkload mirrors what removeData leaves behind: containers written
// back as a typed slice under a plain map.
func realisticWorkload(containers, envPerContainer int) map[string]any {
	list := make([]corev1.Container, 0, containers)
	for i := range containers {
		env := make([]corev1.EnvVar, 0, envPerContainer)
		for j := 0; j < envPerContainer; j++ {
			env = append(env, corev1.EnvVar{Name: fmt.Sprintf("VAR_%d", j), Value: "XXXXXX"})
		}
		list = append(list, corev1.Container{
			Name:  fmt.Sprintf("c%d", i),
			Image: "nginx:1.25",
			Env:   env,
		})
	}
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "billing", "namespace": "prod"},
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{"containers": list},
			},
		},
	}
}

func benchControl(id string) *resourcesresults.ResourceAssociatedControl {
	return &resourcesresults.ResourceAssociatedControl{
		ControlID: id,
		Name:      "bench",
		ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{{
			Name: "rule",
			Paths: []armotypes.PosturePaths{
				{FailedPath: "spec.template.spec.containers[0].env[0].value"},
			},
		}},
	}
}

// One resource failing N controls is the normal case; this measures how the
// cost scales with N.
func BenchmarkCollectAcrossControls(b *testing.B) {
	obj := realisticWorkload(5, 20)
	resource := &fakeResource{kind: "Deployment", obj: obj}
	policy := DefaultPolicy(false)

	controls := make([]*resourcesresults.ResourceAssociatedControl, 20)
	for i := range controls {
		controls[i] = benchControl(fmt.Sprintf("C-%04d", i))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// One view per resource, reused across every control that failed it.
		view := NewResourceView(resource)
		for _, c := range controls {
			_ = Collect(view, c, policy)
		}
	}
}

func BenchmarkResolveTypedPath(b *testing.B) {
	obj := realisticWorkload(5, 20)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ResolveRawPath(obj, "spec.template.spec.containers[4].env[19].value")
	}
}
