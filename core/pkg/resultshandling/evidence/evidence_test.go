package evidence

import (
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/k8s-interface/workloadinterface"
	"github.com/kubescape/kubescape/v3/core/cautils"
	"github.com/kubescape/opa-utils/reporthandling/results/v1/resourcesresults"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeResource struct {
	kind string
	obj  map[string]any
}

func (f *fakeResource) GetObject() map[string]any   { return f.obj }
func (f *fakeResource) GetWorkload() map[string]any { return f.obj }
func (f *fakeResource) GetApiVersion() string       { return "v1" }
func (f *fakeResource) GetKind() string             { return f.kind }
func (f *fakeResource) GetName() string             { return "sample" }
func (f *fakeResource) GetNamespace() string        { return "default" }
func (f *fakeResource) GetID() string               { return "id" }
func (f *fakeResource) GetObjectType() workloadinterface.ObjectType {
	return workloadinterface.TypeUnknown
}
func (f *fakeResource) SetNamespace(string)        {}
func (f *fakeResource) SetName(string)             {}
func (f *fakeResource) SetKind(string)             {}
func (f *fakeResource) SetWorkload(map[string]any) {}
func (f *fakeResource) SetObject(map[string]any)   {}
func (f *fakeResource) SetApiVersion(string)       {}

func controlWith(id, name string, rules ...resourcesresults.ResourceAssociatedRule) *resourcesresults.ResourceAssociatedControl {
	return &resourcesresults.ResourceAssociatedControl{
		ControlID:               id,
		Name:                    name,
		ResourceAssociatedRules: rules,
	}
}

// Bucket 1 of the rule survey: rules where every branch emits a real path, such
// as alert-any-hostpath.
func TestCollectAlwaysRealPaths(t *testing.T) {
	resource := &fakeResource{
		kind: "Pod",
		obj: map[string]any{
			"kind": "Pod",
			"spec": map[string]any{
				"volumes": []any{
					map[string]any{"name": "data", "hostPath": map[string]any{"path": "/etc"}},
				},
			},
		},
	}
	control := controlWith("C-0006", "HostPath mount", resourcesresults.ResourceAssociatedRule{
		Name: "alert-any-hostpath",
		Paths: []armotypes.PosturePaths{
			{FailedPath: "spec.volumes[0].hostPath.path"},
			{DeletePath: "spec.volumes[0].hostPath"},
		},
	})

	ev := Collect(NewResourceView(resource), control, DefaultPolicy(false))

	assert.False(t, ev.NoFieldEvidence)
	require.Len(t, ev.Items, 2)

	assert.Equal(t, "spec.volumes[0].hostPath.path", ev.Items[0].Path)
	assert.Equal(t, KindFailed, ev.Items[0].Kind)
	assert.True(t, ev.Items[0].HasValue)
	assert.Equal(t, "/etc", ev.Items[0].Value)

	assert.Equal(t, KindDelete, ev.Items[1].Kind)
	assert.False(t, ev.Items[1].HasValue, "the path points at a map, so there is no single value to show")
}

// Bucket 2: rules where some branches emit a path and others cannot, such as
// the kubelet checks that read a config file in one branch and a command line
// in another.
func TestCollectMixedPaths(t *testing.T) {
	resource := &fakeResource{
		kind: "Pod",
		obj: map[string]any{
			"kind": "Pod",
			"spec": map[string]any{"hostNetwork": true},
		},
	}
	control := controlWith("C-0044", "Container hostPort",
		resourcesresults.ResourceAssociatedRule{
			Name:  "resolves",
			Paths: []armotypes.PosturePaths{{ReviewPath: "spec.hostNetwork"}},
		},
		resourcesresults.ResourceAssociatedRule{
			Name:  "does-not-resolve",
			Paths: []armotypes.PosturePaths{{ReviewPath: "spec.notPresent"}},
		},
	)

	ev := Collect(NewResourceView(resource), control, DefaultPolicy(false))

	require.Len(t, ev.Items, 2)
	assert.True(t, ev.Items[0].HasValue)
	assert.Equal(t, "true", ev.Items[0].Value)
	assert.False(t, ev.Items[1].HasValue, "an absent field still names where to look")
	assert.False(t, ev.NoFieldEvidence)
}

// Bucket 3: rules that cannot produce a field path at all, such as naked-pods
// or the CIS host-file checks. Reporting this state plainly is correct output,
// not a gap to fix.
func TestCollectPlaceholderOnly(t *testing.T) {
	resource := &fakeResource{kind: "Pod", obj: map[string]any{"kind": "Pod"}}
	control := controlWith("C-0073", "Naked pods", resourcesresults.ResourceAssociatedRule{
		Name:  "naked-pods",
		Paths: nil,
	})

	ev := Collect(NewResourceView(resource), control, DefaultPolicy(false))

	assert.True(t, ev.NoFieldEvidence)
	assert.Empty(t, ev.Items)
	assert.Equal(t, "C-0073", ev.ControlID)
}

func TestCollectDedupesRepeatedPaths(t *testing.T) {
	resource := &fakeResource{
		kind: "Pod",
		obj:  map[string]any{"kind": "Pod", "spec": map[string]any{"hostPID": true}},
	}
	// Two rules back this control and both name the same field.
	control := controlWith("C-0038", "Host PID",
		resourcesresults.ResourceAssociatedRule{
			Name:  "rule-a",
			Paths: []armotypes.PosturePaths{{FailedPath: "spec.hostPID"}},
		},
		resourcesresults.ResourceAssociatedRule{
			Name:  "rule-b",
			Paths: []armotypes.PosturePaths{{FailedPath: "spec.hostPID"}},
		},
	)

	ev := Collect(NewResourceView(resource), control, DefaultPolicy(false))

	require.Len(t, ev.Items, 1)
	assert.Equal(t, "spec.hostPID", ev.Items[0].Path)
}

// Rules routinely emit the same field as both a failed path and a review or
// delete path. Failed paths are the deprecated form, so listing both would show
// the user the same field twice; the more actionable kind wins.
func TestCollectDropsFailedPathCoveredByAnotherKind(t *testing.T) {
	resource := &fakeResource{
		kind: "Pod",
		obj:  map[string]any{"kind": "Pod", "spec": map[string]any{"hostPID": true}},
	}
	control := controlWith("C-0038", "Host PID", resourcesresults.ResourceAssociatedRule{
		Name: "rule",
		Paths: []armotypes.PosturePaths{
			{FailedPath: "spec.hostPID"},
			{ReviewPath: "spec.hostPID"},
		},
	})

	ev := Collect(NewResourceView(resource), control, DefaultPolicy(false))

	require.Len(t, ev.Items, 1)
	assert.Equal(t, KindReview, ev.Items[0].Kind)
	assert.Equal(t, "spec.hostPID", ev.Items[0].Path)
}

// A failed path nothing else covers is still reported.
func TestCollectKeepsUncoveredFailedPath(t *testing.T) {
	resource := &fakeResource{
		kind: "Pod",
		obj:  map[string]any{"kind": "Pod", "spec": map[string]any{"hostPID": true, "hostIPC": true}},
	}
	control := controlWith("C-0038", "Host PID", resourcesresults.ResourceAssociatedRule{
		Name: "rule",
		Paths: []armotypes.PosturePaths{
			{FailedPath: "spec.hostPID"},
			{ReviewPath: "spec.hostIPC"},
		},
	})

	ev := Collect(NewResourceView(resource), control, DefaultPolicy(false))

	require.Len(t, ev.Items, 2)
	assert.Equal(t, KindFailed, ev.Items[0].Kind)
	assert.Equal(t, KindReview, ev.Items[1].Kind)
}

func TestCollectCarriesFixValue(t *testing.T) {
	resource := &fakeResource{kind: "Pod", obj: map[string]any{"kind": "Pod", "spec": map[string]any{}}}
	control := controlWith("C-0017", "Immutable filesystem", resourcesresults.ResourceAssociatedRule{
		Name: "rule",
		Paths: []armotypes.PosturePaths{
			{FixPath: armotypes.FixPath{Path: "spec.containers[0].securityContext.readOnlyRootFilesystem", Value: "true"}},
		},
	})

	ev := Collect(NewResourceView(resource), control, DefaultPolicy(false))

	require.Len(t, ev.Items, 1)
	assert.Equal(t, KindFix, ev.Items[0].Kind)
	assert.Equal(t, "true", ev.Items[0].FixValue)
	assert.False(t, ev.Items[0].HasValue, "a fix path names a field that is not set yet")
}

func TestCollectRedactsThroughThePolicy(t *testing.T) {
	resource := &fakeResource{kind: "Deployment", obj: deploymentWithPlaintextCredential()}
	control := controlWith("C-0012", "Applications credentials in configuration files",
		resourcesresults.ResourceAssociatedRule{
			Name: "rule-credentials-in-env-var",
			Paths: []armotypes.PosturePaths{
				{FailedPath: "spec.template.spec.containers[0].env[0].name"},
				{FailedPath: "spec.template.spec.containers[0].env[0].value"},
			},
		})

	ev := Collect(NewResourceView(resource), control, DefaultPolicy(false))

	require.Len(t, ev.Items, 2)
	assert.Equal(t, "DB_PASSWORD", ev.Items[0].Value)
	assert.True(t, ev.Items[1].Redacted)
	assert.NotContains(t, ev.Items[1].Value, plaintextCredential)
}

func TestCollectHandlesNilPolicyAndResource(t *testing.T) {
	control := controlWith("C-0001", "Some control", resourcesresults.ResourceAssociatedRule{
		Name:  "rule",
		Paths: []armotypes.PosturePaths{{FailedPath: "spec.hostPID"}},
	})

	ev := Collect(nil, control, nil)

	require.Len(t, ev.Items, 1)
	assert.False(t, ev.Items[0].HasValue)
}

// A value the scan replaced before reporting is not the same as one the policy
// hid: the original no longer exists, so the output must not present the
// placeholder as the field's content.
func TestCollectMarksValuesMaskedByTheScan(t *testing.T) {
	resource := &fakeResource{
		kind: "Deployment",
		obj: map[string]any{
			"kind": "Deployment",
			"spec": map[string]any{
				"containers": []any{map[string]any{
					"name": "api",
					"env":  []any{map[string]any{"name": "DB_PASSWORD", "value": cautils.MaskedValue}},
				}},
			},
		},
	}
	control := controlWith("C-0012", "Applications credentials in configuration files",
		resourcesresults.ResourceAssociatedRule{
			Name:  "rule-credentials-in-env-var",
			Paths: []armotypes.PosturePaths{{FailedPath: "spec.containers[0].env[0].value"}},
		})

	t.Run("policy hides it by default", func(t *testing.T) {
		ev := Collect(NewResourceView(resource), control, DefaultPolicy(false))
		require.Len(t, ev.Items, 1)
		assert.True(t, ev.Items[0].Redacted)
		assert.False(t, ev.Items[0].Masked)
	})

	t.Run("show-secrets reports it as masked, not as a value", func(t *testing.T) {
		ev := Collect(NewResourceView(resource), control, DefaultPolicy(true))
		require.Len(t, ev.Items, 1)
		assert.False(t, ev.Items[0].Redacted)
		assert.True(t, ev.Items[0].Masked, "--show-secrets cannot recover a value the scan destroyed")
	})
}
