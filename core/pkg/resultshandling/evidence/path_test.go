package evidence

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestSplitPath(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []pathSegment
	}{
		{
			name:  "simple key",
			input: "apiVersion",
			want:  []pathSegment{{key: "apiVersion", index: -1}},
		},
		{
			name:  "dotted path",
			input: "spec.securityContext.runAsNonRoot",
			want: []pathSegment{
				{key: "spec", index: -1},
				{key: "securityContext", index: -1},
				{key: "runAsNonRoot", index: -1},
			},
		},
		{
			name:  "array index",
			input: "spec.containers[0].image",
			want: []pathSegment{
				{key: "spec", index: -1},
				{key: "containers", index: 0},
				{key: "image", index: -1},
			},
		},
		{
			name:  "second array element",
			input: "spec.containers[2].securityContext.privileged",
			want: []pathSegment{
				{key: "spec", index: -1},
				{key: "containers", index: 2},
				{key: "securityContext", index: -1},
				{key: "privileged", index: -1},
			},
		},
		{
			name:  "strip leading dot",
			input: ".spec.nodeName",
			want: []pathSegment{
				{key: "spec", index: -1},
				{key: "nodeName", index: -1},
			},
		},
		{
			name:  "strip = suffix (failed path format)",
			input: "spec.containers[0].securityContext.privileged=true",
			want: []pathSegment{
				{key: "spec", index: -1},
				{key: "containers", index: 0},
				{key: "securityContext", index: -1},
				{key: "privileged", index: -1},
			},
		},
		{
			name:  "empty path",
			input: "",
			want:  nil,
		},
		{
			name:  "empty segments from double dot",
			input: "spec..image",
			want: []pathSegment{
				{key: "spec", index: -1},
				{key: "image", index: -1},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitPath(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolvePath(t *testing.T) {
	obj := map[string]any{
		"kind": "Deployment",
		"spec": map[string]any{
			"replicas": float64(3),
			"paused":   false,
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "api",
							"image": "nginx:1.25",
							"securityContext": map[string]any{
								"privileged": true,
							},
						},
					},
				},
			},
		},
	}

	cases := []struct {
		name   string
		path   string
		want   string
		wantOK bool
	}{
		{name: "nested bool", path: "spec.template.spec.containers[0].securityContext.privileged", want: "true", wantOK: true},
		{name: "nested string", path: "spec.template.spec.containers[0].image", want: "nginx:1.25", wantOK: true},
		{name: "whole number renders without decimal", path: "spec.replicas", want: "3", wantOK: true},
		{name: "false bool", path: "spec.paused", want: "false", wantOK: true},
		{name: "strips = suffix", path: "spec.replicas=5", want: "3", wantOK: true},
		{name: "missing key", path: "spec.missing", wantOK: false},
		{name: "index out of range", path: "spec.template.spec.containers[7].image", wantOK: false},
		{name: "path into a map is not a scalar", path: "spec.template", wantOK: false},
		{name: "empty path", path: "", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolvePath(obj, tc.path)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestResolveRawPathKeepsType(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"automountServiceAccountToken": false,
			"serviceAccountName":           "builder",
		},
	}

	raw, ok := ResolveRawPath(obj, "spec.automountServiceAccountToken")
	assert.True(t, ok)
	assert.IsType(t, false, raw, "bool must survive resolution so redaction can skip it")

	raw, ok = ResolveRawPath(obj, "spec.serviceAccountName")
	assert.True(t, ok)
	assert.IsType(t, "", raw)
}

func TestLastKeyAndParentPath(t *testing.T) {
	assert.Equal(t, "value", LastKey("spec.containers[0].env[1].value"))
	assert.Equal(t, "containers", LastKey("spec.containers[0]"))
	assert.Equal(t, "", LastKey(""))

	parent, ok := ParentPath("spec.containers[0].env[1].value")
	assert.True(t, ok)
	assert.Equal(t, "spec.containers[0].env[1]", parent)

	_, ok = ParentPath("spec")
	assert.False(t, ok)
}

func TestTrimValueSplitsOnFirstEquals(t *testing.T) {
	// CIS control-plane rules emit fix values that themselves contain "=".
	assert.Equal(t, "spec.command[0]", TrimValue("spec.command[0]=--anonymous-auth=false"))
	assert.Equal(t, "spec.replicas", TrimValue("spec.replicas"))
}

// Resources that reach a printer carry typed Kubernetes values: removeData
// writes containers back as []corev1.Container. A walker that only understands
// map[string]any and []any resolves nothing under any subscripted path, which is
// how container-level evidence went missing on file scans.
func TestResolveWalksTypedKubernetesValues(t *testing.T) {
	obj := map[string]any{
		"kind": "Deployment",
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []corev1.Container{{
						Name:            "api",
						Image:           "nginx:1.25",
						ImagePullPolicy: corev1.PullAlways,
						Env:             []corev1.EnvVar{{Name: "LOG_LEVEL", Value: "debug"}},
					}},
				},
			},
		},
	}

	view := NewObjectView(obj, "Deployment")

	for _, tc := range []struct{ path, want string }{
		{"spec.template.spec.containers[0].name", "api"},
		{"spec.template.spec.containers[0].image", "nginx:1.25"},
		{"spec.template.spec.containers[0].env[0].value", "debug"},
		// corev1.PullPolicy is a named string type, which a plain type switch on
		// string does not match.
		{"spec.template.spec.containers[0].imagePullPolicy", "Always"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, _, ok := view.DisplayValue(tc.path, DefaultPolicy(false))
			require.True(t, ok, "path must resolve through typed values")
			assert.Equal(t, tc.want, got)
		})
	}
}

