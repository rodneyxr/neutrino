# Helm Introspection

Neutrino doesn't just parse `values.yaml` files inside a Helm chart to discover configuration options. Modern charts heavily rely on dynamically accessing `.Values` within `templates/` using complex Go templating, Sprig functions, and control flow.

If an introspection tool only reports what is defined in `values.yaml`, it misses crucial override functionality embedded deep in deployments, hooks, and custom Sprig routines.

Neutrino bypasses this limitation with its custom **Template Walker**.

## The Introspection Pipeline

When Neutrino loads a Helm chart, it runs through a multi-stage analysis:

### 1. Template Parsing via Sprig Substitution

Neutrino uses a synthetic rendering pass. It registers all standard Helm and Sprig functions (`include`, `tpl`, `toYaml`, `toJson`, `lookup`, and the full Sprig library) as no-op stubs. This ensures every template file parses into a valid AST without requiring:

- A live Kubernetes cluster
- A full rendering context
- Actual secret or ConfigMap data

!!! note
    The mock functions return zero-values. Neutrino never actually renders template output — it only needs a valid parse tree.

### 2. AST Value Discovery

Instead of compiling the rendered template string, Neutrino parses the template's **Abstract Syntax Tree (AST)** and walks it node by node. During this traversal, it identifies every reference to `.Values`:

```go
{{ .Values.image.repository }}
```

This produces the discovered path `image.repository`, tied directly to the source file, line, and column.

The walker handles complex cases including:

- **Nested field access**: `.Values.service.ports.http.port`
- **Variable assignments**: `$svc := .Values.service` → later `$svc.port` resolves to `service.port`
- **Dynamic paths via `range`**: `{{ range .Values.ingress.hosts }}` → `ingress.hosts.*.host`
- **Context rebinding via `with`**: `{{ with .Values.persistence }}` → `.enabled` resolves to `persistence.enabled`
- **`index` function calls**: `{{ index .Values "some-key" }}` → `some-key`

See [AST Walker](ast-walker.md) for the full technical details of the traversal algorithm.

### 3. Values File Merging

Neutrino loads the chart's `values.yaml` and merges any overrides provided by the upstream package (e.g. a Zarf package may supply custom values files). The merged values tree is used to:

- **Extract default values** for every discovered path.
- **Inventory every addressable path** from `values.yaml`, including unreferenced top-level branches and charts with no templates (unless `--no-values` is set). Declared paths remain visible when a supplied file replaces their parent object.

The report's `defaultPresent` field distinguishes an explicit `null` from an absent value. Values represent inspected files, not deployment output. Lists remain whole values; keys containing literal dots are retained in their parent object but do not become ambiguous dot-notation paths.

The known `_internal_defaults_do_not_set` wrapper uses the chart's public paths (for example, `global.proxy.resources`) while preserving the actual YAML source location. Inferred rows do not duplicate the private wrapper tree. Explicit template references are retained as written.

### 4. Schema Inventory

Neutrino retains `values.schema.json` and inventories explicit `properties`, including nested properties with no configured value. Property annotations preserve types, constraints, descriptions, and defaults. Required membership refers to the property's immediate parent; it does not claim that an optional parent exists.

Schema defaults are **annotations only** and never become configured values. References, composition, conditions, patterns, and array item schemas remain in the original schema; this inventory does not resolve them or perform validation. An unreadable JSON schema produces a diagnostic while other chart evidence remains available. `--no-values` suppresses additional schema/default rows but still attaches available annotations to template-discovered paths and retains the original schemas.

### 5. Subchart Discovery

For charts with dependencies, Neutrino:

1. Reads `Chart.yaml` to identify all declared dependencies (with their aliases).
2. Recursively loads each subchart from `charts/`.
3. Walks each subchart's templates independently.
4. Prefixes discovered value paths with the dependency alias (e.g. for a dependency aliased `redis`, the path `master.replicas` becomes `redis.master.replicas`).
5. Includes dependency defaults and explicit schema properties using the same alias prefixes. Parent file overrides keep their normal map-merge or scalar/list/null replacement behavior. Disabling subchart inspection also disables dependency schema/default discovery.

### 6. Zarf Variable Extraction

Alongside Helm value introspection, the parser scans all template content for Zarf injection markers:

```
###ZARF_VAR_DOMAIN###
```

These are matched against the variables defined in the host `zarf.yaml`. Each marker is recorded with its source location, and mapped to the variable's default value and description from the Zarf package definition.

!!! info
    Zarf variables are displayed separately from Helm values in both the table output and the TUI, making it easy to distinguish between Helm-level and Zarf-level configuration.
