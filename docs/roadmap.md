# Roadmap

## Completed

- [x] **OCI Support** — Directly inspect charts from OCI registries (`oci://`).
- [x] **Zarf Package Inspection** — Analyze `zarf.yaml` to map Zarf variables to Helm values.
- [x] **Interactive TUI** — Explore values in a searchable terminal interface with keybinding navigation and editor integration.
- [x] **UDS Bundle Inspection** — Analyze UDS bundles containing multiple Zarf packages, pulling only metadata layers.
- [x] **Multi-Format Output** — Table, JSON, and YAML output formats.
- [x] **Caching** — Hash-based local caching for fast repeat inspections.
- [x] **Zarf Variable Extraction** — Discover `###ZARF_VAR_*###` markers in templates and map them to `zarf.yaml` definitions.
- [x] **Subchart Analysis** — Recursive introspection of chart dependencies with alias prefixing.
- [x] **UDS Bundle Override Mapping** — Deep analysis of UDS Bundle overrides to map out all configurable variables exposed by a bundle. Surfaces hard-coded override values and user-configurable override variables with cross-references to the underlying chart defaults, streamlining `uds-config.yaml` authoring.

## In Progress

## Planned

- [ ] **`uds-config.yaml` Generation** — Automatically generate a `uds-config.yaml` skeleton from a bundle's discovered variables, pre-populated with defaults and descriptions as comments.
- [ ] **Local Zarf and UDS Bundle Directory Support** — Inspect local directories containing a `zarf.yaml` or `uds-bundle.yaml` file, including support for recursive imports.
- [ ] **Linter Integration** — Warn about values documented in `values.yaml` but never used in templates (dead configuration).
