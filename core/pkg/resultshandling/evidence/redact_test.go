package evidence

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const plaintextCredential = "s3cr3t-prod-pw"

// displayValue is the single-path form of ResourceView.DisplayValue, which the
// policy tests use so each case reads as one object and one path.
func displayValue(obj map[string]any, kind, path string, policy *Policy) (string, bool, bool) {
	return NewObjectView(obj, kind).DisplayValue(path, policy)
}

// deploymentWithPlaintextCredential is the shape control C-0012
// (rule-credentials-in-env-var) fires on: a workload holding a literal
// credential in an environment variable. The rule's failed paths point at
// env[0].name and env[0].value.
func deploymentWithPlaintextCredential() map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "billing", "namespace": "prod"},
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"automountServiceAccountToken": true,
					"containers": []any{
						map[string]any{
							"name": "api",
							"env": []any{
								map[string]any{"name": "DB_PASSWORD", "value": plaintextCredential},
							},
						},
					},
				},
			},
		},
	}
}

// TestC0012CredentialIsNotPrinted guards the policy itself, not the scan
// pipeline. A full scan masks container env values in updateResults before any
// printer runs, so this exact field is already blanked end to end. The policy
// matters for the paths that masking does not cover: removeData skips objects
// that are not Kubernetes workloads, and for workloads it only rewrites env
// values, Secret data and ConfigMap data, leaving command, args and annotations
// untouched. Evidence rendering prints far more fields than the old table did,
// so it cannot rely on that masking being complete.
func TestC0012CredentialIsNotPrinted(t *testing.T) {
	obj := deploymentWithPlaintextCredential()
	const valuePath = "spec.template.spec.containers[0].env[0].value"

	display, redacted, ok := displayValue(obj, "Deployment", valuePath, DefaultPolicy(false))

	require.True(t, ok, "the path must resolve; the point is that its value is withheld")
	assert.True(t, redacted)
	assert.Equal(t, RedactedPlaceholder, display)
	assert.NotContains(t, display, plaintextCredential)
}

// The rule emits the credential as two paths. Judged in isolation the name
// looks sensitive and the value looks innocuous, so without the sibling rule
// the wrong half is hidden.
func TestCredentialNameIsShownAndValueIsHidden(t *testing.T) {
	obj := deploymentWithPlaintextCredential()
	policy := DefaultPolicy(false)

	name, nameRedacted, ok := displayValue(obj, "Deployment", "spec.template.spec.containers[0].env[0].name", policy)
	require.True(t, ok)
	assert.False(t, nameRedacted, "the field name is what tells the user which credential leaked")
	assert.Equal(t, "DB_PASSWORD", name)

	value, valueRedacted, ok := displayValue(obj, "Deployment", "spec.template.spec.containers[0].env[0].value", policy)
	require.True(t, ok)
	assert.True(t, valueRedacted)
	assert.Equal(t, RedactedPlaceholder, value)
}

func TestShowSecretsRevealsTierTwo(t *testing.T) {
	obj := deploymentWithPlaintextCredential()

	display, redacted, ok := displayValue(obj, "Deployment", "spec.template.spec.containers[0].env[0].value", DefaultPolicy(true))

	require.True(t, ok)
	assert.False(t, redacted)
	assert.Equal(t, plaintextCredential, display)
}

func TestSecretDataStaysHiddenEvenWithShowSecrets(t *testing.T) {
	secret := map[string]any{
		"kind": "Secret",
		"data": map[string]any{"password": "aHVudGVyMg=="},
	}

	for _, reveal := range []bool{false, true} {
		display, redacted, ok := displayValue(secret, "Secret", "data.password", DefaultPolicy(reveal))
		require.True(t, ok)
		assert.True(t, redacted, "Secret data is tier 1: never shown, reveal=%v", reveal)
		assert.Equal(t, RedactedPlaceholder, display)
	}
}

// A bool or number is never a credential. automountServiceAccountToken is the
// field that makes this matter: its name contains "token".
func TestNonStringValuesAreNotRedacted(t *testing.T) {
	obj := deploymentWithPlaintextCredential()

	display, redacted, ok := displayValue(obj, "Deployment", "spec.template.spec.automountServiceAccountToken", DefaultPolicy(false))

	require.True(t, ok)
	assert.False(t, redacted)
	assert.Equal(t, "true", display)
}

func TestReferenceFieldsAreNotRedacted(t *testing.T) {
	obj := map[string]any{
		"kind": "Pod",
		"spec": map[string]any{"serviceAccountName": "builder"},
	}

	display, redacted, ok := displayValue(obj, "Pod", "spec.serviceAccountName", DefaultPolicy(false))

	require.True(t, ok)
	assert.False(t, redacted, "the field names a ServiceAccount, it does not hold a credential")
	assert.Equal(t, "builder", display)
}

