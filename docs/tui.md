# Interactive TUI

Neutrino includes a full-screen interactive terminal UI built with [Bubble Tea](https://github.com/charmbracelet/bubbletea). It lets you explore, search, and navigate every discovered value across all components without leaving your terminal.

## Launching the TUI

Add the `-i` flag to any `inspect` command:

```bash
neutrino inspect oci://ghcr.io/zarf-dev/packages/init:v0.74.0 -i
```

!!! tip
    The TUI automatically resolves all file paths to absolute paths so that the editor-jump feature works correctly.

## Views

### List View

The main view displays all discovered values in a tabular format, organized by component. Each row shows:

| Column | Description |
|:---|:---|
| **Path** | The dot-notation value path (e.g. `image.repository`) |
| **Default** | The default value from `values.yaml`, if found |
| **Type** | `field`, `variable`, `override`, or `inferred` |
| **Location** | Source file and line number, or `(N locs)` if consolidated |

Within each component section, items are displayed in this order:

1. **Zarf variables** (`###ZARF_VAR_*###` markers) — shown at the top of each component.
2. **UDS Override Values** — hard-coded values set by the bundle's `overrides.*.values` stanza.
3. **UDS Override Variables** — user-configurable variables defined by the bundle's `overrides.*.variables` stanza.
4. **Chart values** — all other Helm template value references.

### Summary Panels

Two optional summary panels can be toggled independently:

- **Zarf Package Variables** (press `Z`) — Shows all `zarf.yaml` package-level variables across every package, displayed at the top of the list.
- **UDS Bundle Overrides** (press `U`) — Shows a cross-package summary of all UDS override values and variables, displayed after the Zarf summary.

### Detail View

Press `Enter` on any row to open the detail view. This shows:

- For **Helm values**: All template locations (file, line, column) where the value is referenced, followed by any UDS overrides that target the same path ("Overrides" section).
- For **UDS Override Values / Variables**: The affected template locations where the targeted Helm path is used ("Affected Template Locations"), plus a cross-reference link to the corresponding Helm value entry ("Related Values").
- For **Zarf variables**: All template locations where the `###ZARF_VAR_*###` marker appears.

Press `Enter` on a **location** entry to open the file in your editor. Press `Enter` on a **cross-reference** entry to jump to that item in the list.

#### UDS Authoring Snippets

When inspecting a UDS bundle, the detail view also displays ready-to-copy YAML snippets to help you author bundle and config files:

**For Helm values** — a `uds-bundle.yaml` snippet is shown at the bottom of the detail view with the full YAML path pre-filled so you know exactly where to place it:

```yaml
# uds-bundle.yaml
packages:
  - name: core-base
    overrides:
      istio-controlplane:
        istiod:
          variables:
            - name: <prefix>_GLOBAL_PROXY_RESOURCES_REQUESTS_MEMORY  # variable name can be anything you choose
              path: global.proxy.resources.requests.memory
              description: "<description>"
              default: 56Mi
```

The suggested `name` is derived from the Helm path (uppercased, dots replaced with underscores) prefixed with `<prefix>_` as a placeholder — rename it to whatever makes sense for your bundle. The `default` field is populated from the chart's `values.yaml`; if the chart has no default for this path, the line is omitted entirely.

**For UDS Override Variables** — a `uds-config.yaml` snippet is shown so you know exactly how to set that variable when deploying the bundle:

```yaml
# uds-config.yaml
variables:
  core-base:
    ISTIO_REQUESTS_MEMORY: <value>
```

The package name and variable name are taken directly from the bundle definition, so no guessing is required.

## Keybindings

### Navigation

| Key | Action |
|:---|:---|
| `j` / `↓` | Move cursor down one row |
| `k` / `↑` | Move cursor up one row |
| `J` | Move cursor down 5 rows |
| `K` | Move cursor up 5 rows |
| `g` | Jump to top of list |
| `G` | Jump to bottom of list |
| `PgDn` | Move cursor down one page |
| `PgUp` | Move cursor up one page |
| `n` | Jump to the first row of the **next** component |
| `b` | Jump to the first row of the **previous** component |
| `Enter` | Open detail view / open file in editor / jump to cross-reference |
| `Esc` | Back to list view / clear filter |

### Search & Filter

| Key | Action |
|:---|:---|
| `/` | Enter filter mode — type to search value paths |
| `Enter` | Confirm filter and return to normal navigation |
| `Esc` | Cancel filter and clear search |

The filter provides autocomplete suggestions as you type, matching against all known value paths.

### Component Selection

| Key | Action |
|:---|:---|
| `:` | Enter component selection mode |
| `↑` / `↓` | Cycle through matching components |
| `Enter` | Confirm component selection |
| `Esc` | Cancel and return to previous component |

Typing in component selection mode filters the available components. Leave the input empty and press `Enter` to show "All Components".

### Toggles

| Key | Action |
|:---|:---|
| `c` | Toggle consolidation mode (group multiple locations per value vs. expand each) |
| `Z` | Toggle the Zarf Package Variables summary panel at the top of the list |
| `U` | Toggle the UDS Bundle Overrides summary panel (override values and variables across all packages) |
| `y` | Open the first location of the selected value in your editor |
| `q` / `Ctrl+C` | Quit |

## UDS Bundle Override Display

When inspecting a UDS Bundle, the TUI surfaces two additional item types within each component:

### Override Values

Hard-coded values set directly in the bundle's `uds-bundle.yaml` via the `overrides.<component>.<chart>.values` stanza. These are **not** overridable via `uds-config.yaml`. Columns shown:

| Column | Description |
|:---|:---|
| **UDS Override Path** | The Helm values path being overridden |
| **Value** | The value explicitly set by the bundle |
| **Original Default** | The original chart default, if found |

### Override Variables

User-configurable variables exposed by the bundle via the `overrides.<component>.<chart>.variables` stanza. These **can** be set or overridden by the end-user's `uds-config.yaml`. Columns shown:

| Column | Description |
|:---|:---|
| **UDS Variable** | The variable name (e.g. `ISTIO_REQUESTS_MEMORY`) |
| **Path** | The Helm values path this variable controls |
| **Description** | Human-readable description, if provided |

In the detail view, both override types show which template files reference the affected Helm path, making it easy to trace the full flow from bundle operator → chart template.

## Editor Integration

When you press `Enter` on a location in the detail view (or `y` in the list view), Neutrino opens the file in your default editor at the exact line and column. It looks for the `EDITOR` environment variable and falls back to common editors (`code`, `vim`, `nano`).

!!! note
    For VS Code, Neutrino invokes `code --goto <file>:<line>:<column>` to jump directly to the source.
