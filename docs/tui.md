# Terminal workspace

Neutrino's interactive workspace is a static configuration catalog. Browse configured package values, chart defaults, mappings, and observed template references without supplying live deployment configuration.

Inspection opens the workspace automatically when both input and output are terminals. Use `-i` to require it:

```bash
neutrino inspect ./my-package -i
```

Explicit `--output table|json|yaml` and `--interactive=false` print reports. Pipes, redirected streams, and `TERM=dumb` do not enter the TUI automatically. `-i` reports an error when no usable terminal is attached or `--output` is also supplied.

## Workspace layout

At 120 columns and wider, **Settings** and **Inspector** share the screen. **Scope** opens when focused with `f` or pane navigation, then collapses when focus leaves it. Its tree selects a package, component, or chart and retains its cursor and expanded branches when reopened. Settings contains the query and results; moving its selection updates the Inspector immediately. `Tab` and `Shift+Tab` move between panes, and the focused pane has a highlighted border.

From 80 to 119 columns, Settings and Inspector remain side by side and `f` opens the scope picker. Below 80 columns, one pane is visible at a time: `Enter` opens the Inspector and `Esc` returns to Settings. Resizing changes the layout while retaining the scope, query, selected setting, and inspector history.

## Find a setting

Press `f` to focus the Scope tree at wide widths or open its picker at narrower widths. In the tree, use the right arrow to expand a branch and the left arrow to collapse it or move to its parent. Press `Enter` to apply the selected scope and return to Settings. `Esc` closes Scope and returns to the pane that opened it.

Settings has seven type tabs for every artifact: **Bundle vars**, **Bundle files**, **Zarf vars**, **Zarf values**, **Mappings**, **Helm**, and **Overrides**. Use `1`–`7`, or left/right while Settings is focused, to change type. The **All** tab (`8`) stays visible when narrower layouts show only neighboring types. Inspection starts on the artifact's own configuration surface. `0` opens an inspection overview.

Press `/` to search the selected type and scope; terms combine with AND. Examples include `pkg:api replica`, `chart:gateway`, `source:production.yaml`, and `status:changed`. Changing tabs preserves the query. Choose **All** explicitly to search across types. The changed and unresolved shortcuts also retain the selected type.

**Mappings** includes declared Package Values mappings and observed template references. Their kinds distinguish mappings from runtime uses; an observed reference is not itself a configurable declaration. Identical chart names in different packages retain separate owners. Source filenames are available through **Sources**.

Zarf values use an indented object hierarchy. A nonempty object is a **group**, and its children show relative names rather than repeating the parent's path. Empty objects, arrays, explicit nulls and scalar values remain inputs with their own mapping status. Full paths remain available for search, inspection and copying. Search retains matching inputs' ancestors as context. No mapping is inferred from a name: an optional component absent from the inspected package can leave real inputs without an inspected mapping.

The Inspector names each package value's surviving inspected origin: packaged, bundle, operator, mixed, or unknown. **Known input** means an unresolved source prevents claiming a final value. Equal overlays still own their input even when they do not change it. Bundle file rows show their declared filenames and inspection status; packages with no such files remain in Scope without adding an empty Settings row.

Use origin and kind filters such as `kind:packaged`, `kind:files`, or `kind:configurable`. Long paths and values scroll independently on the focused selected row after a brief pause; navigation, resizing, or changing focus resets scrolling. Search, help, and the command palette pause it.

The catalog includes:

- Configured Package Values, with structural object groups for navigation.
- Helm defaults from chart values files, observed template paths, and explicit chart schema properties.
- Bundle `.vars` paths referenced by values files, including paths absent from defaults or configuration.
- Legacy Zarf variable declarations, including unused declarations in standalone packages.

A schema default is an **annotation**, separate from a supplied value or chart default. **Not supplied** differs from explicit `null`, `false`, `0`, or an empty string. A required property is required in its parent object; it is not necessarily required when that parent is absent. Dynamic Helm paths are marked as dynamic rather than resolved defaults.

Schema-only Zarf properties stay out of Settings. Press `S` on a package in Scope or one of its settings to view its entire schema, including properties without supplied inputs. The command palette also provides **View package schema**. Lowercase `s` opens the selected setting’s schema. Static schema inventory follows explicit `properties`; raw references, composition, and other constraints remain available in the schema view; the inventory does not expand every possible schema path or validate the inputs. Runtime enrichment is optional and is not needed to browse this catalog.

## Read a setting

The Inspector follows the selected setting and shows its name, owner, kind, known value or absence, available description, and proven mapping actions. Press `Enter` from Settings to focus the Inspector; at wide and medium widths the surrounding workspace remains visible. Long descriptions and values have separate views within the Inspector. Additional evidence does not fill the primary screen just because the terminal is wide.

For example, a legacy `.Variables.CA_BUNDLE_CERTS` reference appears as a **Zarf runtime reference**, with **Requires deployment context** and a Sources action. It is not renamed to Package `.Values`, and no Helm destination is invented. `.Constants`, `.Pkg`, and unsupported static template expressions say **Expression not evaluated**. An empty target on a template reference is not a chart-root mapping.

Secondary views are explicit actions:

