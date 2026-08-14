package printer

import (
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/k8s-interface/workloadinterface"
	"github.com/kubescape/kubescape/v3/core/pkg/resultshandling/evidence"
	"github.com/kubescape/opa-utils/reporthandling/results/v1/resourcesresults"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnrichedPathsForField(t *testing.T) {
	deploymentResource := &mockResource{
		kind: "Deployment",
		obj: map[string]any{
			"spec": map[string]any{
				"hostIPC": true,
			},
		},
	}
	ctrl := &resourcesresults.ResourceAssociatedControl{
		ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{
			{
				Paths: []armotypes.PosturePaths{
					{FailedPath: "spec.hostIPC"},
					{ReviewPath: "spec.hostIPC"},
				},
			},
		},
	}

	t.Run("getPath selects FailedPath", func(t *testing.T) {
		got := enrichedPathsForField(ctrl, evidence.NewResourceView(deploymentResource), evidence.DefaultPolicy(false), func(p armotypes.PosturePaths) string { return p.FailedPath })
		require.Len(t, got, 1)
		assert.Equal(t, "spec.hostIPC (current: true)", got[0])
	})

	t.Run("getPath selects ReviewPath", func(t *testing.T) {
		got := enrichedPathsForField(ctrl, evidence.NewResourceView(deploymentResource), evidence.DefaultPolicy(false), func(p armotypes.PosturePaths) string { return p.ReviewPath })
		require.Len(t, got, 1)
		assert.Equal(t, "spec.hostIPC (current: true)", got[0])
	})

	t.Run("empty obj produces bare path", func(t *testing.T) {
		emptyResource := &mockResource{kind: "Deployment", obj: map[string]any{}}
		got := enrichedPathsForField(ctrl, evidence.NewResourceView(emptyResource), evidence.DefaultPolicy(false), func(p armotypes.PosturePaths) string { return p.FailedPath })
		require.Len(t, got, 1)
		assert.Equal(t, "spec.hostIPC", got[0])
	})

	t.Run("Secret data path is suppressed", func(t *testing.T) {
		secretResource := &mockResource{
			kind: "Secret",
			obj: map[string]any{
				"data": map[string]any{
					"password": "XXXXXX",
				},
			},
		}
		secretCtrl := &resourcesresults.ResourceAssociatedControl{
			ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{
				{Paths: []armotypes.PosturePaths{{FailedPath: "data.password"}}},
			},
		}
		got := enrichedPathsForField(secretCtrl, evidence.NewResourceView(secretResource), evidence.DefaultPolicy(false), func(p armotypes.PosturePaths) string { return p.FailedPath })
		require.Len(t, got, 1)
		assert.Equal(t, "data.password", got[0])
	})
}

func makeControlWithPaths(failedPaths, reviewPaths []string) *resourcesresults.ResourceAssociatedControl {
	var posturePaths []armotypes.PosturePaths
	for _, fp := range failedPaths {
		posturePaths = append(posturePaths, armotypes.PosturePaths{FailedPath: fp})
	}
	for _, rp := range reviewPaths {
		posturePaths = append(posturePaths, armotypes.PosturePaths{ReviewPath: rp})
	}
	return &resourcesresults.ResourceAssociatedControl{
		ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{
			{Paths: posturePaths},
		},
	}
}

type mockResource struct {
	kind string
	obj  map[string]any
}

func (m *mockResource) GetObject() map[string]any   { return m.obj }
func (m *mockResource) GetApiVersion() string       { return "" }
func (m *mockResource) GetKind() string             { return m.kind }
func (m *mockResource) GetName() string             { return "" }
func (m *mockResource) GetNamespace() string        { return "" }
func (m *mockResource) GetID() string               { return "" }
func (m *mockResource) GetWorkload() map[string]any { return m.obj }
func (m *mockResource) GetObjectType() workloadinterface.ObjectType {
	return workloadinterface.TypeUnknown
}

func (m *mockResource) SetNamespace(string)                {}
func (m *mockResource) SetName(string)                     {}
func (m *mockResource) SetKind(string)                     {}
func (m *mockResource) SetWorkload(map[string]interface{}) {}
func (m *mockResource) SetObject(map[string]interface{})   {}
func (m *mockResource) SetApiVersion(string)               {}

func TestFailedPathsWithCurrentValues(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"hostNetwork": true,
			"containers": []any{
				map[string]any{
					"securityContext": map[string]any{
						"privileged": true,
					},
				},
			},
		},
	}
	resource := &mockResource{obj: obj}

	t.Run("value extracted", func(t *testing.T) {
		ctrl := makeControlWithPaths([]string{"spec.containers[0].securityContext.privileged"}, nil)
		got := failedPathsWithCurrentValues(ctrl, evidence.NewResourceView(resource), evidence.DefaultPolicy(false))
		require.Len(t, got, 1)
		assert.Equal(t, "spec.containers[0].securityContext.privileged (current: true)", got[0])
	})

	t.Run("missing path falls back to bare path", func(t *testing.T) {
		ctrl := makeControlWithPaths([]string{"spec.containers[0].securityContext.readOnlyRootFilesystem"}, nil)
		got := failedPathsWithCurrentValues(ctrl, evidence.NewResourceView(resource), evidence.DefaultPolicy(false))
		require.Len(t, got, 1)
		assert.Equal(t, "spec.containers[0].securityContext.readOnlyRootFilesystem", got[0])
	})

	t.Run("multiple paths", func(t *testing.T) {
		ctrl := makeControlWithPaths([]string{
			"spec.hostNetwork",
			"spec.containers[0].securityContext.privileged",
		}, nil)
		got := failedPathsWithCurrentValues(ctrl, evidence.NewResourceView(resource), evidence.DefaultPolicy(false))
		require.Len(t, got, 2)
		assert.Equal(t, "spec.hostNetwork (current: true)", got[0])
		assert.Equal(t, "spec.containers[0].securityContext.privileged (current: true)", got[1])
	})

	t.Run("no failed paths returns nil", func(t *testing.T) {
		ctrl := makeControlWithPaths(nil, nil)
		got := failedPathsWithCurrentValues(ctrl, evidence.NewResourceView(resource), evidence.DefaultPolicy(false))
		assert.Nil(t, got)
	})
}

