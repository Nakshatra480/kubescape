package printer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/k8s-interface/workloadinterface"
	"github.com/kubescape/kubescape/v3/core/cautils"
	"github.com/kubescape/kubescape/v3/core/pkg/resultshandling/evidence"
	"github.com/kubescape/opa-utils/reporthandling/apis"
	"github.com/kubescape/opa-utils/reporthandling/results/v1/resourcesresults"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const evidenceTestCredential = "s3cr3t-prod-pw"

func deploymentWithCredential() map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "billing", "namespace": "prod"},
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name": "api",
							"env": []any{
								map[string]any{"name": "DB_PASSWORD", "value": evidenceTestCredential},
							},
						},
					},
				},
			},
		},
	}
}

func sessionWithFailedCredentialControl(t *testing.T) *cautils.OPASessionObj {
	t.Helper()

	resource := workloadinterface.NewWorkloadObj(deploymentWithCredential())
	require.NotNil(t, resource)

	control := resourcesresults.ResourceAssociatedControl{
		ControlID: "C-0012",
		Name:      "Applications credentials in configuration files",
		ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{
			{
				Name:   "rule-credentials-in-env-var",
				Status: apis.StatusFailed,
				Paths: []armotypes.PosturePaths{
					{FailedPath: "spec.template.spec.containers[0].env[0].name"},
					{FailedPath: "spec.template.spec.containers[0].env[0].value"},
				},
			},
		},
	}

	result := resourcesresults.Result{
		ResourceID:         resource.GetID(),
		AssociatedControls: []resourcesresults.ResourceAssociatedControl{control},
	}

	session := cautils.NewOPASessionObjMock()
	session.AllResources = map[string]workloadinterface.IMetadata{resource.GetID(): resource}
	session.ResourcesResult = map[string]resourcesresults.Result{resource.GetID(): result}
	return session
}

func renderEvidence(t *testing.T, showEvidence, showSecrets bool) string {
	t.Helper()

	out := filepath.Join(t.TempDir(), "evidence.txt")
	pp := NewPrettyPrinter(false, "v2", false, cautils.SecurityViewType, cautils.ScanTypeCluster, []string{}, "", showEvidence, showSecrets)
	require.NoError(t, pp.SetWriter(context.Background(), out))
	t.Cleanup(func() { _ = pp.writer.Close() })

	if showEvidence {
		pp.printEvidence(sessionWithFailedCredentialControl(t))
	}

	content, err := os.ReadFile(out)
	require.NoError(t, err)
	return string(content)
}

func TestPrintEvidenceRedactsCredentialByDefault(t *testing.T) {
	got := renderEvidence(t, true, false)

	assert.Contains(t, got, "Deployment/prod/billing")
	assert.Contains(t, got, "C-0012")
	assert.Contains(t, got, "env[0].name")
	assert.Contains(t, got, "DB_PASSWORD")
	assert.Contains(t, got, "<redacted>")
	assert.NotContains(t, got, evidenceTestCredential)
	assert.Contains(t, got, "--show-secrets")
}

func TestPrintEvidenceRevealsUnderShowSecrets(t *testing.T) {
	got := renderEvidence(t, true, true)

	assert.Contains(t, got, evidenceTestCredential)
	assert.NotContains(t, got, "<redacted>")
	assert.NotContains(t, got, "--show-secrets", "the hint is pointless once values are revealed")
}

func TestEvidenceSectionAbsentWithoutFlag(t *testing.T) {
	got := renderEvidence(t, false, false)

	assert.Empty(t, got)
}

func TestPrintEvidenceReportsControlsWithoutFieldPaths(t *testing.T) {
	resource := workloadinterface.NewWorkloadObj(map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "naked", "namespace": "default"},
	})
	require.NotNil(t, resource)

	control := resourcesresults.ResourceAssociatedControl{
		ControlID: "C-0073",
		Name:      "Naked pods",
		ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{
			{Name: "naked-pods", Status: apis.StatusFailed},
		},
	}

	session := cautils.NewOPASessionObjMock()
	session.AllResources = map[string]workloadinterface.IMetadata{resource.GetID(): resource}
	session.ResourcesResult = map[string]resourcesresults.Result{
		resource.GetID(): {
			ResourceID:         resource.GetID(),
			AssociatedControls: []resourcesresults.ResourceAssociatedControl{control},
		},
	}

	out := filepath.Join(t.TempDir(), "evidence.txt")
	pp := NewPrettyPrinter(false, "v2", false, cautils.SecurityViewType, cautils.ScanTypeCluster, []string{}, "", true, false)
	require.NoError(t, pp.SetWriter(context.Background(), out))
	t.Cleanup(func() { _ = pp.writer.Close() })

	pp.printEvidence(session)

	content, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Contains(t, string(content), "no field-level evidence")
}

