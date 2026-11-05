# Package Handling

Neutrino works as a **multi-tier introspection engine**. It understands that you might be looking at a raw Helm chart, a curated Zarf package containing Helm charts, or an overarching UDS bundle containing Zarf packages.

This page covers how Neutrino identifies what type of package it's looking at, and its strategy for handling local and remote variations of each.

## Source Type Detection

Neutrino determines the package type using extension heuristics, naming conventions, and OCI manifest inspection:

| Signal | Detected Type |
|:---|:---|
| `uds-bundle-*.tar.zst` filename | Local UDS Bundle |
| OCI manifest with `uds-bundle.yaml` layer | Remote UDS Bundle |
| `bundle.uds.hcl` file or containing directory | UDS Next source |
| OCI index with `application/vnd.defenseunicorns.uds.bundle.v1` artifact type | UDS Next artifact |
| Directory containing `zarf.yaml` | Local Zarf source |
| `zarf-package-*.tar.zst` filename | Local Zarf Package |
| OCI manifest with `application/vnd.zarf.config.v1+json` layer | Remote Zarf Package |
| `.tgz` archive with `Chart.yaml` inside | Helm Chart Archive |
| Directory containing `Chart.yaml` | Local Helm Chart |
| `oci://` prefix (without Zarf/UDS markers) | OCI Helm Chart |

For remote OCI sources, Neutrino inspects the manifest and layer annotations before downloading any data. This means it can distinguish between a Zarf package and a Helm chart in the same registry without pulling gigabytes of image layers.

## UDS Bundles

Legacy UDS bundles use `uds-bundle.yaml`; Next bundles use `bundle.uds.hcl` and a distinct OCI artifact format. Both contain Zarf packages, and both retain legacy Zarf-variable inspection alongside Package Values.

### Remote OCI Handling

Neutrino fetches only the root manifest layers from the OCI registry to locate the `uds-bundle.yaml`. It does **not** download the actual multi-gigabyte bundle blobs. Once it reads the manifest, it pulls only the lightweight layers containing the metadata needed for introspection.

Legacy bundle roots can advertise either the UDS config media type or Zarf's layer blob media type; Neutrino selects the `uds-bundle.yaml` descriptor in either case. Embedded Zarf package roots still require the Zarf config media type.

For each Zarf package referenced in the bundle, Neutrino:

1. Resolves the package reference from the bundle manifest.
2. Pulls the Zarf package metadata (using the same selective-layer strategy).
   The embedded package manifest comes from the bundle. Selected package layers come from the package's declared `repository` when present, or from the bundle repository otherwise. Every selected layer is verified against its embedded descriptor.
3. Extracts each embedded Helm chart.
4. Runs the [Helm introspection](helm-introspection.md) pipeline against every chart.
5. Processes all `overrides` stanzas from `uds-bundle.yaml` for each chart (see [Bundle Overrides](#bundle-overrides) below).

### Legacy Local Tarball Handling

If a bundle is presented as a `.tar.zst` archive, Neutrino extracts it to an internal cache directory. Instead of parsing the whole bundle immediately, it uses the OCI layout (`index.json`) contained inside the tarball to locate and reconstruct individual embedded Zarf packages.

### UDS Next

Next inspection supports HCL source definitions, created `.tar.zst` artifacts, unpacked `oci/` layouts, and remote OCI indexes. It reads the bundle-definition manifest and ordered values layers, resolves the selected architecture, and forms each embedded package's verified Package View. It does not fetch embedded image blobs. Archive inspection reads selected entries without extracting the full OCI image payload.

For a remote Next bundle whose package source is OCI, Neutrino checks the source manifest against the embedded package config and every overlapping titled descriptor, then selects needed package layers from that source. This completes sparse embedded manifests (for example, an omitted chart component) without fetching `images/**`. Non-OCI and local artifact packages continue to use their embedded descriptors.

A package artifact can omit a chart-bearing component explicitly marked `required: false` in `zarf.yaml`. Neutrino excludes such components from the Package View, chart inspection, and expected chart count; it does not fetch their chart URLs. A missing required (or unspecified) chart component remains an incomplete package error.

HCL locals, package references, and dependencies are resolved before inspection. Duplicate package IDs, missing dependencies, cycles, missing selected descriptors, or digest/size failures stop Next Bundle Inspection. Source packages are inspected in deterministic dependency-first order.

`defaults.uds.hcl` variables are overlaid by an explicit `--config` file. Bundle values files use the standard Go template `.vars` context. Known inputs are rendered and merged into Package Values in declaration order; missing template inputs are identified as unresolved. These remain configuration inputs, not a simulated deployment.

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

Neutrino uses [Zarf v0.87.0](https://github.com/zarf-dev/zarf/releases/tag/v0.87.0). It reads both `zarf.dev/v1alpha1` (also used when `apiVersion` is omitted) and `zarf.dev/v1beta1`. For multi-document definitions, the highest supported API version wins; duplicate supported versions fail inspection. Package API metadata is retained in reports. Reading a definition does not execute package actions or resolve component imports.

### Remote OCI

The Neutrino-owned Package Reader resolves a digest-pinned Package Root and fetches only verified selected descriptors: `zarf.yaml`, chart-bearing component archives, and assembled package values/schema when present. It never fetches `images/**`. Selected missing or corrupt descriptors fail inspection rather than falling back to a full pull.

### Local Archives and Directories

Local `.tar.zst` archives are extracted to cache. If a local directory is provided, Neutrino reads it directly.

### Component Extraction

Packaged inspection uses one selected-chart layout implementation in `src/pkg/helm/introspect/packaged_charts.go`, shared by Package Reader validation and Zarf consumption. Direct Zarf, Legacy materialized packages, and Next materialized packages use the same consumption path; bundle callers do not recount charts.

1. Read the selected `zarf.yaml` document in declaration order. Select every chart-bearing component present as `components/<name>.tar`; omit an absent component only when explicitly `required: false` in v1alpha1 or `optional: true` in v1beta1.
2. Resolve chart archives (`<name>-<version>.tgz` or `<name>.tgz`) or chart directories containing `Chart.yaml`. Component archives may put `charts/` and `values/` at their root or under the component name.
3. Require every selected chart and every managed static or templated values file. Consume static YAML inputs in declaration order; retain runtime templates as unresolved Package Values usages. v1beta1's `valuesFiles` entries can interleave plain files and files with `enableTemplating: true`; that order is preserved. Legacy `valuesFiles` precede `templatedValuesFiles` as in Zarf's v1alpha1 conversion.
4. Invoke [Helm introspection](helm-introspection.md) for every selected chart. Missing content, extraction failures, or chart-analysis failures stop packaged inspection; packaged charts never fall back to source paths or chart URLs.
5. Map `###ZARF_VAR_*###` markers back to package variables, and retain Package Values mappings separately. Built packages use assembled root `values.yaml` and `values.schema.json`.

Source directories retain their separate behavior: declared source values/schema paths, chart `localPath` (v1alpha1) or `local.path` (v1beta1), and chart URLs remain available. v1beta1 packaged chart files use `<name>.tgz` and `<name>-<index>`; legacy files retain `<name>-<version>` stems. A component archive within a directory uses packaged chart layout rules rather than source fallback.

The Package Reader requires declared package values content to be present. Zarf v0.87.0 can omit assembled `values.yaml` when the merged map is empty; such a package currently fails with a missing-values diagnostic instead of being treated as a complete empty Package View. A discovered schema is reported as evidence; schema presence does not mean values have been validated against it.

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
