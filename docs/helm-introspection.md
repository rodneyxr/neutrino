# Helm Introspection

Neutrino doesn't just parse `values.yaml` files inside a Helm chart to discover configuration options. Modern charts heavily rely on conditionally defining `.Values` access dynamically within `templates/` using complex Sprig templating.

If an introspection tool only reports what is defined in `values.yaml`, it misses crucial override functionality embedded deep in `deployment.yaml` hooks and custom Sprig routines. 

Neutrino bypasses this limitation with its custom **Template Walker**.

## The Template Walker

When Neutrino successfully fetches and decompresses a Helm Chart, it merges any Zarf-provided configuration files on top of the default `values.yaml`. Instead of stopping there, the analysis engine looks closely at the resulting templates.

### 1. Pseudo-Execution via Sprig Substitution
Neutrino uses a synthetic rendering pass utilizing `Masterminds/sprig` augmented with core Helm directives (like `include`, `tpl`, `toYaml`).
This ensures Neutrino doesn't crash on standard Helm operations while attempting to parse AST blocks.

### 2. AST Value Discovery Traversal
Instead of compiling the rendered template string, Neutrino parses the template's AST (Abstract Syntax Tree), simulating an execution walk.
During this walk, Neutrino spots any invocation of `.Values`.

```go
{{ .Values.image.repository }}
```
Here, neutino captures `image.repository`, tying its occurrence directly to the line and column in `deployment.yaml`. 

### 3. Nested Discovery and Alias Mapping
For Subcharts, discovering configuration keys can be complex since paths are implicitly prefixed by the Chart's dependency name or alias. 
Neutrino intercepts these dependency boundaries, prefixes discovered values with the alias names detected in `Chart.yaml`, and maps their default values perfectly back to the main chart's `values.yaml` hierarchy.

### 4. Zarf Variable Extraction
Alongside Helm introspection, the parser recursively looks for standard Zarf injection syntaxes (e.g. `###ZARF_VAR_...###`) inside templates. It maps them up to the variables defined globally within the host `zarf.yaml`.
