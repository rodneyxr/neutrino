# CLI Reference

## `neutrino inspect`

The core command. Analyzes a package source and outputs discovered overridable values, Zarf variables, and chart metadata.

### Syntax

```bash
neutrino inspect [PACKAGE_SOURCE] [flags]
```

### Package Sources

`[PACKAGE_SOURCE]` accepts the following formats:

| Source Type | Example |
|:---|:---|
| Local helm chart directory | `./charts/podinfo` |
| Local helm chart archive | `./podinfo-6.11.1.tgz` |
| Zarf package archive | `./zarf-package-app-amd64-0.0.1.tar.zst` |
| OCI chart URL | `oci://ghcr.io/stefanprodan/charts/podinfo:6.11.1` |
| HTTP(S) chart URL | `https://example.com/charts/podinfo-6.11.1.tgz` |
| Remote Zarf package | `oci://ghcr.io/zarf-dev/packages/init:v0.74.0` |
| UDS Bundle | `oci://ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-demo:1.0.0` |

### Flags

| Flag | Type | Default | Description |
|:---|:---|:---|:---|
| `--version` | `string` | | Version of the chart to fetch (when using URL). |
| `--output`, `-o` | `string` | `table` | Output format: `table`, `json`, `yaml`. |
| `--include-subcharts` | `bool` | `true` | Recursively analyze dependencies found in `charts/`. |
| `--architecture`, `-a` | `string` | | Architecture for OCI images and Zarf packages (e.g. `amd64`, `arm64`). |
| `--interactive`, `-i` | `bool` | `false` | Enter interactive TUI mode. |
| `--sort-mode` | `string` | `path` | Sort mode for values: `path` or `discovery`. |
| `--wildcards` | `bool` | `false` | Include wildcard/dynamic values (e.g. `ingress.hosts.*.host`) in output. |
| `--absolute` | `bool` | `false` | Output absolute filepaths for locations. |
| `--no-values` | `bool` | `false` | Disable inference of nested paths from `values.yaml`. |
| `--no-cache` | `bool` | `false` | Force re-download of a package, ignoring local cache. |
| `--log-level` | `string` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`. |

### Examples

#### Inspect a local chart

```bash
neutrino inspect ./charts/podinfo
```

#### Inspect an OCI chart with version tag

```bash
neutrino inspect oci://ghcr.io/stefanprodan/charts/podinfo:6.11.1
```

The version can also be specified separately:

```bash
neutrino inspect oci://ghcr.io/stefanprodan/charts/podinfo --version 6.11.1
```

#### Inspect a Zarf package

```bash
neutrino inspect ./zarf-package-podinfo-amd64-0.0.1.tar.zst
neutrino inspect oci://ghcr.io/zarf-dev/packages/init:v0.74.0
```

#### Inspect a UDS Bundle

```bash
neutrino inspect oci://ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-demo:1.0.0
```

#### JSON output for CI

```bash
neutrino inspect ./charts/podinfo -o json > introspection.json
```

#### Interactive exploration

```bash
neutrino inspect oci://ghcr.io/zarf-dev/packages/init:v0.74.0 -i
```

## Understanding the Output

Neutrino classifies value usage into categories:

### Usage Types

| Type | Description | Example |
|:---|:---|:---|
| **`field`** | Direct access to a value key. | `{{ .Values.image.repository }}` → `image.repository` |
| **`variable`** | Value assigned to a variable. | `{{ $svc := .Values.service }}` → `service` |
| **`inferred`** | Path exists in `values.yaml` but is not directly referenced in templates. | Nested keys from `values.yaml` |

### Wildcard Paths

When values are accessed inside `range` loops, Neutrino uses `*` to denote dynamic segments:

```
{{ range .Values.ingress.hosts }}
  - host: {{ .host }}
{{ end }}
```

Produces the path: `ingress.hosts.*.host`

!!! note
    Wildcard paths are hidden by default. Use `--wildcards` to include them.

### Zarf Variable Tables

When inspecting a Zarf package or UDS Bundle, Neutrino outputs additional tables:

- **Zarf Package Variables** — Variables defined in `zarf.yaml` with their default values and descriptions.
- **Per-component Zarf Variables** — Locations where `###ZARF_VAR_*###` template markers are used in chart templates, mapped back to their `zarf.yaml` definitions.

### UDS Bundle Override Tables

When inspecting a UDS Bundle, two additional summary tables are printed after the Zarf variable tables:

**UDS Bundle Override Values** — Hard-coded values set by the bundle's `overrides.*.values` stanza. These are applied unconditionally at deploy time and are **not** overridable via `uds-config.yaml`.

| Column | Description |
|:---|:---|
| Package | The Zarf package name |
| Component/Chart | `<componentName>/<chartName>` pair |
| Path | The Helm values path being overridden |
| Value | The value explicitly set by the bundle |
| Original Default | The chart's original default (if found) |

**UDS Bundle Override Variables** — Variables exposed by the bundle's `overrides.*.variables` stanza. End-users can set or override these via `uds-config.yaml`.

| Column | Description |
|:---|:---|
| Package | The Zarf package name |
| Component/Chart | `<componentName>/<chartName>` pair |
| Variable | The variable name (e.g. `MEMORY_LIMIT`) |
| Path | The Helm values path the variable controls |
| Default | The bundle-operator-provided default |
| Original Default | The chart's original default (if found) |
| Description | Human-readable description |

Per-component sections also include "UDS Override Values" and "UDS Override Variables" sub-tables directly beneath the Zarf variable table for each chart.

### JSON Schema

When using `-o json`, the output provides a machine-readable structure:

```json
{
  "variables": [
    {
      "name": "DOMAIN",
      "default": "uds.dev",
      "description": "The domain to use for the cluster"
    }
  ],
  "udsOverrideValues": [
    {
      "packageName": "my-app",
      "componentName": "my-component",
      "chartName": "my-chart",
      "path": "tls.enabled",
      "value": true,
      "originalDefault": false
    }
  ],
  "udsOverrideVariables": [
    {
      "packageName": "my-app",
      "componentName": "my-component",
      "chartName": "my-chart",
      "name": "MEMORY_LIMIT",
      "path": "resources.limits.memory",
      "default": "512Mi",
      "originalDefault": "256Mi",
      "description": "Memory limit for the main container"
    }
  ],
  "components": {
    "loki": {
      "chartName": "loki",
      "chartVersion": "6.4.0",
      "source": "/path/to/extracted/chart",
      "values": [
        {
          "path": "ingress.hosts.*.paths.*.path",
          "chart": "loki",
          "locations": [
            {
              "kind": "field",
              "location": "templates/ingress.yaml:32:5"
            }
          ]
        }
      ],
      "zarfVars": [
        {
          "name": "LOKI_STORAGE_CLASS",
          "default": "",
          "locations": [
            {
              "kind": "variable",
              "location": "templates/configmap.yaml:12:8"
            }
          ]
        }
      ],
      "udsOverrideValues": [
        {
          "path": "tls.enabled",
          "value": true,
          "originalDefault": false
        }
      ],
      "udsOverrideVariables": [
        {
          "name": "MEMORY_LIMIT",
          "path": "resources.limits.memory",
          "default": "512Mi",
          "originalDefault": "256Mi",
          "description": "Memory limit for the main container"
        }
      ]
    }
  }
}
```
