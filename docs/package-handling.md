# Package Handling

Neutrino works as a multi-tier introspection engine. It understands that you might be looking at a raw Helm chart, a curated Zarf package containing Helm charts, or an overarching UDS bundle containing Zarf packages.

This document covers how Neutrino knows what type of package it's looking at, and its strategy for handling local and remote variations of each.

## Differentiating Source Types

At its core, Neutrino relies on extension heuristics and OCI manifold layers to determine the source it's operating against.

- **Local tar.zst files**: Analyzed first by file extension or naming conventions (e.g. `uds-bundle-*.tar.zst`).
- **OCI Remote Registries**: Inspected using `oras` (OCI registry as storage) to read the manifest of the repository and determine its structure. By looking at layer annotations (like `application/vnd.zarf.config.v1+json` or the existence of `uds-bundle.yaml`), Neutrino immediately knows how to handle the remote without pulling the entire image.

## How Each Package is Handled

### 1. UDS Bundles

UDS bundles act as the highest-level orchestrator. They are composed of a `uds-bundle.yaml` which declares a series of embedded or external Zarf packges.

**Local Handling**: 
If a bundle is presented as a local uncompressed directory, Neutrino simply reads the `uds-bundle.yaml` from the root.
If presented as a compressed `.tar.zst`, Neutrino extracts it to an internal cache directory. But instead of parsing the whole bundle immediately, it uses the OCI layout (`index.json`) contained inside the tarball to locate and reconstruct individual embedded Zarf packages gracefully.

**Remote OCI Handling**:
Neutrino fetches only the root manifest layers from the OCI registry to locate the `uds-bundle.yaml`. It does not download the actual multi-gigabyte bundle blobs right away. Once it reads the manifest, it pulls only the lightweight layers containing the metadata needed for introspection.

### 2. Zarf Packages

Zarf packages are comprised of a `zarf.yaml` and a set of `components` carrying Helm charts, manifests, or images.

**Local and Remote**:
If provided a local directory, Neutrino treats it as an uncompressed package. If pulling from remote, it leverages Zarf's built-in `PullOptions` to fetch the tarball down to a temporary directory.

**Component Extraction**:
Once a Zarf package is cached or pulled locally, Neutrino cracks it open, iterating through `.tar` archives in the `components/` directory. It unpacks the `charts/` directories and automatically invokes the Helm Introspection tier against each discovered chart.

### 3. Helm Charts

The foundational step for all package handling is the Helm Chart. 

**Loading Options**: 
Charts can be referenced locally directly matching a path, or fetched via OCI (`oci://`) or standard HTTP(s) repo archives (`.tgz`). 

Neutrino handles Helm archives by downloading them into a managed cache directory hash, extracting the `Chart.yaml`, and compiling its bundled `values.yaml` against any custom overrides provided upstream by the Zarf package wrapper.