// Walking typed values must not copy the object. An earlier version marshalled
// the whole resource through JSON on every lookup, which cost tens of kilobytes
// per control on every failed resource.
func TestResolveTypedValuesDoesNotCopyTheObject(t *testing.T) {
	obj := map[string]any{
		"kind": "Pod",
		"spec": map[string]any{
			"containers": []corev1.Container{{
				Name: "api",
				Env:  []corev1.EnvVar{{Name: "LOG_LEVEL", Value: "debug"}},
			}},
		},
	}

	allocs := testing.AllocsPerRun(100, func() {
		ResolveRawPath(obj, "spec.containers[0].env[0].value")
	})

	assert.LessOrEqual(t, allocs, float64(12), "resolution should walk the object, not clone it")
}

func TestResolveEdgeCases(t *testing.T) {
	type inner struct {
		Visible  string `json:"visible"`
		Skipped  string `json:"-"`
		Untagged string
		hidden   string
	}

	obj := map[string]any{
		"array":     [2]string{"first", "second"},
		"typedMap":  map[string]string{"a": "one"},
		"intKeyMap": map[int]string{1: "no"},
		"nilPtr":    (*inner)(nil),
		"ptr":       &inner{Visible: "yes", Skipped: "no", Untagged: "plain"},
		"quantity":  resource.MustParse("500m"),
		"port":      intstr.FromInt32(8080),
		"portName":  intstr.FromString("https"),
	}

	cases := []struct {
		name   string
		path   string
		want   string
		wantOK bool
	}{
		{name: "array index", path: "array[1]", want: "second", wantOK: true},
		{name: "typed map", path: "typedMap.a", want: "one", wantOK: true},
		{name: "non-string map key", path: "intKeyMap.1", wantOK: false},
		{name: "nil pointer", path: "nilPtr.visible", wantOK: false},
		{name: "through pointer", path: "ptr.visible", want: "yes", wantOK: true},
		{name: "json dash field is skipped", path: "ptr.Skipped", wantOK: false},
		{name: "untagged field by name", path: "ptr.Untagged", want: "plain", wantOK: true},
		{name: "unexported field", path: "ptr.hidden", wantOK: false},
		{name: "custom marshaller quantity", path: "quantity", want: "500m", wantOK: true},
		{name: "custom marshaller int port", path: "port", want: "8080", wantOK: true},
		{name: "custom marshaller string port", path: "portName", want: "https", wantOK: true},
		{name: "index into a scalar", path: "typedMap.a[0]", wantOK: false},
		{name: "field on a scalar", path: "array[0].nope", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolvePath(obj, tc.path)
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}