func TestReviewPathsWithCurrentValues(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"automountServiceAccountToken": true,
		},
	}
	resource := &mockResource{obj: obj}

	t.Run("value extracted", func(t *testing.T) {
		ctrl := makeControlWithPaths(nil, []string{"spec.automountServiceAccountToken"})
		got := reviewPathsWithCurrentValues(ctrl, evidence.NewResourceView(resource), evidence.DefaultPolicy(false))
		require.Len(t, got, 1)
		assert.Equal(t, "spec.automountServiceAccountToken (current: true)", got[0])
	})

	t.Run("missing path falls back", func(t *testing.T) {
		ctrl := makeControlWithPaths(nil, []string{"spec.serviceAccountName"})
		got := reviewPathsWithCurrentValues(ctrl, evidence.NewResourceView(resource), evidence.DefaultPolicy(false))
		require.Len(t, got, 1)
		assert.Equal(t, "spec.serviceAccountName", got[0])
	})
}

func TestAssistedRemediationPathsWithCurrentValues(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"hostPID": true,
		},
	}
	resource := &mockResource{obj: obj}

	t.Run("failed path annotated, fix path unchanged", func(t *testing.T) {
		ctrl := &resourcesresults.ResourceAssociatedControl{
			ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{
				{
					Paths: []armotypes.PosturePaths{
						{FailedPath: "spec.hostPID"},
						{FixPath: armotypes.FixPath{Path: "spec.hostPID", Value: "false"}},
					},
				},
			},
		}
		got := AssistedRemediationPathsWithCurrentValues(ctrl, evidence.NewResourceView(resource), evidence.DefaultPolicy(false))
		assert.Contains(t, got, "spec.hostPID=false")
		assert.Contains(t, got, "spec.hostPID (current: true)")
		assert.Len(t, got, 2)
	})
}