| Action | Evidence |
| --- | --- |
| **Sources** (`o`) | Friendly source roles and known line numbers. Select a source to see its exact path; an existing local source can then open in `$EDITOR`. Unavailable coordinates are not displayed as `:0:0`. |
| **Full value** (`v`) | Copyable YAML for the inspected value, with sensitive values masked. |
| **Input history** (`t`) | Inspected source layers from low to high precedence, shadowed contributions, and the inspected result when all layers are known. |
| **Schema** (`s`) | Raw schema annotations, with sensitive defaults and examples masked. Schema presence does not mean validation ran. |
| **Values files using this variable** | File-level bundle variable references. A file reference does not prove which output leaf an expression generates. |
| **Other inputs in source files** | Other observed bundle inputs read by the package's values files; these are not dependencies of the selected leaf. |
| **Package references / Observed uses** | Package source, ordering metadata, or separately recorded template usage. |
| **Authoring example** (`b` or `Space`) | Optional Package Values or legacy UDS configuration example to review before use. |

Only applicable actions appear. With the Inspector focused, `j`/`k` selects an action and `Enter` follows it. `Esc` returns to the preceding inspector page and restores its selection and scroll position, then returns focus to Settings. Moving to a different setting starts that setting's summary; changing focus or resizing alone preserves the current evidence view. Short terminals keep the primary identity and value concise; secondary evidence can be scrolled.

## Explore mappings

A Package Value's inspector links to its declared chart destinations. Selecting a destination opens the chart mapping table at the corresponding path in the Settings area, with the selected mapping row shown in the Inspector. Inspect a declaration in **Mappings & uses** and follow its mapping action to explore it, even when its input is empty or absent. `Esc` restores the originating settings or inspector state.

The mapping view separates **Chart default** from **Package input**; the Inspector shows the selected path's input when the table is too narrow for both. **Available** means a chart path is reachable through the declaration, not that an input was supplied or deployment succeeded. **Excluded** paths retain their exclusion reason. Press `a` to show chart paths outside a subtree destination. An explicit `targetPath: .` is a chart-root mapping; a missing target is not.

Chart values opened directly and through mapping rows share the same inspector. Proven mappings show their owning package input paths. Multiple mappings remain separate; the inspector does not select a final Helm winner from incomplete evidence.

`y` copies the selected value path. In a mapping table it copies the corresponding package input path; in Details, `p` copies the selected mapping's package input path. Copying a path never copies its value. Clipboard integration uses terminal OSC 52.

Authoring examples use supplied input when known, otherwise a known chart default or a marked `<set-value>` placeholder. Schema defaults are not silently inserted. Excluded or unresolved mappings do not produce a misleading example. Examples represent configuration input and must be reviewed before use.

## Keyboard reference

| Key | Action |
| --- | --- |
| `f` | Focus Scope at wide widths; open the scope picker at narrower widths |
| Left / right in Scope | Collapse or move to parent / expand branch |
| `/` | Search within the selected type and scope |
| `1`–`7` / left-right in Settings | Select a setting type |
| `0` / `8` | Overview / all inputs |
| `c` / `u` | Toggle changed / unresolved filter |
| `Tab` / `Shift+Tab` | Cycle workspace panes; open or collapse Scope as focus changes |
| `j` / `k`, arrows | Move through rows or actions; scroll evidence |
| `g` / `G` | First / last item |
| `PgUp` / `PgDn` | Move by a viewport |
| `n` / `N`, `]` / `[` | Next / previous component or group |
| `Enter` | Apply a scope, focus the selected setting's Inspector, or follow its selected action |
| `Esc` | Back, close an overlay, or clear a filter |
| `o` | Browse source locations |
| `v` / `t` / `s` | Full value / input history / schema, when applicable |
| `S` | Whole package schema from Scope or a package setting |
| `b` / `Space` | Authoring example in the inspector |
| `a` | Show/hide other paths in a mapping table |
| `y` / `p` | Copy value path / selected mapping input path |
| `Z` / `U` | Toggle legacy package-variable / bundle-override summaries |
| `:` / `?` | Command palette / keyboard help |
| `q` | Quit |

## Inspection boundaries

Bundle `.vars`, Zarf Package `.Values`, Helm `.Values`, and legacy variables are distinct inputs. `depends_on` specifies package ordering, not value transfer. Neutrino does not execute runtime actions or arbitrary template functions. Changed/unresolved filters describe inspected evidence: missing inputs, dynamic paths, unsupported expressions, unavailable files, and parse failures remain distinguishable.

History covers the files available to inspection. Created Zarf artifacts expose an assembled `values.yaml`; their original source-file history cannot be recovered. Unknown layers prevent a definitive inspected winner. Final Helm values also depend on chart files, templates, legacy substitution, overrides, and deployment flags.

The inspector uses Zarf **v0.87.0**. Legacy v1alpha1 variables/constants remain separate from beta Package Values. v1alpha1 chart values files precede templated files; v1beta1 has one ordered list that can interleave the two. UDS Next includes `defaults.uds.hcl` and the supplied `--config`; it does not simulate the deploy `--set` layer.

Credential-shaped values, explicitly sensitive legacy variables, and schema-marked sensitive values are masked. Masking uses the owning chart or package schema, including local references and composition. Remote, unresolved, or cyclic schema references are not fetched; affected values are conservatively redacted. Raw inspection data is not modified by display masking.
