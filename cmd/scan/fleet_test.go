package scan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFleetOptionsValidate(t *testing.T) {
	cases := []struct {
		name    string
		opts    fleetOptions
		wantErr string
	}{
		{
			name: "valid",
			opts: fleetOptions{contexts: []string{"prod", "staging"}},
		},
		{
			name: "valid with baseline and drift only",
			opts: fleetOptions{contexts: []string{"prod", "staging"}, baseline: "staging", driftOnly: true},
		},
		{
			name:    "no contexts",
			opts:    fleetOptions{},
			wantErr: "--contexts is required",
		},
		{
			name:    "single context",
			opts:    fleetOptions{contexts: []string{"prod"}},
			wantErr: "at least two clusters",
		},
		{
			name:    "drift only without baseline",
			opts:    fleetOptions{contexts: []string{"prod", "staging"}, driftOnly: true},
			wantErr: "--drift-only requires --baseline",
		},
		{
			name:    "baseline not in contexts",
			opts:    fleetOptions{contexts: []string{"prod", "staging"}, baseline: "dev"},
			wantErr: "not in --contexts",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestGetFleetCmdFlags(t *testing.T) {
	cmd := getFleetCmd(nil, nil)

	for _, flag := range []string{"contexts", "baseline", "frameworks", "drift-only"} {
		assert.NotNil(t, cmd.PersistentFlags().Lookup(flag), "missing --%s", flag)
	}
}

// Defining a --format flag here would shadow the one scan already has and drop
// its -f shorthand, leaving fleet as the only subcommand where -f fails.
func TestFleetDoesNotShadowTheSharedFormatFlag(t *testing.T) {
	cmd := getFleetCmd(nil, nil)

	assert.Nil(t, cmd.PersistentFlags().Lookup("format"))
	assert.Nil(t, cmd.Flags().Lookup("format"))
}

func TestValidateFleetFormat(t *testing.T) {
	for _, format := range []string{"", "pretty-printer", "json"} {
		assert.NoError(t, validateFleetFormat(format), "format %q should be accepted", format)
	}

	err := validateFleetFormat("sarif")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan fleet supports")

	assert.True(t, fleetOutputIsJSON("json"))
	assert.False(t, fleetOutputIsJSON("pretty-printer"))
}
