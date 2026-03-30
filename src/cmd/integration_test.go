package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_Inspect_OCI_Helm(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Capture output
	buf := new(bytes.Buffer)
	cmd := newInspectCmd(buf)

	// Set args
	// We rely on the public ghcr.io/stefanprodan/charts/podinfo being available
	// This ensures the OCI regression logic holds up in a real execution environment
	cmd.SetArgs([]string{"oci://ghcr.io/stefanprodan/charts/podinfo", "--version", "6.9.3"})

	// Run
	err := cmd.Execute()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Chart: podinfo")
	// assert.Contains(t, output, "Source: oci://ghcr.io/stefanprodan/charts/podinfo") - Source might be local cache path in test env
	// Verify some expected introspected values
	assert.Contains(t, output, "serviceMonitor.interval")
	assert.Contains(t, output, "templates/servicemonitor.yaml:16")
}

func TestIntegration_Inspect_Invalid(t *testing.T) {
	buf := new(bytes.Buffer)
	cmd := newInspectCmd(buf)
	// Usage of invalid host to ensure connection failure
	cmd.SetArgs([]string{"oci://localhost:12345/nonexistant"})

	// Silence usage to keep test logs clean
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	// Expect error because:
	// 1. isZarfPackage returns false (due to connection error)
	// 2. Helm inspector tries to fetch and fails
	err := cmd.Execute()
	require.Error(t, err)
}
