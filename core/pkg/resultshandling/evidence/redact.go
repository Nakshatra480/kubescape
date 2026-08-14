package evidence

import (
	"reflect"
	"regexp"
	"strings"
)

const RedactedPlaceholder = "<redacted>"

const (
	inputSensitiveKeyNames        = "sensitiveKeyNames"
	inputSensitiveValues          = "sensitiveValues"
	inputSensitiveKeyNamesAllowed = "sensitiveKeyNamesAllowed"
	inputSensitiveValuesAllowed   = "sensitiveValuesAllowed"
)

// Used when a scan has no control inputs: offline runs, unit tests, or a policy
// load that fell back to defaults. Redaction must not depend on a successful
// policy download, since failing open would leak on already degraded runs.
var (
	builtinSensitiveKeyNames = []string{
		"aws_secret_access_key", "azure_batchai_storage_key", "azure_batch_key",
		"secret", "key", "password", "pwd", "token", "jwt", "bearer", "credential",
	}
	builtinSensitiveValues = []string{
		`BEGIN \w+ PRIVATE KEY`, `PRIVATE KEY`, `eyJhbGciO`, `JWT`, `Bearer`, `_key_`, `_secret_`,
	}
)

var referenceFields = map[string]struct{}{
	"secretname":         {},
	"serviceaccountname": {},
}

// Fields under these point at where a value lives, or select a node or a label,
// rather than carrying a credential. secretKeyRef matters most: hiding its key
// would remove the very thing that tells a literal password apart from a
// reference, which is the case issue #1563 raises.
var structuralParents = map[string]struct{}{
	"secretkeyref":     {},
	"configmapkeyref":  {},
	"secretref":        {},
	"configmapref":     {},
	"fieldref":         {},
	"resourcefieldref": {},
	"tolerations":      {},
	"nodeselector":     {},
	"matchlabels":      {},
}

// Redaction applies in three tiers: Secret data is never shown, values whose
// field name or content looks like a credential are hidden unless --show-secrets
// is set, and everything else is shown. updateResults already masks container
// env values and Secret and ConfigMap data, but it skips objects that are not
// Kubernetes workloads and never touches command, args or annotations.
type Policy struct {
	keyNames      []string
	valuePatterns []*regexp.Regexp
	allowedKeys   map[string]struct{}
	allowedValues map[string]struct{}
	reveal        bool
}

func NewPolicy(controlInputs map[string][]string, reveal bool) *Policy {
	p := &Policy{
		reveal:        reveal,
		allowedKeys:   lowerSet(controlInputs[inputSensitiveKeyNamesAllowed]),
		allowedValues: lowerSet(controlInputs[inputSensitiveValuesAllowed]),
	}

	if p.keyNames = lowerAll(controlInputs[inputSensitiveKeyNames]); len(p.keyNames) == 0 {
		p.keyNames = lowerAll(builtinSensitiveKeyNames)
	}

	patterns := controlInputs[inputSensitiveValues]
	if len(patterns) == 0 {
		patterns = builtinSensitiveValues
	}
	for _, pattern := range patterns {
		// A bad operator regex must not take the scan down, nor disable the
		// rest of the list.
		if re, err := regexp.Compile("(?i)" + pattern); err == nil {
			p.valuePatterns = append(p.valuePatterns, re)
		}
	}

	return p
}

func DefaultPolicy(reveal bool) *Policy {
	return NewPolicy(nil, reveal)
}

func (p *Policy) Redact(kind, path string, raw any) (display string, redacted, ok bool) {
	value, ok := ScalarToString(raw)
	if !ok {
		return "", false, false
	}
	if AlwaysWithheld(kind, path) {
		return RedactedPlaceholder, true, true
	}
	if p.reveal {
		return value, false, true
	}
	// A bool or a number is never a credential, which keeps fields such as
	// automountServiceAccountToken visible.
	if !isStringValue(raw) {
		return value, false, true
	}
	if p.sensitive(path, value) {
		return RedactedPlaceholder, true, true
	}
	return value, false, true
}

func AlwaysWithheld(kind, path string) bool {
	if kind != "Secret" {
		return false
	}
	path = strings.TrimLeft(TrimValue(path), ".")
	return path == "data" || strings.HasPrefix(path, "data.") ||
		path == "stringData" || strings.HasPrefix(path, "stringData.")
}

func isStringValue(raw any) bool {
	if _, ok := raw.(string); ok {
		return true
	}
	rv := deref(reflect.ValueOf(raw))
	return rv.IsValid() && rv.Kind() == reflect.String
}

func isReferencePath(path string) bool {
	parent, ok := ParentPath(path)
	if !ok {
		return false
	}
	_, structural := structuralParents[strings.ToLower(LastKey(parent))]
	return structural
}

func (p *Policy) sensitive(path, value string) bool {
	if isReferencePath(path) {
		return false
	}
	if p.sensitiveKey(LastKey(path)) {
		return true
	}
	if value == "" {
		return false
	}
	if _, allowed := p.allowedValues[strings.ToLower(value)]; allowed {
		return false
	}
	for _, re := range p.valuePatterns {
		if re.MatchString(value) {
			return true
		}
	}
	return false
}

func (p *Policy) sensitiveKey(name string) bool {
	name = strings.ToLower(name)
	if name == "" {
		return false
	}
	if _, allowed := p.allowedKeys[name]; allowed {
		return false
	}
	if _, safe := referenceFields[name]; safe {
		return false
	}
	for _, candidate := range p.keyNames {
		if strings.Contains(name, candidate) {
			return true
		}
	}
	return false
}

// Rules emit a credential as two paths, the name and the value. Judged alone
// the name looks sensitive and the value looks innocuous, so without this the
// wrong half is hidden.
func (p *Policy) SensitiveSibling(siblingName string) bool {
	if p.reveal {
		return false
	}
	return p.sensitiveKey(siblingName)
}

func (p *Policy) Reveal() bool { return p.reveal }

func lowerAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func lowerSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out[s] = struct{}{}
		}
	}
	return out
}
