# Neutrino

Neutrino inspects deployable package artifacts to reveal Helm chart values, templates, and package-provided configuration.

## Language

**Package Reader**:
The boundary that resolves a package artifact and exposes the metadata and payload descriptors needed for Neutrino inspection. It does not imply a complete extracted package filesystem.
_Avoid_: package puller, package extractor

**Package Root**:
The resolved, requested-platform OCI manifest and artifact classification from which a Package Reader selects package descriptors; it is pinned by digest.
_Avoid_: package manifest, root layer

**Package Snapshot**:
The immutable Package Root digest and the verified descriptor digests used for one inspection; it identifies both the input and reusable cached bytes.
_Avoid_: cache key, package version

**Package View**:
The complete, verified, read-only set of selected package files made available by a Package Reader to inspection; a missing or unverifiable selected file prevents inspection of that package.
_Avoid_: extracted package, package directory

**Bundle Inspection**:
An inspection of every package selected by a UDS bundle. It succeeds only if each selected package produces a complete Package View.
_Avoid_: partial bundle inspection, best-effort bundle inspection
