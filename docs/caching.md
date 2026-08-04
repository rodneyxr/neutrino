# Caching

Pulling remote Helm charts, OCI-based Zarf packages, and large UDS bundles is operationally expensive. Without a caching mechanism, introspection tools are forced to redownload multi-gigabyte layers frequently, making development slow and tedious.

Neutrino employs a local caching strategy to persist metadata, parsed values, and extraction directories.

## Cache Structure

All network artifacts downloaded by Neutrino are stored under `~/.cache/neutrino/`:

```
~/.cache/neutrino/
├── charts/          # Extracted Helm chart directories
├── uds-bundles/     # Extracted UDS bundle metadata
└── zarf-pkgs/       # Extracted Zarf package contents
```

## Hash-Based Cache Keys

Inside each namespace, Neutrino uses SHA-256 hashes generated from a combination of the asset's **URL** (or local file path), **version**, and **architecture**. These unique hashes serve as extraction and pull targets.

For example, when fetching a Zarf package:

1. Neutrino constructs: `sha256(URL + Version + Arch)`.
2. Looks up `~/.cache/neutrino/zarf-pkgs/<hash>`.
3. If `zarf.yaml` exists in that directory, Neutrino bypasses downloading and extraction altogether, reading directly from cache.

This means subsequent inspections of the same artifact are nearly instant — no network I/O required.

## Cache Invalidation

You may frequently need to re-introspect an artifact without reading from a stale cache, especially during active development.

Neutrino provides the `--no-cache` flag to force a fresh download:

```bash
neutrino inspect oci://ghcr.io/my-org/my-package:latest --no-cache
```

Under the hood:

1. Neutrino computes the hash-directory path.
2. Deletes `~/.cache/neutrino/<type>/<hash>` recursively.
3. Recreates the directory and pulls fresh data.

!!! tip
    The `--no-cache` flag only invalidates the cache for the specific artifact being inspected. All other cached artifacts remain intact, so you don't lose your entire local cache.

## Cache Behavior by Package Type

| Package Type | What's Cached | Cache Hit Criteria |
|:---|:---|:---|
| **Helm Chart** | Extracted chart directory (`Chart.yaml`, `values.yaml`, `templates/`) | `Chart.yaml` exists in hash directory |
| **Zarf Package** | Full extracted package (`zarf.yaml`, `components/`) | `zarf.yaml` exists in hash directory |
| **UDS Bundle** | Bundle metadata + individually extracted Zarf packages | `uds-bundle.yaml` exists in hash directory |

For UDS bundles, each embedded Zarf package is cached separately under `zarf-pkgs/`, so re-inspecting a bundle that shares packages with other bundles benefits from cross-bundle cache hits.
