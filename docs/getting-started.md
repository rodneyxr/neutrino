# Getting Started

## Prerequisites

- **Go 1.25+** — Neutrino is written in Go and is built from source.

## Installation

```bash
# Clone the repository
git clone https://github.com/defenseunicorns-dashdays/neutrino.git
cd neutrino

# Build the binary
go build -o neutrino

# Verify installation
./neutrino --help
```

## Your First Inspection

### Inspect a Helm Chart

Point Neutrino at any local chart directory, archive, or OCI URL:

```bash
# Local chart directory
neutrino inspect ./charts/podinfo

# OCI registry
neutrino inspect oci://ghcr.io/stefanprodan/charts/podinfo:6.11.1
```

Neutrino will parse all templates, walk the AST, and output a table of every discoverable `.Values` path along with its default value, usage type, and source location.

### Inspect a Zarf Package

Neutrino understands Zarf packages natively. It extracts all embedded Helm charts, analyzes them, and surfaces Zarf variables defined in `zarf.yaml`:

```bash
# Local Zarf archive
neutrino inspect ./zarf-package-podinfo-amd64-0.0.1.tar.zst

# Remote Zarf package from OCI
neutrino inspect oci://ghcr.io/zarf-dev/packages/init:v0.74.0
```

### Inspect a UDS Bundle

UDS Bundles contain multiple Zarf packages. Neutrino pulls only the metadata layers it needs and analyzes every chart across every package:

```bash
neutrino inspect oci://ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-demo:1.0.0
```

!!! tip "Architecture flag"
    When inspecting multi-arch OCI artifacts, specify the architecture explicitly:
    ```bash
    neutrino inspect oci://ghcr.io/defenseunicorns/packages/uds/core-base:1.0.0-upstream -a amd64
    ```

### Interactive TUI Mode

Add the `-i` flag to enter the interactive terminal UI. This lets you search, filter, and navigate all values across components with keyboard controls:

```bash
neutrino inspect oci://ghcr.io/zarf-dev/packages/init:v0.74.0 -i
```

See [Interactive TUI](tui.md) for the full keybinding reference.

## Output Formats

Neutrino supports three output formats:

=== "Table (default)"

    ```bash
    neutrino inspect ./charts/podinfo
    ```

    Produces a human-readable table grouped by component, with columns for path, default value, type, and location.

=== "JSON"

    ```bash
    neutrino inspect ./charts/podinfo -o json > introspection.json
    ```

    Machine-readable output for CI pipelines and automation.

=== "YAML"

    ```bash
    neutrino inspect ./charts/podinfo -o yaml
    ```

    YAML-formatted output.

## Next Steps

- [CLI Reference](cli-reference.md) — Full flag and option reference.
- [Interactive TUI](tui.md) — Keyboard navigation and search.
- [Architecture](package-handling.md) — How Neutrino handles each package type.
