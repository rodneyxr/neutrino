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
- **Infer nested paths** from `values.yaml` keys that weren't directly referenced in templates (unless `--no-values` is set).

### 4. Subchart Discovery

For charts with dependencies, Neutrino:

1. Reads `Chart.yaml` to identify all declared dependencies (with their aliases).
2. Recursively loads each subchart from `charts/`.
3. Walks each subchart's templates independently.
4. Prefixes discovered value paths with the dependency alias (e.g. for a dependency aliased `redis`, the path `master.replicas` becomes `redis.master.replicas`).

### 5. Zarf Variable Extraction

Alongside Helm value introspection, the parser scans all template content for Zarf injection markers:

```
###ZARF_VAR_DOMAIN###
```

These are matched against the variables defined in the host `zarf.yaml`. Each marker is recorded with its source location, and mapped to the variable's default value and description from the Zarf package definition.

!!! info
    Zarf variables are displayed separately from Helm values in both the table output and the TUI, making it easy to distinguish between Helm-level and Zarf-level configuration.
