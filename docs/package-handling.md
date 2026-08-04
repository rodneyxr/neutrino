# Package Handling

Neutrino works as a **multi-tier introspection engine**. It understands that you might be looking at a raw Helm chart, a curated Zarf package containing Helm charts, or an overarching UDS bundle containing Zarf packages.

This page covers how Neutrino identifies what type of package it's looking at, and its strategy for handling local and remote variations of each.

## Source Type Detection

Neutrino determines the package type using extension heuristics, naming conventions, and OCI manifest inspection:

| Signal | Detected Type |
|:---|:---|
| `uds-bundle-*.tar.zst` filename | Local UDS Bundle |
| OCI manifest with `uds-bundle.yaml` layer | Remote UDS Bundle |
| `zarf-package-*.tar.zst` filename | Local Zarf Package |
| OCI manifest with `application/vnd.zarf.config.v1+json` layer | Remote Zarf Package |
| `.tgz` archive with `Chart.yaml` inside | Helm Chart Archive |
| Directory containing `Chart.yaml` | Local Helm Chart |
| `oci://` prefix (without Zarf/UDS markers) | OCI Helm Chart |

For remote OCI sources, Neutrino inspects the manifest and layer annotations before downloading any data. This means it can distinguish between a Zarf package and a Helm chart in the same registry without pulling gigabytes of image layers.

## UDS Bundles

UDS bundles act as the highest-level orchestrator. They are composed of a `uds-bundle.yaml` which declares a series of embedded or external Zarf packages.

### Remote OCI Handling

Neutrino fetches only the root manifest layers from the OCI registry to locate the `uds-bundle.yaml`. It does **not** download the actual multi-gigabyte bundle blobs. Once it reads the manifest, it pulls only the lightweight layers containing the metadata needed for introspection.

For each Zarf package referenced in the bundle, Neutrino:

1. Resolves the package reference from the bundle manifest.
2. Pulls the Zarf package metadata (using the same selective-layer strategy).
3. Extracts each embedded Helm chart.
4. Runs the [Helm introspection](helm-introspection.md) pipeline against every chart.
5. Processes all `overrides` stanzas from `uds-bundle.yaml` for each chart (see [Bundle Overrides](#bundle-overrides) below).

### Local Tarball Handling

If a bundle is presented as a `.tar.zst` archive, Neutrino extracts it to an internal cache directory. Instead of parsing the whole bundle immediately, it uses the OCI layout (`index.json`) contained inside the tarball to locate and reconstruct individual embedded Zarf packages.

### Bundle Overrides

The `overrides` section of `uds-bundle.yaml` lets a bundle operator hard-code values or expose configurable variables for charts inside its Zarf packages. Neutrino parses both kinds and correlates them with the underlying chart analysis:

```yaml
packages:
  - name: my-app
    repository: oci://ghcr.io/example/my-app
    ref: "1.2.3"
    overrides:
      my-component:
        my-chart:
          values:
            - path: tls.enabled
              value: true
          variables:
            - name: MEMORY_LIMIT
              description: "Memory limit for the main container"
              path: resources.limits.memory
              default: "512Mi"
```

#### Override Values (`overrides.*.*.values`)

Hard-coded value overrides in `uds-bundle.yaml` are **not** user-configurable. They are always applied when the bundle is deployed. Neutrino:

- Records each override as a `UDSOverrideValue` with its `path` and explicit `value`.
- Looks up the **original chart default** from `values.yaml` for that path and records it as `originalDefault`, making it easy to see what baseline the bundle is overriding.
- Attaches each override to its corresponding `IntrospectResult` (identified by `packageName/componentName/chartName`) so it appears alongside the chart's other values.

#### Override Variables (`overrides.*.*.variables`)

Exposed bundle variables **are** configurable by end-users via `uds-config.yaml`. Neutrino:

- Records each variable as a `UDSOverrideVariable` with its `name`, `path`, `default`, and `description`.
- Cross-references `path` with the chart's `values.yaml` to populate `originalDefault`, showing both the bundle-operator-set default and the chart's baseline.
- Attaches each variable to its `IntrospectResult` and also collects them into a bundle-level `UDSOverrideVariables` list with full package/component/chart context for summary output.

## Zarf Packages

Zarf packages contain a `zarf.yaml` and a set of components carrying Helm charts, manifests, or images.

### Remote OCI

When pulling from a remote OCI registry, Neutrino leverages Zarf's built-in `PullOptions` to fetch the package tarball to a managed cache directory.

### Local Archives and Directories

Local `.tar.zst` archives are extracted to cache. If a local directory is provided, Neutrino reads it directly.

### Component Extraction

Once a Zarf package is available locally, Neutrino:

1. Reads `zarf.yaml` to catalog all components and their variables.
2. Iterates through `.tar` archives in the `components/` directory.
3. Unpacks the `charts/` directories from each component.
4. Invokes the [Helm introspection](helm-introspection.md) pipeline against each discovered chart.
5. Maps `###ZARF_VAR_*###` markers found in templates back to the variables defined in `zarf.yaml`.

!!! info "Component ordering"
    Components and packages are displayed in the order they are defined in `zarf.yaml` and `uds-bundle.yaml`, preserving the deployment sequence.

## Helm Charts

The foundational step for all package handling is the Helm chart.

### Loading Options

Charts can be loaded from multiple sources:

| Source | Handling |
|:---|:---|
| Local directory | Read directly from filesystem |
| Local `.tgz` archive | Extracted to cache |
| OCI (`oci://`) | Pulled via OCI client to cache |
| HTTP(S) URL | Downloaded and extracted to cache |

### Analysis Pipeline

Once a chart is loaded locally, Neutrino:

1. Reads `Chart.yaml` for metadata (name, version, dependencies).
2. Loads `values.yaml` and merges any upstream overrides (e.g. from a Zarf package's custom values files).
3. If `--include-subcharts` is enabled (default), recursively processes all dependency charts in `charts/`.
4. Passes all templates to the [AST Walker](ast-walker.md) for value discovery.
5. Infers additional value paths from `values.yaml` keys that weren't directly referenced in templates (unless `--no-values` is set).
