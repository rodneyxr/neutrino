# Caching 

Pulling remote Helm charts, OCI-based Zarf packages, and large UDS bundles is operationally expensive. Without a caching mechanism, introspection utilities are forced to redownload multi-gigabyte layers frequently, making development slow and tedious.

Neutrino employs a specialized local-caching strategy to persist metadata, parsed values, and root extraction directories.

## Hash-based Cache Directories

All network artifacts downloaded by Neutrino are stored within your user Home directory under `.cache/neutrino`:
```
~/.cache/neutrino/
  ├── charts/
  ├── uds-bundles/
  └── zarf-pkgs/
```

Inside each namespace, Neutrino uses SHA-256 hashes generated from a combination of the asset's **URL** (or local file path) and its **Version**. These unique hashes serve as extraction and pull targets.

For example, when fetching a Zarf package:
1. Neutrino dynamically constructs: `sha256(URL + Version + Arch)`.
2. It looks up `~/.cache/neutrino/zarf-pkgs/<hash>`.
3. If `zarf.yaml` exists in that directory, Neutrino bypasses downloading and extraction altogether, directly reading the local cache.

## Cache Invalidation and Bypassing

You may frequently find yourself needing to re-introspect an artifact without pulling from a stale cache, especially during active development of a Zarf Package.

Neutrino provides a `--no-cache` argument to forcefully skip cache loading mechanisms. Under the hood:
1. Neutrino intercepts the hash-directory.
2. It deletes `~/.cache/neutrino/<type>/<hash>` recursively.
3. Recreates the directory, pulling fresh remote source blobs directly into it.

This implementation allows you to explicitly drop the cache exclusively for the artifacts undergoing development, leaving your overall local cache entirely intact.
