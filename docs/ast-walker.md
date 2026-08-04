# AST Walker

The AST Walker is Neutrino's core analysis engine. It traverses the parsed Go template AST to track every reference to `.Values`, resolving complex cases like variable assignments, context rebinding, and dynamic iteration.

## Architecture

The walker is implemented in `src/pkg/helm/introspect/walker.go`. It maintains two key pieces of state:

### Dot Path Stack

The `dotPathStack` tracks what `.` (dot) currently refers to at any point in the template. In standard Helm templates, dot starts as the root context. Inside `with` and `range` blocks, dot is rebound to a sub-path:

```go
// At root level:      dot = "" (root context)
// Inside with .Values.image: dot = "Values.image"
// Inside range .Values.hosts: dot = "Values.hosts[]"
```

The stack accurately handles nested scopes — when a `with` or `range` block exits, the previous dot context is restored.

### Variable Bindings

The `variables` map tracks Go template variable assignments:

```go
{{ $svc := .Values.service }}
```

After this assignment, the walker knows that `$svc` resolves to `Values.service`. Any subsequent access like `{{ $svc.port }}` is resolved to the full path `service.port`.

## Node Handling

The walker processes each AST node type:

### `FieldNode` — Direct Value Access

Handles `.Values.foo.bar` and dot-relative access inside `with`/`range` blocks.

```go
// .Values.image.repository → records "image.repository"
// Inside `with .Values.image`:
//   .repository → records "image.repository"
```

### `VariableNode` — Variable Dereferencing

When encountering `$var.field`, the walker checks if `$var` was previously assigned to a `.Values` path and resolves it:

```go
{{ $svc := .Values.service }}
{{ $svc.port }}
// → records "service.port"
```

### `RangeNode` — Dynamic Iteration

For `range` blocks over `.Values` paths, the walker pushes a wildcard scope:

```go
{{ range .Values.ingress.hosts }}
  {{ .host }}
{{ end }}
// → records "ingress.hosts.*.host"
```

The `[]` suffix in the dot path stack is converted to `*` in the final output.

### `WithNode` — Context Rebinding

For `with` blocks, the walker pushes the new context path without a wildcard:

```go
{{ with .Values.persistence }}
  {{ .enabled }}
{{ end }}
// → records "persistence.enabled"
```

### `CommandNode` — Function Calls

The walker recognizes the `index` function for dynamic key access:

```go
{{ index .Values "some-dashed-key" }}
// → records "some-dashed-key"
```

## Source Attribution

Every discovered value path is recorded with precise source information:

| Field | Description |
|:---|:---|
| **File** | Template file path relative to the chart root |
| **Line** | Line number (1-based), computed from the byte offset |
| **Column** | Column number (1-based), computed from the last newline |
| **Kind** | Usage context: `field`, `variable`, or `value` |

The line and column computation uses the raw template content string and the AST node's `Position()` byte offset.

## Deduplication

If the same value path is discovered multiple times within a chart, the walker merges the locations into a single `ValueUsage` entry. This keeps the output clean while preserving all source references.

When using the `--wildcards` flag, paths containing dynamic segments (from `range` blocks) are included in the output. By default, they are filtered out to reduce noise.
