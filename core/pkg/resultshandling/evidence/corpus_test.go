package evidence

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ruleFieldNames is every distinct field a regolibrary rule path ends in,
// extracted from the path literals in all 257 rules of regolibrary v2.0.1.
// Redaction keys on this name, so any entry it hides is a field a user asked
// about and did not get an answer for.
var ruleFieldNames = []string{
	"allowPrivilegeEscalation", "allowedCapabilities", "annotations", "automountServiceAccountToken",
	"clientCAFile", "command", "containerRuntimeVersion", "cpu", "drop", "enableRBAC", "enabled",
	"fsGroup", "fsGroupChangePolicy", "hostIPC", "hostNetwork", "hostPID", "image", "imageID",
	"imagePullPolicy", "kernelVersion", "kms", "kubeletVersion", "labels", "livenessProbe",
	"matchLabels", "memory", "mode", "name", "namespace", "osImage", "path", "privileged",
	"procMount", "providers", "readinessProbe", "requiredDropCapabilities", "resources", "rule",
	"rules", "runAsGroup", "runAsNonRoot", "runAsUser", "securityContext", "selector",
	"serviceAccountName", "spec", "state", "storageClassName", "subjects", "supplementalGroups",
	"sysctls", "tls", "type", "value",
}

// automountServiceAccountToken is the only corpus field whose name matches a
// sensitive term. It always holds a bool, which Redact shows regardless, so it
// never actually reaches a user as hidden. It is listed here so that a change
// making the name check stricter or looser shows up as a test failure.
var expectedNameMatches = map[string]struct{}{
	"automountServiceAccountToken": {},
}

func TestRedactionDoesNotHideOrdinaryRuleFields(t *testing.T) {
	policy := DefaultPolicy(false)

	for _, field := range ruleFieldNames {
		t.Run(field, func(t *testing.T) {
			_, expected := expectedNameMatches[field]
			assert.Equal(t, expected, policy.sensitiveKey(field),
				"field appears in real rule paths; hiding it costs the user the answer they asked for")
		})
	}
}

// The one name match holds a bool in every resource that carries it, and a
// bool is never treated as a credential.
func TestAutomountTokenStaysVisible(t *testing.T) {
	obj := map[string]any{
		"kind": "Pod",
		"spec": map[string]any{"automountServiceAccountToken": true},
	}

	display, redacted, ok := displayValue(obj, "Pod", "spec.automountServiceAccountToken", DefaultPolicy(false))

	assert.True(t, ok)
	assert.False(t, redacted)
	assert.Equal(t, "true", display)
}
