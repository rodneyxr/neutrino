# Edge Cases & Workarounds

While standard package execution runs efficiently, many edge cases arise when dealing with deeply nested architectures, OCI index limitations, and Helm value overrides.

Neutrino implements several built-in fallback strategies for improved stability.

## `_internal_defaults_do_not_set` Value Wrapper Workaround

Many complex UDS and Zarf packages wrap foundational values inside namespaces like `_internal_defaults_do_not_set`. This keeps end users from explicitly defining keys they shouldn't generally modify.

When Neutrino walks a template and detects `.Values.foo.bar`, it attempts to look up that exact dot-notated path inside `values.yaml` to extract the default. If the direct lookup fails, Neutrino checks an internal list of common fallback namespace overrides, such as:

```go
fallbacks := []string{"_internal_defaults_do_not_set"}
```

Neutrino transparently recurses within `_internal_defaults_do_not_set.foo.bar` and returns the true default value as if the wrapper didn't exist.

## Cross-Architecture Image Parsing Failures

Metadata inspection occasionally trips over OCI manifests that were published for a different architecture than the host running Neutrino.

For example, pulling an `arm64`-specific manifest component on an `amd64` machine. When Neutrino accesses an OCI remote layer that explicitly rejects the OS/architecture, the runtime automatically invokes an **Arch Fallback Check**.
It re-fetches the manifest leveraging an enforced `amd64` architecture identifier to inspect the metadata layers successfully. Since Neutrino only reads metadata logic and `uds-bundle.yaml` layers, the architecture discrepancy inherently does not matter during introspection. 

## Bundled UDS Packages Extraction Tricks

When UDS bundles their included Zarf packages, those packages represent internally embedded `.tar` layouts inside `blobs/sha256/` that cannot be read normally. 

When extracting a `uds-bundle-*.tar.zst` locally, Neutrino parses `index.json`, correlates the overarching manifest hash, and manually rebuilds the OCI uncompressed layout for each individual Zarf package inside `.cache/neutrino/<hash>/extracted-packages/`. It does this by hard-linking the opaque SHA-256 blobs onto filesystem files bearing their original registry annotations.