func TestSensitiveValuePatterns(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "JWT", value: "eyJhbGciOiJIUzI1NiJ9.abc.def", want: true},
		{name: "bearer token", value: "Bearer abcdef123456", want: true},
		{name: "PEM private key", value: "-----BEGIN RSA PRIVATE KEY-----", want: true},
		{name: "ordinary image tag", value: "nginx:1.25", want: false},
		{name: "ordinary path", value: "/var/run/data", want: false},
	}

	policy := DefaultPolicy(false)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj := map[string]any{"kind": "Pod", "spec": map[string]any{"note": tc.value}}
			_, redacted, ok := displayValue(obj, "Pod", "spec.note", policy)
			require.True(t, ok)
			assert.Equal(t, tc.want, redacted)
		})
	}
}

func TestControlInputsDriveThePolicy(t *testing.T) {
	obj := map[string]any{"kind": "Pod", "spec": map[string]any{"launchCode": "abc123"}}

	shown, redacted, ok := displayValue(obj, "Pod", "spec.launchCode", DefaultPolicy(false))
	require.True(t, ok)
	require.False(t, redacted, "not sensitive under the built-in lists")
	assert.Equal(t, "abc123", shown)

	custom := NewPolicy(map[string][]string{"sensitiveKeyNames": {"launchcode"}}, false)
	_, redacted, ok = displayValue(obj, "Pod", "spec.launchCode", custom)
	require.True(t, ok)
	assert.True(t, redacted, "an operator-supplied key name must take effect")
}

func TestAllowListSuppressesRedaction(t *testing.T) {
	obj := map[string]any{"kind": "Pod", "spec": map[string]any{"token": "public-demo"}}

	policy := NewPolicy(map[string][]string{"sensitiveKeyNamesAllowed": {"token"}}, false)

	display, redacted, ok := displayValue(obj, "Pod", "spec.token", policy)
	require.True(t, ok)
	assert.False(t, redacted)
	assert.Equal(t, "public-demo", display)
}

// A bad operator regex must not disable the rest of the list, and must not
// take the scan down.
func TestMalformedPatternIsSkipped(t *testing.T) {
	policy := NewPolicy(map[string][]string{
		"sensitiveValues": {"([unclosed", "eyJhbGciO"},
	}, false)

	obj := map[string]any{"kind": "Pod", "spec": map[string]any{"note": "eyJhbGciOiJIUzI1NiJ9"}}
	_, redacted, ok := displayValue(obj, "Pod", "spec.note", policy)

	require.True(t, ok)
	assert.True(t, redacted, "the valid pattern must still apply")
}

// Redaction must not depend on a successful policy download: a degraded scan is
// exactly when leaking would be worst.
func TestEmptyControlInputsStillRedact(t *testing.T) {
	obj := deploymentWithPlaintextCredential()

	policy := NewPolicy(map[string][]string{}, false)
	display, redacted, ok := displayValue(obj, "Deployment", "spec.template.spec.containers[0].env[0].value", policy)

	require.True(t, ok)
	assert.True(t, redacted)
	assert.False(t, strings.Contains(display, plaintextCredential))
}

// The issue this work comes from names the case directly: a user must be able
// to tell a literal password apart from a valueFrom.secretKeyRef. Fields under
// a reference point at where a value lives rather than carrying it, so hiding
// them would remove exactly the evidence that distinguishes the two.
func TestReferencePathsAreNotRedacted(t *testing.T) {
	obj := map[string]any{
		"kind": "Pod",
		"spec": map[string]any{
			"tolerations": []any{map[string]any{"key": "node-role", "operator": "Exists"}},
			"containers": []any{map[string]any{
				"name": "api",
				"env": []any{map[string]any{
					"name": "DB_PASSWORD",
					"valueFrom": map[string]any{
						"secretKeyRef": map[string]any{"name": "db-secret", "key": "password"},
					},
				}},
			}},
		},
	}
	policy := DefaultPolicy(false)

	for _, tc := range []struct{ name, path, want string }{
		{"secret name", "spec.containers[0].env[0].valueFrom.secretKeyRef.name", "db-secret"},
		{"key within the secret", "spec.containers[0].env[0].valueFrom.secretKeyRef.key", "password"},
		{"toleration key", "spec.tolerations[0].key", "node-role"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			display, redacted, ok := displayValue(obj, "Pod", tc.path, policy)
			require.True(t, ok)
			assert.False(t, redacted, "a reference does not carry the credential")
			assert.Equal(t, tc.want, display)
		})
	}
}
