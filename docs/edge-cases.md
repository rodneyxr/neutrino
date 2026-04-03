# Edge Cases & Workarounds

While standard package inspection runs smoothly, certain patterns in real-world Helm charts and Zarf/UDS packages require special handling. Neutrino implements built-in fallback strategies for these cases.

## `_internal_defaults_do_not_set` Wrapper

Many complex UDS and Zarf packages wrap foundational values inside namespaces like `_internal_defaults_do_not_set`. This prevents end users from explicitly setting keys they shouldn't generally modify.

When Neutrino walks a template and detects `.Values.foo.bar`, it attempts to look up that exact dot-notated path inside `values.yaml` to extract the default value. If the direct lookup fails, Neutrino checks a list of known fallback namespace wrappers:

```go
fallbacks := []string{"_internal_defaults_do_not_set"}
```

Neutrino transparently recurses within `_internal_defaults_do_not_set.foo.bar` and returns the true default value as if the wrapper didn't exist.

!!! note
    This only affects default value resolution. The discovered value _path_ always reflects the actual template usage (e.g. `foo.bar`), not the internal wrapper path.

## Cross-Architecture OCI Manifests

Metadata inspection occasionally encounters OCI manifests published for a different architecture than the host running Neutrino.

For example, pulling an `arm64`-specific manifest on an `amd64` machine will fail if the manifest index doesn't include the host architecture. When Neutrino hits this situation, it automatically invokes an **architecture fallback**:

1. Retries the manifest fetch with an explicit `amd64` architecture identifier.
2. Since Neutrino only reads metadata layers (not actual container images), the architecture mismatch has no functional impact.

!!! tip
    You can avoid this entirely by specifying the architecture explicitly:
    ```bash
    neutrino inspect oci://example.com/package:latest -a amd64
    ```

## Embedded UDS Package Extraction

When UDS bundles embed Zarf packages, those packages exist as OCI `.tar` layouts inside `blobs/sha256/` — they can't be read as normal filesystem archives.

To handle this, Neutrino:

1. Parses `index.json` from the extracted bundle.
2. Correlates the overarching manifest hash.
3. Manually reconstructs the OCI layout for each Zarf package under `.cache/neutrino/<hash>/extracted-packages/`.
4. Hard-links the opaque SHA-256 blobs onto filesystem files bearing their original registry annotations.

This reconstruction allows Neutrino to process each embedded Zarf package as if it were a standalone OCI artifact.

## Template Parsing Resilience

Not all Helm templates are well-formed or follow standard patterns. Neutrino handles several common issues:

| Issue | Strategy |
|:---|:---|
| Unknown Sprig/Helm functions | Registered as no-op stubs returning zero-values |
| Recursive `include` calls | Not followed (analysis stays within each file's AST) |
| Invalid template syntax | Template is skipped with a warning; other templates continue |
| Missing `values.yaml` | Analysis proceeds with empty defaults |
| Custom template functions | Registered dynamically as no-ops during parse |

!!! warning
    Templates that use deeply recursive `tpl` calls within `include` may not have all paths resolved. This is a known limitation of static analysis without full rendering.