// A value the scan replaced before reporting must not be printed as if it were
// the field's content, and the footer has to say that no flag can recover it.
func TestPrintEvidenceDistinguishesScanMaskedValues(t *testing.T) {
	resource := workloadinterface.NewWorkloadObj(map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "billing", "namespace": "prod"},
		"spec": map[string]any{
			"containers": []any{map[string]any{
				"name": "api",
				"env":  []any{map[string]any{"name": "DB_PASSWORD", "value": cautils.MaskedValue}},
			}},
		},
	})
	require.NotNil(t, resource)

	control := resourcesresults.ResourceAssociatedControl{
		ControlID: "C-0012",
		Name:      "Applications credentials in configuration files",
		ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{{
			Name:   "rule-credentials-in-env-var",
			Status: apis.StatusFailed,
			Paths:  []armotypes.PosturePaths{{FailedPath: "spec.containers[0].env[0].value"}},
		}},
	}

	session := cautils.NewOPASessionObjMock()
	session.AllResources = map[string]workloadinterface.IMetadata{resource.GetID(): resource}
	session.ResourcesResult = map[string]resourcesresults.Result{
		resource.GetID(): {
			ResourceID:         resource.GetID(),
			AssociatedControls: []resourcesresults.ResourceAssociatedControl{control},
		},
	}

	out := filepath.Join(t.TempDir(), "evidence.txt")
	pp := NewPrettyPrinter(false, "v2", false, cautils.SecurityViewType, cautils.ScanTypeCluster, []string{}, "", true, true)
	require.NoError(t, pp.SetWriter(context.Background(), out))
	t.Cleanup(func() { _ = pp.writer.Close() })

	pp.printEvidence(session)

	content, err := os.ReadFile(out)
	require.NoError(t, err)
	got := string(content)

	assert.Contains(t, got, maskedPlaceholder)
	assert.NotContains(t, got, cautils.MaskedValue)
	assert.Contains(t, got, "no flag can show it")
}

func sessionWithFailedResources(t *testing.T, count int) *cautils.OPASessionObj {
	t.Helper()

	session := cautils.NewOPASessionObjMock()
	session.AllResources = map[string]workloadinterface.IMetadata{}
	session.ResourcesResult = map[string]resourcesresults.Result{}

	for i := range count {
		resource := workloadinterface.NewWorkloadObj(map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata":   map[string]any{"name": fmt.Sprintf("pod-%03d", i), "namespace": "demo"},
			"spec":       map[string]any{"hostPID": true},
		})
		require.NotNil(t, resource)

		control := resourcesresults.ResourceAssociatedControl{
			ControlID: "C-0038",
			Name:      "Host PID",
			ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{{
				Name:   "host-pid",
				Status: apis.StatusFailed,
				Paths:  []armotypes.PosturePaths{{ReviewPath: "spec.hostPID"}},
			}},
		}

		id := resource.GetID()
		session.AllResources[id] = resource
		session.ResourcesResult[id] = resourcesresults.Result{
			ResourceID:         id,
			AssociatedControls: []resourcesresults.ResourceAssociatedControl{control},
		}
	}
	return session
}

func renderSession(t *testing.T, session *cautils.OPASessionObj, verbose bool) string {
	t.Helper()

	out := filepath.Join(t.TempDir(), "evidence.txt")
	pp := NewPrettyPrinter(verbose, "v2", false, cautils.SecurityViewType, cautils.ScanTypeCluster, []string{}, "", true, false)
	require.NoError(t, pp.SetWriter(context.Background(), out))
	t.Cleanup(func() { _ = pp.writer.Close() })

	pp.printEvidence(session)

	content, err := os.ReadFile(out)
	require.NoError(t, err)
	return string(content)
}

// A cluster scan can fail hundreds of resources, and roughly twenty lines of
// evidence each would bury the summary.
func TestPrintEvidenceLimitsResourcesUnlessVerbose(t *testing.T) {
	session := sessionWithFailedResources(t, defaultEvidenceResourceLimit+5)

	got := renderSession(t, session, false)
	assert.Equal(t, defaultEvidenceResourceLimit, strings.Count(got, "Pod/demo/pod-"))
	assert.Contains(t, got, "5 more failed resources have evidence")

	verbose := renderSession(t, session, true)
	assert.Equal(t, defaultEvidenceResourceLimit+5, strings.Count(verbose, "Pod/demo/pod-"))
	assert.NotContains(t, verbose, "more failed resources have evidence")
}

func TestPrintEvidenceNoLimitNoticeUnderTheLimit(t *testing.T) {
	got := renderSession(t, sessionWithFailedResources(t, 3), false)

	assert.Equal(t, 3, strings.Count(got, "Pod/demo/pod-"))
	assert.NotContains(t, got, "more failed resources have evidence")
}

// A path wider than the column pushes its own value right, so the fix line has
// to follow that path rather than the column.
func TestPrintEvidenceAlignsFixValueUnderLongPaths(t *testing.T) {
	longPath := "spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem"
	require.Greater(t, len(longPath), maxEvidencePathWidth)

	ev := evidence.ControlEvidence{
		ControlID:   "C-0017",
		ControlName: "Immutable container filesystem",
		Items: []evidence.Item{{
			Path:     longPath,
			Kind:     evidence.KindFix,
			FixValue: "true",
		}},
	}

	out := filepath.Join(t.TempDir(), "evidence.txt")
	pp := NewPrettyPrinter(false, "v2", false, cautils.SecurityViewType, cautils.ScanTypeCluster, []string{}, "", true, false)
	require.NoError(t, pp.SetWriter(context.Background(), out))
	t.Cleanup(func() { _ = pp.writer.Close() })

	pp.printControlEvidence(ev)

	content, err := os.ReadFile(out)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, strings.Index(lines[1], "(not set)"), strings.Index(lines[2], "expected:"))
}
