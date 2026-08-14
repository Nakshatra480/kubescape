package printer

import (
	"encoding/json"
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/k8s-interface/workloadinterface"
	"github.com/kubescape/kubescape/v3/core/cautils"
	"github.com/kubescape/opa-utils/reporthandling/apis"
	"github.com/kubescape/opa-utils/reporthandling/results/v1/resourcesresults"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildEvidenceReport(t *testing.T) {
	report := buildEvidenceReport(sessionWithFailedCredentialControl(t), false)

	require.Len(t, report, 1)
	assert.Equal(t, "Deployment", report[0].Kind)
	assert.Equal(t, "prod", report[0].Namespace)
	assert.Equal(t, "billing", report[0].Name)

	require.Len(t, report[0].Controls, 1)
	control := report[0].Controls[0]
	assert.Equal(t, "C-0012", control.ControlID)
	require.Len(t, control.Items, 2)

	assert.Equal(t, "DB_PASSWORD", control.Items[0].Value)
	assert.False(t, control.Items[0].Redacted)

	assert.True(t, control.Items[1].Redacted)
	assert.NotContains(t, control.Items[1].Value, evidenceTestCredential)
}

// The evidence key is opt-in, so a consumer parsing the default output sees the
// same document as before.
func TestEvidenceIsAbsentFromTheReportByDefault(t *testing.T) {
	report := PostureReportWithSeverity{}

	encoded, err := json.Marshal(report)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "evidence")
}

func TestEvidenceReportMarksMaskedValues(t *testing.T) {
	resource := workloadinterface.NewWorkloadObj(map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "masked", "namespace": "demo"},
		"spec": map[string]any{
			"containers": []any{map[string]any{
				"name": "api",
				"env":  []any{map[string]any{"name": "DB_PASSWORD", "value": cautils.MaskedValue}},
			}},
		},
	})
	require.NotNil(t, resource)

	session := cautils.NewOPASessionObjMock()
	session.AllResources = map[string]workloadinterface.IMetadata{resource.GetID(): resource}
	session.ResourcesResult = map[string]resourcesresults.Result{
		resource.GetID(): {
			ResourceID: resource.GetID(),
			AssociatedControls: []resourcesresults.ResourceAssociatedControl{{
				ControlID: "C-0012",
				Name:      "Applications credentials in configuration files",
				ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{{
					Name:   "rule-credentials-in-env-var",
					Status: apis.StatusFailed,
					Paths:  []armotypes.PosturePaths{{FailedPath: "spec.containers[0].env[0].value"}},
				}},
			}},
		},
	}

	report := buildEvidenceReport(session, true)

	require.Len(t, report, 1)
	require.Len(t, report[0].Controls, 1)
	require.Len(t, report[0].Controls[0].Items, 1)

	item := report[0].Controls[0].Items[0]
	assert.True(t, item.Masked)
	assert.False(t, item.Redacted)
	assert.Equal(t, maskedPlaceholder, item.Value)
	assert.NotContains(t, item.Value, cautils.MaskedValue)
}

// A control that cannot name a field is reported as such rather than as an
// empty item list a consumer would read as a bug.
func TestEvidenceReportKeepsNoFieldEvidenceFlag(t *testing.T) {
	resource := workloadinterface.NewWorkloadObj(map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "naked", "namespace": "demo"},
	})
	require.NotNil(t, resource)

	session := cautils.NewOPASessionObjMock()
	session.AllResources = map[string]workloadinterface.IMetadata{resource.GetID(): resource}
	session.ResourcesResult = map[string]resourcesresults.Result{
		resource.GetID(): {
			ResourceID: resource.GetID(),
			AssociatedControls: []resourcesresults.ResourceAssociatedControl{{
				ControlID:               "C-0073",
				Name:                    "Naked pods",
				ResourceAssociatedRules: []resourcesresults.ResourceAssociatedRule{{Name: "naked-pods", Status: apis.StatusFailed}},
			}},
		},
	}

	report := buildEvidenceReport(session, false)

	require.Len(t, report, 1)
	require.Len(t, report[0].Controls, 1)
	assert.True(t, report[0].Controls[0].NoFieldEvidence)
	assert.Empty(t, report[0].Controls[0].Items)
}
