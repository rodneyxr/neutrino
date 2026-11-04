package introspect

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeContextReferencesKeepObservedSourceCoordinates(t *testing.T) {
	const source = "certs: '{{ .Variables.CA_BUNDLE_CERTS }}'\nstate: '{{ index .State \"ready\" }}'\nagain: '{{ .Variables.CA_BUNDLE_CERTS }}'\n"
	usages := inspectTemplateText(source, "uds-operator-config-0.1.0-0", "operator", "operator")
	require.Len(t, usages, 2)
	lines := strings.Split(source, "\n")
	for _, usage := range usages {
		require.True(t, usage.Runtime)
		require.Empty(t, usage.TargetPath, "a runtime reference does not declare a Helm target")
		for _, location := range usage.Locations {
			require.Equal(t, "runtimeContext", location.Kind)
			require.Positive(t, location.Line)
			require.Positive(t, location.Column)
			line := lines[location.Line-1]
			require.LessOrEqual(t, location.Column, len(line))
			// Go's parser can point at a later traversal segment (for example
			// .CA_BUNDLE_CERTS), rather than the start of the complete field.
			token := strings.Fields(line[location.Column-1:])[0]
			require.Contains(t, usage.Path, token, "position must identify an observed reference: %+v", location)
		}
	}
	require.Len(t, usages[0].Locations, 2, "repeated references retain both locations")
	require.Equal(t, 1, usages[0].Locations[0].Line)
	require.Equal(t, 3, usages[0].Locations[1].Line)
}
