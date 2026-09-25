# Interactive TUI

Neutrino includes a full-screen interactive terminal UI built with [Bubble Tea](https://github.com/charmbracelet/bubbletea). It lets you explore, search, and navigate every discovered value across all components without leaving your terminal.

## Launching the TUI

Add the `-i` flag to any `inspect` command:

```bash
neutrino inspect oci://ghcr.io/zarf-dev/packages/init:v0.74.0 -i
```

!!! tip
    The TUI automatically resolves all file paths to absolute paths so that the editor-jump feature works correctly.

## Workspace

The interactive inspector uses a keyboard-first two-pane workspace and a full-width details view:

| Surface | Purpose |
|:---|:---|
| **Components** | Select all components or narrow the workspace to one component. Expands when focused to show full package and component names. |
| **Values** | Search and navigate discovered paths, values, types, and source locations. This pane has focus when the TUI opens. |
| **Details** | Open the selected value full-width to inspect defaults, effective overrides, template locations, cross-references, and optional UDS authoring snippets. |

The focused pane uses a double border. Selection markers and pane titles preserve meaning in terminals that cannot display color.

### Responsive layout

Neutrino changes layout based on available terminal width:

| Width | Layout |
|:---|:---|
| **120 columns or wider** | Components and a wide Values table are visible together. |
| **80–119 columns** | Components and Values remain visible with a narrower Values table. |
| **Below 80 columns** | One workspace pane is visible at a time. Use `Tab`, `Shift+Tab`, `h`, or `l` to switch between Components and Values. |

### Values

The Values pane displays each discovered value in a dense table. Wider terminals show:

| Column | Description |
|:---|:---|
| **Value Path** | Dot-notation value path, such as `image.repository`. Selected long paths scroll horizontally automatically with pauses at the beginning and end. |
| **Value** | Chart default, legacy override, configured package input, or an explicit unresolved runtime marker. |
| **Type** | Helm usage type or a Package Value mapping status: `mapped`, `excluded`, `unmapped`, `runtime`, `mixed`, or `declared`. |
| **Source** | Source file and line, or a consolidated location count. |

Rows are grouped under a full-width component band, with muted sentence-case labels and a blank line separating **Zarf variables**, **UDS override values** (fixed by the bundle), **UDS variables** (configurable via `uds-config.yaml`), and **Helm chart values**. Package-wide Zarf variables and bundle-wide override summaries have their own bands. When scrolling into the middle of a component, its band and the current section label remain visible. Repeated template locations remain consolidated in the Values pane; Details lists every location.

**Zarf Package Values** appear as configured leaf paths in All Charts, including packages with no Helm charts. The Type column labels their aggregate Helm mapping status: **mapped** (green), **excluded** (red), **unmapped** (grey), **runtime** (amber), or **mixed** (amber, when mappings differ). On narrow terminals the status prefixes the Value column. Labels, not color, carry meaning; **unmapped** does not rule out manifest/action uses. Chart mapping declarations are also directly selectable even when the configured source is `{}` or missing; multiple chart destinations have separate rows. Search includes package IDs, mapping targets, excluded paths, and values-file names.

### Details

Selecting a value and pressing `Enter` opens full-width Details. The value path is the title, with a compact package/component/chart breadcrumb and value type underneath; unavailable identity levels are omitted. Shaded YAML blocks separate defaults from configured inputs. Short section headings organize configuration mappings, template locations, related values, and input-file provenance. Descriptions and long metadata wrap rather than disappear. UDS variables show the Helm path they control; UDS override values retain separate chart-default and effective-value blocks. Bundle authoring mode (`b` or `Space`) adds a UDS authoring snippet. Source paths remain absolute internally for editor jumps, but Details displays them relative to the chart.

Package Values with one chart mapping open the chart-first table directly on `Enter`, including inputs such as `istio-controlplane.base: {}`. Values with multiple mappings open Details so you can select a destination. You can also open declaration rows under **Chart mappings · explore configurable values**.

The table is scoped to the owning package, component/chart, and `sourcePath` → `targetPath` declaration. A root destination exposes all discovered chart value paths even when package input is empty or missing; a subtree destination exposes its discovered descendants.

| Column/status | Meaning |
|:---|:---|
| **Chart path** | Destination Helm value path. Supplied-only paths absent from chart discovery are explicitly labeled. |
| **Chart default** | Value known from chart inspection; `—` means unknown, not an empty override. |
| **Package input** | Currently supplied package input, shown separately on wide terminals; `not supplied` is distinct from `null`, `false`, `0`, or an empty value. |
| **Available** (green) | Reachable through this declaration, not necessarily supplied, schema-approved, or deployment-effective. |
| **Excluded** (red) | Matches a fully prefixed package `excludePaths` entry. |
| **Other** (grey) | A discovered chart path outside this mapping's destination. Press `a` to show/hide these rows. Root mappings already cover all chart paths. |

Chart values and mapping-table rows open the **same Details view**. **Configure through Package Values** shows each owning-package mapping's status, declared route, exact **Set input at** path, supplied value (or absence), and exclusion reason. Root destinations are labeled **chart root (.)**. **Input files** appears after source locations and related values, lists files in declared order, and is shown once per package rather than repeated for each mapping. For Next bundles, the package label is the bundle package ID. Discovery and mapping do not simulate deployment or resolve runtime inputs.

Use `j` / `↓` and `k` / `↑` to scroll Details and select mappings, source locations, or related values. The footer describes the selected action: explore a mapping, edit a source, or inspect a related value. `Enter` performs that action; `o` opens the first source location. Selection stays visible as you navigate. A bottom position indicator shows the visible line range and retains the value path when its title has scrolled away. `g` returns to the beginning; `G` moves to the end, including input provenance and any UDS authoring snippet.

In Details, `y` copies the chart value path; `p` copies the selected mapping's package input path. In the mapping table, `y` still copies the package input path. `Esc` returns to the originating table or workspace; nested mapping links preserve the preceding Details selection.

### Command palette

Press `:` from either workspace pane to open the searchable command palette. The shortcut key is the first column, followed by the action and description. Every primary action is available there, including filtering, component selection, summaries, bundle authoring, editor integration, copying paths, help, and quit.

Action feedback in the footer returns to the default keyboard hints after about two seconds. A newer message restarts that interval.

## Keybindings

### Focus and navigation

| Key | Action |
|:---|:---|
| `Tab` / `Shift+Tab` | Switch between Components and Values. |
| `h` / `←` | Focus the Components pane (expands the left column). |
| `l` / `→` | Focus the Values pane (collapses the left column). |
| `j` / `↓` | Move down 1 row in the focused pane. |
| `k` / `↑` | Move up 1 row in the focused pane. |
| `J` / `K` | Move 5 rows down or up in the focused pane. |
| `n` / `N` (also `]` / `[`) | Jump to the next or previous component. |
| `g` / `G` | Move to the first or last item in the focused pane. |
| `PgDn` / `PgUp` | Move by the available viewport height. |
| `Enter` | Select a component, open Details, follow a mapping link, inspect a mapping row, or open a location/cross-reference. |
| `Esc` | Return from a mapping row to its table, then to the originating Details/Values; close an overlay or clear the active value filter. |

### Search and commands

| Key | Action |
|:---|:---|
| `/` | Filter values by path. |
| `f` | Filter and select a component. |
| `:` | Open the searchable command palette. |
| `?` | Open context-sensitive keyboard help. |

Input modes preserve arrow keys for suggestions and selection. `Enter` applies the input; `Esc` cancels it.

### Actions and toggles

| Key | Action |
|:---|:---|
| `Space` | Select a component when Components is focused, or toggle bundle authoring in Details. |
| `a` | In a Package Value mapping table, show/hide discovered chart paths outside the selected destination. |
| `Z` | Toggle the Zarf package variable summary. |
| `U` | Toggle the UDS bundle override summary. |
| `b` | Toggle bundle authoring snippets in Details. |
| `o` | Open the first source location for the selected value in `$EDITOR`. |
| `y` | Copy the selected value path using terminal OSC 52; mapping tables copy the package input path. |
| `p` | In Details, copy the selected mapping's package input path. |
| `q` / `Ctrl+C` | Quit. |

OSC 52 copying works through compatible local terminals and SSH sessions without requiring a desktop clipboard service. Terminal policy may require enabling OSC 52 support.

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
